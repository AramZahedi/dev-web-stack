package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const wsAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func testTLSCertificate(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "devstack proxy test"},
		DNSNames:     []string{"blog.test", "www.test", "devstack.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startProxyServer(t *testing.T, snap Snapshot, maxBody int64) (*Server, string, string) {
	t.Helper()
	r := NewRouter()
	r.Replace(snap)
	s := NewServer(ServerConfig{
		HTTPAddr:     "127.0.0.1:0",
		HTTPSAddr:    "127.0.0.1:0",
		Router:       r,
		MaxBodyBytes: maxBody,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = s.Stop(context.Background())
	})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return s, s.HTTPAddr(), s.HTTPSAddr()
}

func noRedirectClient() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// tlsClientFor dials addr for every request while keeping the request URL's
// hostname as the SNI value. Tests use a self-signed certificate, so
// verification is skipped.
func tlsClientFor(addr string) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test certificate only
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func upstreamAddr(t *testing.T, rawURL string) string {
	t.Helper()
	return strings.TrimPrefix(strings.TrimPrefix(rawURL, "http://"), "https://")
}

func TestServerForceHTTPSRedirectsWithoutHSTS(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cert := testTLSCertificate(t)
	_, httpAddr, _ := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindStatic,
			Upstream: upstreamAddr(t, upstream.URL), TLS: true, ForceHTTPS: true,
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 0)

	req, err := http.NewRequest("GET", "http://"+httpAddr+"/deep/path?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "blog.test"
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), "https://blog.test/deep/path?x=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS emitted: %q", got)
	}
	if calls.Load() != 0 {
		t.Fatal("upstream was called for a redirect")
	}
}

func TestServerTLSDisabledRedirectsToHTTP(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cert := testTLSCertificate(t)
	_, _, httpsAddr := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindNode,
			Upstream: upstreamAddr(t, upstream.URL), TLS: false,
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 0)

	resp, err := tlsClientFor(httpsAddr).Get("https://blog.test/keep?q=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), "http://blog.test/keep?q=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS emitted: %q", got)
	}
	if calls.Load() != 0 {
		t.Fatal("upstream was called for a redirect")
	}
}

type forwardedRequest struct {
	host      string
	forwarded string
	proto     string
	xHost     string
}

func TestServerHTTP2AndForwardedHeaders(t *testing.T) {
	seen := make(chan forwardedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- forwardedRequest{
			host:      r.Host,
			forwarded: r.Header.Get("X-Forwarded-For"),
			proto:     r.Header.Get("X-Forwarded-Proto"),
			xHost:     r.Header.Get("X-Forwarded-Host"),
		}
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cert := testTLSCertificate(t)
	s, _, httpsAddr := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindNode,
			Upstream: upstreamAddr(t, upstream.URL), TLS: true,
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 0)

	if !containsString(s.httpsSrv.TLSConfig.NextProtos, "h2") {
		t.Fatalf("TLS NextProtos = %v, want h2", s.httpsSrv.TLSConfig.NextProtos)
	}

	req, err := http.NewRequest(http.MethodGet, "https://blog.test/hello?x=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := tlsClientFor(httpsAddr).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("code=%d body=%q", resp.StatusCode, body)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("proto = %s, want HTTP/2", resp.Proto)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("upstream HSTS leaked through the proxy: %q", got)
	}

	select {
	case got := <-seen:
		if got.host != "blog.test" {
			t.Fatalf("upstream Host = %q, want original blog.test", got.host)
		}
		if got.proto != "https" {
			t.Fatalf("X-Forwarded-Proto = %q", got.proto)
		}
		if got.xHost != "blog.test" {
			t.Fatalf("X-Forwarded-Host = %q", got.xHost)
		}
		if got.forwarded != "127.0.0.1" {
			t.Fatalf("X-Forwarded-For = %q, want the real client address only", got.forwarded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive the request")
	}
}

func TestServerUpstreamFailureIsBranded502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()

	cert := testTLSCertificate(t)
	_, httpAddr, _ := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindNode, Upstream: deadAddr, TLS: true,
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 0)

	req, err := http.NewRequest("GET", "http://"+httpAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "blog.test"
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502; body=%q", resp.StatusCode, body)
	}
	for _, want := range []string{"blog.test", "devstack site logs"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("502 page missing %q: %q", want, body)
		}
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS emitted: %q", got)
	}
}

func TestServerRejectsInvalidUpstream(t *testing.T) {
	cert := testTLSCertificate(t)
	_, httpAddr, _ := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindNode, Upstream: "http://127.0.0.1:8080",
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 0)

	req, err := http.NewRequest("GET", "http://"+httpAddr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "blog.test"
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(string(body), "blog.test") {
		t.Fatalf("502 page does not name the site: %q", body)
	}
}

func TestServerBodyLimit(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cert := testTLSCertificate(t)
	_, httpAddr, _ := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindNode, Upstream: upstreamAddr(t, upstream.URL),
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 32)

	req, err := http.NewRequest("POST", "http://"+httpAddr+"/", bytes.NewReader(bytes.Repeat([]byte("x"), 1024)))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "blog.test"
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", resp.StatusCode)
	}
	if calls.Load() != 0 {
		t.Fatal("upstream was called for an oversized body")
	}
}

