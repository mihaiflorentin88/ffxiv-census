package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// ProxyDialError marks a proven failure to establish or negotiate the
// connection to the proxy itself: the TCP dial, the SOCKS handshake, or a
// proxy-side refusal. Target-side TLS, HTTP, and payload errors are never
// wrapped in this type, so consumers can classify dial failures as
// proxy deaths without string matching.
type ProxyDialError struct {
	Reason string
	Err    error
}

func (e *ProxyDialError) Error() string { return "proxy " + e.Reason + ": " + e.Err.Error() }

// Unwrap exposes the wrapped cause so errors.Is/errors.As traverse it.
func (e *ProxyDialError) Unwrap() error { return e.Err }

// proxyDialer mirrors the standard library's DefaultTransport dialer so the
// shared transport keeps its bounded dial timeout and keep-alives.
var proxyDialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

// NewProxyTransport builds an *http.Transport that routes every request
// through the given proxy URL. Supported schemes are http, https, socks5,
// and socks4. The transport starts from a clone of the standard library's
// DefaultTransport with the inherited ProxyFromEnvironment explicitly
// cleared; only the selected proxy is assigned.
//
// timeout bounds the SOCKS4 dial and handshake; other schemes are bounded by
// the caller's client timeout and request context. Dial and negotiation
// failures are wrapped in *ProxyDialError.
func NewProxyTransport(proxyURL string, timeout time.Duration) (*http.Transport, error) {
	if proxyURL == "" {
		return nil, errors.New("proxy URL is empty")
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil // explicitly clear inherited ProxyFromEnvironment

	switch u.Scheme {
	case "http", "https":
		base.Proxy = http.ProxyURL(u)
		base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := proxyDialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, &ProxyDialError{Reason: "dial", Err: err}
			}
			return conn, nil
		}
	case "socks5":
		dialer, err := xproxy.FromURL(u, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create socks5 dialer: %w", err)
		}
		ctxDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer does not support context")
		}
		base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := ctxDialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, &ProxyDialError{Reason: "socks5", Err: err}
			}
			return conn, nil
		}
	case "socks4":
		base.DialContext = socks4DialContext(u, timeout)
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", u.Scheme)
	}
	return base, nil
}

// NewDirectTransport builds an *http.Transport without any proxy. The
// inherited ProxyFromEnvironment is explicitly cleared, so control-plane
// checks never route through ambient proxies.
func NewDirectTransport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	return base
}

// socks4DialContext returns the transport dial function for SOCKS4 proxies.
// It performs a real bounded handshake: the TCP dial and every handshake
// step respect the caller's context and the configured timeout, the
// connection is closed through context.AfterFunc on cancellation, and no
// abandoned dial goroutines are left behind. SOCKS4 only supports IPv4
// targets; hostnames are resolved locally over ip4, and an IPv6 target is a
// caller-side capability error, not a proxy failure.
func socks4DialContext(u *url.URL, timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("socks4 target address: %w", err)
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("socks4 target port: %w", err)
		}
		var targetIP [4]byte
		if literal := net.ParseIP(host); literal != nil {
			v4 := literal.To4()
			if v4 == nil {
				return nil, fmt.Errorf("socks4 requires an IPv4 target, got %q", host)
			}
			copy(targetIP[:], v4)
		} else {
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
			if err != nil {
				return nil, fmt.Errorf("socks4 resolve target: %w", err)
			}
			ip := addrs[0].Unmap()
			if !ip.Is4() {
				return nil, fmt.Errorf("socks4 requires an IPv4 target, %q resolved to %s", host, ip)
			}
			targetIP = ip.As4()
		}

		proxyAddr := net.JoinHostPort(u.Hostname(), u.Port())
		conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, &ProxyDialError{Reason: "socks4 dial", Err: err}
		}
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()

		deadline := time.Now().Add(timeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)

		req := []byte{
			0x04, 0x01, // SOCKS version 4, command CONNECT
			byte(port >> 8), byte(port),
			targetIP[0], targetIP[1], targetIP[2], targetIP[3],
			0x00, // empty USERID terminator
		}
		if _, err := conn.Write(req); err != nil {
			_ = conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, &ProxyDialError{Reason: "socks4 handshake", Err: fmt.Errorf("write request: %w", err)}
		}
		reply := make([]byte, 8)
		if _, err := io.ReadFull(conn, reply); err != nil {
			_ = conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, &ProxyDialError{Reason: "socks4 handshake", Err: fmt.Errorf("read reply: %w", err)}
		}
		if reply[1] != 0x5A { // 0x5A (90) is granted
			_ = conn.Close()
			return nil, &ProxyDialError{Reason: "socks4 negotiation", Err: fmt.Errorf("proxy rejected CONNECT with status %d", reply[1])}
		}

		// Success: clear the handshake deadline; the HTTP request context
		// now owns the connection.
		_ = conn.SetDeadline(time.Time{})
		if err := ctx.Err(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
}
