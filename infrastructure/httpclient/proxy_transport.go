package httpclient

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"

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

// WrapProxyDial re-attributes a transport error as a typed conclusive proxy
// failure when the chain contains a *ProxyDialError; any other error is
// returned unchanged so ambiguous failures stay unclassified. The original
// error stays in the %w chain for errors.As/errors.Is traversal.
func WrapProxyDial(err error) error {
	var dialErr *ProxyDialError
	if errors.As(err, &dialErr) {
		return &contract.ProxyCheckError{Kind: contract.CheckProxy, Reason: "proxy " + dialErr.Reason, Err: err}
	}
	return err
}

// ParseRetryAfter parses a Retry-After hint as delta-seconds or an HTTP
// date. The date form yields the duration remaining at receivedAt, never an
// absolute timestamp. Invalid or non-positive hints return 0 so consumers
// apply their normal cooldown floor.
func ParseRetryAfter(hint string, receivedAt time.Time) time.Duration {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return 0
	}
	if secs, err := strconv.Atoi(hint); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(hint); err == nil {
		d := t.Sub(receivedAt)
		if d <= 0 {
			return 0
		}
		return d
	}
	return 0
}

// proxyDialer mirrors the standard library's DefaultTransport dialer so the
// shared transport keeps its bounded dial timeout and keep-alives.
var proxyDialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

// browserTLSConfig builds a utls config whose ClientHello mimics a current
// Chrome release. Fronting services (Cloudflare in front of The Lodestone)
// fingerprint the TLS ClientHello and serve HTTP 202 challenge interstitials
// to non-browser fingerprints before the request reaches the origin; the
// stock crypto/tls hello is distinctive and was being challenged fleet-wide.
// ALPN is pinned to http/1.1 because net/http does not speak h2 over a
// custom DialTLSContext.
func browserTLSConfig(host string, base *tls.Config) *utls.Config {
	cfg := &utls.Config{
		ServerName: host,
		NextProtos: []string{"http/1.1"},
	}
	if base != nil {
		cfg.RootCAs = base.RootCAs
		cfg.InsecureSkipVerify = base.InsecureSkipVerify
		if base.ServerName != "" {
			cfg.ServerName = base.ServerName
		}
	}
	return cfg
}

func browserTLSClient(ctx context.Context, conn net.Conn, host string, tlsCfg *tls.Config, handshakeTimeout time.Duration) (net.Conn, error) {
	// Start from the Chrome ClientHello spec, then pin its ALPN extension to
	// http/1.1: the preset ships h2-first ALPN, and net/http does not speak
	// h2 over a custom DialTLSContext. Ciphers, extensions and curves — the
	// parts fingerprinting scores — are untouched.
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_120)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("build chrome client hello: %w", err)
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
		}
	}
	uconn := utls.UClient(conn, browserTLSConfig(host, tlsCfg), utls.HelloCustom)
	if err := uconn.ApplyPreset(&spec); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("apply chrome client hello: %w", err)
	}
	// utls's handshake does not abort on context cancellation at every read,
	// and net/http applies TLSHandshakeTimeout only to its own TLS path, so
	// bound the handshake explicitly: the earliest of the context deadline
	// and the transport's handshake timeout.
	deadline := time.Now().Add(10 * time.Second)
	if handshakeTimeout > 0 {
		deadline = time.Now().Add(handshakeTimeout)
	}
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	err = uconn.HandshakeContext(ctx)
	if err != nil {
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{}) // clear before the HTTP exchange
	return uconn, nil
}

// NewProxyTransport builds an *http.Transport that routes every request
// through the given proxy URL. Supported schemes are http, https, socks5,
// and socks4. The transport starts from a clone of the standard library's
// DefaultTransport with the inherited ProxyFromEnvironment explicitly
// cleared; the selected proxy is applied by the dial function.
//
// HTTP and HTTPS proxies complete their CONNECT handshake inside the dial
// function, so a proxy that answers CONNECT with a non-2xx status is a
// proven proxy failure. timeout bounds the SOCKS4 dial and handshake; other
// schemes are bounded by the caller's client timeout and request context.
// Dial and negotiation failures are wrapped in *ProxyDialError.
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

	var dial func(ctx context.Context, network, addr string) (net.Conn, error)
	switch u.Scheme {
	case "http", "https":
		dial = httpConnectDialContext(u, base)
	case "socks5":
		dialer, err := xproxy.FromURL(u, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create socks5 dialer: %w", err)
		}
		ctxDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer does not support context")
		}
		dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := ctxDialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, &ProxyDialError{Reason: "socks5", Err: err}
			}
			return conn, nil
		}
	case "socks4":
		dial = socks4DialContext(u, timeout)
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", u.Scheme)
	}
	base.DialContext = dial
	// HTTPS targets are tunneled by the same dial function and then handed
	// a browser-mimicking TLS handshake (see browserTLSConfig). TLSClientConfig
	// is read at dial time so callers can still inject trust roots after
	// construction.
	base.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return browserTLSClient(ctx, conn, host, base.TLSClientConfig, base.TLSHandshakeTimeout)
	}
	base.ForceAttemptHTTP2 = false
	return base, nil
}

