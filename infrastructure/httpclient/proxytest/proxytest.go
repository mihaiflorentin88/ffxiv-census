// Package proxytest provides local proxy and target server fixtures for
// transport tests: an HTTP CONNECT/forward proxy, SOCKS4/SOCKS5 servers, a
// TLS target, and helpers to observe connection teardown. It exists for
// tests only and carries no production behavior.
package proxytest

import (
	"bufio"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// guardDeadline bounds every fixture connection so a leaked connection can
// never pin a test goroutine forever.
const guardDeadline = 60 * time.Second

// TLSTarget starts an HTTPS test server that answers every request with body
// and counts handled requests. Only the returned server's own fixture
// certificate is trusted; use CertPool to build that pool.
func TLSTarget(t *testing.T, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// StallingBodyTLSTarget starts an HTTPS target that writes prefix and then
// stalls until the client disconnects, for body-stall regressions.
func StallingBodyTLSTarget(t *testing.T, prefix string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, prefix)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// CertPool returns a pool trusting only the fixture server's certificate.
func CertPool(srv *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

// ClosedPort allocates a listener, records its port, and closes it, yielding
// a port that currently refuses connections.
func ClosedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return port
}

// StallingTCP starts a listener that accepts connections and never speaks,
// for TLS-handshake stall regressions.
func StallingTCP(t *testing.T) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(guardDeadline))
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

// HTTPProxy is a minimal local HTTP proxy. It counts CONNECT tunnels and
// absolute-form forwarded requests separately and tracks open client
// connections so tests can observe teardown. With RefuseStatus set it
// accepts TCP but answers every CONNECT and forwarded request with that
// status instead of reaching the target.
type HTTPProxy struct {
	ln           net.Listener
	RefuseStatus int
	Connects     atomic.Int64
	Forwards     atomic.Int64
	Open         atomic.Int64
}

// NewHTTPProxy starts the proxy and registers its shutdown with the test.
func NewHTTPProxy(t *testing.T) *HTTPProxy { return newHTTPProxy(t, 0) }

// NewRefusingHTTPProxy starts a proxy that accepts TCP and answers every
// CONNECT (and absolute-form request) with status instead of tunneling,
// for CONNECT-refusal regressions.
func NewRefusingHTTPProxy(t *testing.T, status int) *HTTPProxy {
	return newHTTPProxy(t, status)
}

func newHTTPProxy(t *testing.T, refuseStatus int) *HTTPProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &HTTPProxy{ln: ln, RefuseStatus: refuseStatus}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

// Port returns the proxy's local port.
func (p *HTTPProxy) Port() int { return p.ln.Addr().(*net.TCPAddr).Port }

// Addr returns the proxy's host:port address.
func (p *HTTPProxy) Addr() string { return p.ln.Addr().String() }

// WaitConnsClosed fails the test if client connections remain open after d.
func (p *HTTPProxy) WaitConnsClosed(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for p.Open.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d proxy connection(s) still open after %s", p.Open.Load(), d)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (p *HTTPProxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

func (p *HTTPProxy) handle(conn net.Conn) {
	p.Open.Add(1)
	defer p.Open.Add(-1)
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(guardDeadline))

	br := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if req.Method == http.MethodConnect {
			p.Connects.Add(1)
			if p.RefuseStatus != 0 {
				p.reject(conn)
				return
			}
			p.tunnel(conn, req.Host)
			return
		}
		p.Forwards.Add(1)
		if p.RefuseStatus != 0 {
			p.reject(conn)
			return
		}
		p.forward(conn, req)
	}
}

// reject answers the request with the configured refusal status and closes
// the connection; the deferred close in handle runs right after.
func (p *HTTPProxy) reject(conn net.Conn) {
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nConnection: close\r\n\r\n",
		p.RefuseStatus, http.StatusText(p.RefuseStatus))
}

func (p *HTTPProxy) tunnel(conn net.Conn, host string) {
	upstream, err := net.Dial("tcp", host)
	if err != nil {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n\r\n", http.StatusBadGateway, http.StatusText(http.StatusBadGateway))
		return
	}
	defer func() { _ = upstream.Close() }()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	// Tunnel lifetime is owned by the tunneled peers; re-arm the guard.
	_ = conn.SetDeadline(time.Now().Add(guardDeadline))
	_ = upstream.SetDeadline(time.Now().Add(guardDeadline))
	relay(conn, upstream)
}

func (p *HTTPProxy) forward(conn net.Conn, req *http.Request) {
	req.RequestURI = ""
	resp, err := forwardClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n\r\n", http.StatusBadGateway, http.StatusText(http.StatusBadGateway))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_ = resp.Write(conn)
}

// forwardClient never follows redirects, so proxied redirect responses
// reach the caller unchanged. Its timeout bounds fixture cleanup when a
// check aborts mid-forward: a raw ReadRequest conn carries no server
// context, so the forward cannot observe the inbound close.
var forwardClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       2 * time.Second,
}

func relay(a, b net.Conn) {
	go func() {
		_, _ = io.Copy(b, a)
		_ = b.Close()
	}()
	_, _ = io.Copy(a, b)
	_ = a.Close()
}

// SOCKS4Proxy is a minimal local SOCKS4 server. With Stall set it accepts
// TCP but never answers the handshake. With GarbageVN set it answers the
// handshake with a malformed nonzero version byte.
type SOCKS4Proxy struct {
	ln        net.Listener
	Stall     bool
	GarbageVN bool
	Connects  atomic.Int64
	Open      atomic.Int64

	mu     sync.Mutex
	target string
}

