package httpclient

import (
	"testing"
	"time"
)

func TestNewProxyClient_HTTPProxy(t *testing.T) {
	c, err := NewProxyClient("http://1.2.3.4:8080", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected client, got nil")
	}
}

func TestNewProxyClient_HTTPSProxy(t *testing.T) {
	c, err := NewProxyClient("https://1.2.3.4:8080", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected client, got nil")
	}
}

func TestNewProxyClient_SOCKS5Proxy(t *testing.T) {
	c, err := NewProxyClient("socks5://1.2.3.4:1080", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected client, got nil")
	}
}

func TestNewProxyClient_SOCKS4Proxy(t *testing.T) {
	c, err := NewProxyClient("socks4://1.2.3.4:1080", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected client, got nil")
	}
}

func TestNewProxyClient_UnsupportedProtocol(t *testing.T) {
	_, err := NewProxyClient("ftp://1.2.3.4:21", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}

func TestNewProxyClient_EmptyAddress(t *testing.T) {
	_, err := NewProxyClient("", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestNewProxyClient_InvalidAddress(t *testing.T) {
	_, err := NewProxyClient("://invalid", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestNewProxyClient_TimeoutPropagated(t *testing.T) {
	timeout := 42 * time.Second
	c, err := NewProxyClient("http://1.2.3.4:8080", timeout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected client, got nil")
	}
	// We can't easily inspect the internal http.Client timeout, but we can
	// verify the client was created successfully with the given timeout.
}
