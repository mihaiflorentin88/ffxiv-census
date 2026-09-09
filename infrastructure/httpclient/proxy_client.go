package httpclient

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// NewProxyClient creates an HTTPClient that routes requests through the given
// proxy. The proxyAddr must include the protocol: http://ip:port,
// https://ip:port, socks4://ip:port, or socks5://ip:port.
func NewProxyClient(proxyAddr string, timeout time.Duration) (contract.HTTPClient, error) {
	if proxyAddr == "" {
		return nil, fmt.Errorf("proxy address is empty")
	}
	if _, err := url.Parse(proxyAddr); err != nil {
		return nil, fmt.Errorf("parse proxy address: %w", err)
	}
	transport, err := NewProxyTransport(proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	return New(&http.Client{Transport: transport, Timeout: timeout}), nil
}