// NewSOCKS4Proxy starts a SOCKS4 server that tunnels to the requested target.
func NewSOCKS4Proxy(t *testing.T) *SOCKS4Proxy { return newSOCKS4Proxy(t, false, false) }

// NewStalledSOCKS4 starts a SOCKS4 server that accepts TCP and never answers
// the handshake.
func NewStalledSOCKS4(t *testing.T) *SOCKS4Proxy { return newSOCKS4Proxy(t, true, false) }

// NewGarbageVNSOCKS4 starts a SOCKS4 server that answers the handshake with
// a nonzero version byte (and a granted status byte), for malformed-reply
// regressions.
func NewGarbageVNSOCKS4(t *testing.T) *SOCKS4Proxy { return newSOCKS4Proxy(t, false, true) }

func newSOCKS4Proxy(t *testing.T, stall, garbageVN bool) *SOCKS4Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &SOCKS4Proxy{ln: ln, Stall: stall, GarbageVN: garbageVN}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

// Port returns the proxy's local port.
func (p *SOCKS4Proxy) Port() int { return p.ln.Addr().(*net.TCPAddr).Port }

// Addr returns the proxy's host:port address.
func (p *SOCKS4Proxy) Addr() string { return p.ln.Addr().String() }

// Target returns the target address from the last parsed CONNECT request.
func (p *SOCKS4Proxy) Target() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.target
}

// WaitConnsClosed fails the test if client connections remain open after d.
func (p *SOCKS4Proxy) WaitConnsClosed(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for p.Open.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d socks4 connection(s) still open after %s", p.Open.Load(), d)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (p *SOCKS4Proxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

func (p *SOCKS4Proxy) handle(conn net.Conn) {
	p.Open.Add(1)
	defer p.Open.Add(-1)
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(guardDeadline))

	if p.Stall {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	if p.GarbageVN {
		// Malformed reply: the version byte must be null per the SOCKS4
		// spec; the granted status byte must not make the client accept it.
		_, _ = conn.Write([]byte{0x01, 0x5A, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}

	head := make([]byte, 8) // VN CD DSTPORT(2) DSTIP(4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if head[0] != 0x04 || head[1] != 0x01 {
		return
	}
	// USERID is NUL-terminated; the fixture requires the empty userid.
	userid, err := bufio.NewReader(conn).ReadBytes(0x00)
	if err != nil || len(userid) != 1 {
		return
	}
	port := int(head[2])<<8 | int(head[3])
	ip := net.IP(head[4:8])
	p.mu.Lock()
	p.target = net.JoinHostPort(ip.String(), strconv.Itoa(port))
	p.mu.Unlock()
	p.Connects.Add(1)

	upstream, err := net.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		_, _ = conn.Write([]byte{0x00, 0x5B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}
	defer func() { _ = upstream.Close() }()
	_, _ = conn.Write([]byte{0x00, 0x5A, head[2], head[3], head[4], head[5], head[6], head[7]})
	_ = conn.SetDeadline(time.Now().Add(guardDeadline))
	_ = upstream.SetDeadline(time.Now().Add(guardDeadline))
	relay(conn, upstream)
}

// SOCKS5Proxy is a minimal local SOCKS5 server (no-auth) that tunnels to the
// requested target.
type SOCKS5Proxy struct {
	ln       net.Listener
	Connects atomic.Int64
	Open     atomic.Int64

	mu     sync.Mutex
	target string
}

// NewSOCKS5Proxy starts the SOCKS5 server and registers its shutdown.
func NewSOCKS5Proxy(t *testing.T) *SOCKS5Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &SOCKS5Proxy{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

// Port returns the proxy's local port.
func (p *SOCKS5Proxy) Port() int { return p.ln.Addr().(*net.TCPAddr).Port }

// Addr returns the proxy's host:port address.
func (p *SOCKS5Proxy) Addr() string { return p.ln.Addr().String() }

// Target returns the target address from the last parsed CONNECT request.
func (p *SOCKS5Proxy) Target() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.target
}

// WaitConnsClosed fails the test if client connections remain open after d.
func (p *SOCKS5Proxy) WaitConnsClosed(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for p.Open.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d socks5 connection(s) still open after %s", p.Open.Load(), d)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (p *SOCKS5Proxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

func (p *SOCKS5Proxy) handle(conn net.Conn) {
	p.Open.Add(1)
	defer p.Open.Add(-1)
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(guardDeadline))

	greeting := make([]byte, 2) // VER NMETHODS
	if _, err := io.ReadFull(conn, greeting); err != nil || greeting[0] != 0x05 {
		return
	}
	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	head := make([]byte, 4) // VER CMD RSV ATYP
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 0x01 {
		return
	}
	var host string
	switch head[3] {
	case 0x01:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		name := make([]byte, l[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	case 0x04:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes)
	p.mu.Lock()
	p.target = net.JoinHostPort(host, strconv.Itoa(int(port)))
	p.mu.Unlock()
	p.Connects.Add(1)

	upstream, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = upstream.Close() }()
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	_ = conn.SetDeadline(time.Now().Add(guardDeadline))
	_ = upstream.SetDeadline(time.Now().Add(guardDeadline))
	relay(conn, upstream)
}