// NewDirectTransport builds an *http.Transport without any proxy. The
// inherited ProxyFromEnvironment is explicitly cleared, so control-plane
// checks never route through ambient proxies. TLS handshakes use the
// browser-mimicking fingerprint (see browserTLSConfig).
func NewDirectTransport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	dialer := proxyDialer
	base.DialContext = dialer.DialContext
	base.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return browserTLSClient(ctx, conn, host, base.TLSClientConfig, base.TLSHandshakeTimeout)
	}
	base.ForceAttemptHTTP2 = false
	return base
}

// httpConnectDialContext returns the transport dial function for HTTP and
// HTTPS proxies. It performs the CONNECT handshake itself instead of leaving
// it to net/http, so every proxy-side failure — the TCP dial, TLS to the
// proxy, a request write or response read error, and a non-2xx CONNECT
// refusal — is wrapped in *ProxyDialError and classifies as a conclusive
// proxy failure. Success yields the established tunnel to addr.
func httpConnectDialContext(u *url.URL, transport *http.Transport) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		proxyPort := u.Port()
		if proxyPort == "" {
			proxyPort = "80"
			if u.Scheme == "https" {
				proxyPort = "443"
			}
		}
		conn, err := proxyDialer.DialContext(ctx, network, net.JoinHostPort(u.Hostname(), proxyPort))
		if err != nil {
			return nil, &ProxyDialError{Reason: "dial", Err: err}
		}
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()

		if d, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(d)
		}

		if u.Scheme == "https" {
			cfg := transport.TLSClientConfig.Clone()
			if cfg == nil {
				cfg = &tls.Config{}
			}
			if cfg.ServerName == "" {
				cfg.ServerName = u.Hostname()
			}
			tconn := tls.Client(conn, cfg)
			if err := tconn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				return nil, &ProxyDialError{Reason: "proxy tls", Err: fmt.Errorf("tls handshake with proxy: %w", err)}
			}
			conn = tconn
		}

		req := &http.Request{
			Method: http.MethodConnect,
			URL:    &url.URL{Opaque: addr},
			Host:   addr,
			Header: make(http.Header),
		}
		if u.User != nil {
			password, _ := u.User.Password()
			req.SetBasicAuth(u.User.Username(), password)
		}
		if err := req.Write(conn); err != nil {
			_ = conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, &ProxyDialError{Reason: "connect", Err: fmt.Errorf("write CONNECT: %w", err)}
		}
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			_ = conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, &ProxyDialError{Reason: "connect", Err: fmt.Errorf("read CONNECT response: %w", err)}
		}
		if resp.StatusCode/100 != 2 {
			_ = conn.Close()
			return nil, &ProxyDialError{Reason: "connect", Err: fmt.Errorf("proxy refused CONNECT with %s", resp.Status)}
		}

		// Success: clear the handshake deadline; the HTTP request context
		// now owns the connection. The reply has no body, so tunnel bytes
		// continue on the same stream; anything the header reader already
		// buffered must not be lost.
		_ = conn.SetDeadline(time.Time{})
		if err := ctx.Err(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if br.Buffered() != 0 {
			return &tunnelConn{Conn: conn, r: br}, nil
		}
		return conn, nil
	}
}

// tunnelConn fuses the tunnel connection with CONNECT-response bytes the
// header reader already buffered, so tunnel data is never lost.
type tunnelConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *tunnelConn) Read(p []byte) (int, error) { return c.r.Read(p) }

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
		if reply[0] != 0x00 { // the SOCKS4 reply version byte must be null
			_ = conn.Close()
			return nil, &ProxyDialError{Reason: "socks4 negotiation", Err: fmt.Errorf("unexpected reply version %d", reply[0])}
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