func TestServerBodyLimitChunked(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the stream so the proxy hits the limit while forwarding.
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cert := testTLSCertificate(t)
	_, httpAddr, _ := startProxyServer(t, Snapshot{
		Routes: map[string]Route{"blog.test": {
			SiteID: "site-1", Host: "blog.test", Kind: KindNode, Upstream: upstreamAddr(t, upstream.URL),
		}},
		Certs:       map[string]*tls.Certificate{"blog.test": cert},
		DefaultCert: cert,
	}, 32)

	req, err := http.NewRequest("POST", "http://"+httpAddr+"/", io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 1024))))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1 // force a chunked upload
	req.Host = "blog.test"
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413; body=%q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Request too large") {
		t.Fatalf("413 page not branded: %q", body)
	}
}

func TestServerStartRejectsBindConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := NewServer(ServerConfig{HTTPAddr: ln.Addr().String(), Router: NewRouter()})
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded on an occupied address")
	}

	noRouter := NewServer(ServerConfig{HTTPAddr: "127.0.0.1:0"})
	if err := noRouter.Start(context.Background()); err == nil {
		t.Fatal("Start accepted a nil router")
	}
}

func TestServerTimeoutConfigurationAndStop(t *testing.T) {
	s, httpAddr, _ := startProxyServer(t, Snapshot{}, 0)
	for _, srv := range []*http.Server{s.httpSrv, s.httpsSrv} {
		if srv.ReadHeaderTimeout != 30*time.Second {
			t.Fatalf("ReadHeaderTimeout = %v", srv.ReadHeaderTimeout)
		}
		if srv.IdleTimeout != 120*time.Second {
			t.Fatalf("IdleTimeout = %v", srv.IdleTimeout)
		}
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	conn, err := net.DialTimeout("tcp", httpAddr, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("HTTP listener still accepts connections after Stop")
	}
}

func TestRouterWebSocketEcho(t *testing.T) {
	wsUpstream := httptest.NewServer(http.HandlerFunc(wsEchoHandler))
	defer wsUpstream.Close()

	r := NewRouter()
	r.Replace(Snapshot{Routes: map[string]Route{"blog.test": {
		SiteID: "site-1", Host: "blog.test", Kind: KindNode, Upstream: upstreamAddr(t, wsUpstream.URL),
	}}})
	front := httptest.NewServer(r.Handler())
	defer front.Close()

	conn, br, resp := dialWebSocket(t, front.Listener.Addr().String(), "blog.test", "/socket?room=1")
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake code = %d", resp.StatusCode)
	}

	if err := writeMaskedWebSocketFrame(conn, 0x1, []byte("hello websocket")); err != nil {
		t.Fatal(err)
	}
	op, payload, err := readWebSocketFrame(br)
	if err != nil {
		t.Fatal(err)
	}
	if op != 0x1 || string(payload) != "hello websocket" {
		t.Fatalf("echo = opcode %d payload %q", op, payload)
	}
}

func TestRouterSSEPassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "data: first\n\n")
		fl.Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	r := NewRouter()
	r.Replace(Snapshot{Routes: map[string]Route{"blog.test": {
		SiteID: "site-1", Host: "blog.test", Kind: KindNode, Upstream: upstreamAddr(t, upstream.URL),
	}}})
	front := httptest.NewServer(r.Handler())
	defer front.Close()

	req, err := http.NewRequest("GET", front.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "blog.test"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}

	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "data: first\n" {
		t.Fatalf("first SSE line = %q", line)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func dialWebSocket(t *testing.T, addr, host, path string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	request := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, host, key,
	)
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		sum := sha1.Sum([]byte(key + wsAcceptGUID))
		want := base64.StdEncoding.EncodeToString(sum[:])
		if got := resp.Header.Get("Sec-WebSocket-Accept"); got != want {
			_ = conn.Close()
			t.Fatalf("Sec-WebSocket-Accept = %q, want %q", got, want)
		}
	}
	return conn, br, resp
}

func wsEchoHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	sum := sha1.Sum([]byte(key + wsAcceptGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	if _, err := fmt.Fprintf(rw,
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		return
	}
	if err := rw.Flush(); err != nil {
		return
	}

	for {
		op, payload, err := readWebSocketFrame(rw.Reader)
		if err != nil {
			return
		}
		switch op {
		case 0x8: // close
			_ = writeWebSocketFrame(conn, 0x8, payload)
			return
		case 0x1: // text
			if err := writeWebSocketFrame(conn, 0x1, payload); err != nil {
				return
			}
		}
	}
}

func readWebSocketFrame(r *bufio.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	op := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(r, extended); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(r, extended); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended)
	}
	if length > 1<<20 {
		return 0, nil, fmt.Errorf("websocket frame too large: %d", length)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}

func writeWebSocketFrame(w io.Writer, op byte, payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("test websocket frame too large: %d", len(payload))
	}
	frame := make([]byte, 0, 2+len(payload))
	frame = append(frame, 0x80|op, byte(len(payload)))
	frame = append(frame, payload...)
	_, err := w.Write(frame)
	return err
}

// writeMaskedWebSocketFrame writes a client-to-server frame. RFC 6455 requires
// client frames to be masked.
func writeMaskedWebSocketFrame(w io.Writer, op byte, payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("test websocket frame too large: %d", len(payload))
	}
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	frame := make([]byte, 0, 6+len(payload))
	frame = append(frame, 0x80|op, 0x80|byte(len(payload)))
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	_, err := w.Write(frame)
	return err
}
