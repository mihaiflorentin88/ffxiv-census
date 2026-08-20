package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	xproxy "golang.org/x/net/proxy"

	_ "github.com/bdandy/go-socks4" // register socks4 scheme with golang.org/x/net/proxy
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// NewProxyClient creates an HTTPClient that routes requests through the given proxy.
// The proxyAddr must include the protocol: http://ip:port, https://ip:port,
// socks4://ip:port, or socks5://ip:port.
func NewProxyClient(proxyAddr string, timeout time.Duration) (contract.HTTPClient, error) {
	if proxyAddr == "" {
		return nil, fmt.Errorf("proxy address is empty")
	}

	u, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("parse proxy address: %w", err)
	}

	var transport *http.Transport
	switch u.Scheme {
	case "http", "https":
		transport = &http.Transport{
			Proxy: http.ProxyURL(u),
		}
	case "socks5":
		dialer, err := xproxy.FromURL(u, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create socks dialer: %w", err)
		}
		ctxDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks dialer does not support context")
		}
		transport = &http.Transport{
			DialContext: ctxDialer.DialContext,
		}
	case "socks4":
		dialer, err := xproxy.FromURL(u, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create socks4 dialer: %w", err)
		}
		ctxDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			// go-socks4 dialer doesn't implement ContextDialer; wrap plain Dialer.
			transport = &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			}
		} else {
			transport = &http.Transport{DialContext: ctxDialer.DialContext}
		}
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", u.Scheme)
	}

	// Configure sensible defaults for the transport.
	transport.DialContext = wrapDialContext(transport.DialContext, 30*time.Second)

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	return New(client), nil
}

// wrapDialContext wraps a DialContext with a default timeout if nil.
func wrapDialContext(existing func(ctx context.Context, network, addr string) (net.Conn, error), timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if existing != nil {
		return existing
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		return d.DialContext(ctx, network, addr)
	}
}
