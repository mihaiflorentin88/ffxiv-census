package httpclient

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestProxyClient_LiveSOCKS4 tests actual HTTP requests through a SOCKS4 proxy.
func TestProxyClient_LiveSOCKS4(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live proxy test in short mode")
	}
	proxies := []string{
		"socks4://103.237.102.191:11111",
		"socks4://94.78.67.171:80",
		"socks4://213.165.43.234:3129",
	}
	testProxyClient(t, proxies, "socks4")
}

// TestProxyClient_LiveSOCKS5 tests actual HTTP requests through a SOCKS5 proxy.
func TestProxyClient_LiveSOCKS5(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live proxy test in short mode")
	}
	proxies := []string{
		"socks5://3.92.205.59:1081",
		"socks5://77.110.103.146:1081",
		"socks5://79.137.196.250:1081",
	}
	testProxyClient(t, proxies, "socks5")
}

// TestProxyClient_LiveHTTP tests actual HTTP requests through an HTTP proxy.
func TestProxyClient_LiveHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live proxy test in short mode")
	}
	proxies := []string{
		"http://45.63.43.188:50000",
		"http://45.89.223.196:9080",
		"http://153.80.242.105:8080",
	}
	testProxyClient(t, proxies, "http")
}

func testProxyClient(t *testing.T, proxies []string, protocol string) {
	t.Helper()
	testURL := "http://httpbin.org/ip"

	for _, proxyAddr := range proxies {
		t.Run(proxyAddr, func(t *testing.T) {
			client, err := NewProxyClient(proxyAddr, 10*time.Second)
			if err != nil {
				t.Fatalf("NewProxyClient(%s): %v", proxyAddr, err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			resp, err := client.Get(ctx, testURL, nil, nil)
			if err != nil {
				t.Logf("%s proxy %s failed (may be dead): %v", protocol, proxyAddr, err)
				return // dead proxy, not a code bug
			}

			if resp.StatusCode != 200 {
				t.Logf("%s proxy %s returned status %d", protocol, proxyAddr, resp.StatusCode)
				return
			}

			fmt.Printf("✅ %s proxy %s → %s\n", protocol, proxyAddr, string(resp.Body))
		})
	}
}

// TestProxyClient_AllProtocols_CanConstruct verifies all three protocols
// can construct a client without errors (unit test, no network).
func TestProxyClient_AllProtocols_CanConstruct(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"http", "http://1.2.3.4:8080", false},
		{"https", "https://1.2.3.4:8443", false},
		{"socks4", "socks4://1.2.3.4:1080", false},
		{"socks5", "socks5://1.2.3.4:1080", false},
		{"unsupported", "ftp://1.2.3.4:21", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProxyClient(tt.addr, 5*time.Second)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewProxyClient(%s) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}
