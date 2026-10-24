package forwarder_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/forwarder"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func readBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("request through forwarder: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func TestForwarderPassesTrafficAndDetectsConflict(t *testing.T) {
	ctx := context.Background()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	lp := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", lp)
	f := forwarder.New(listen, "", target.Listener.Addr().String(), "", slog.Default())
	if err := f.Start(ctx); err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	defer func() {
		if err := f.Stop(ctx); err != nil {
			t.Errorf("stop forwarder: %v", err)
		}
	}()

	body := readBody(t, "http://"+listen+"/")
	if body != "ok" {
		t.Fatalf("forwarded body = %q, want %q", body, "ok")
	}

	duplicate := forwarder.New(listen, "", target.Listener.Addr().String(), "", slog.Default())
	err := duplicate.Start(ctx)
	if err == nil {
		t.Fatal("want bind conflict")
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("conflict error = %v, want EADDRINUSE", err)
	}
	if !strings.Contains(err.Error(), listen) {
		t.Fatalf("conflict error = %q, want listen address %q", err, listen)
	}
}

func TestForwarderForwardsBothListeners(t *testing.T) {
	ctx := context.Background()
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "http-target")
	}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "https-target")
	}))
	defer httpsTarget.Close()

	httpPort := freePort(t)
	httpsPort := freePort(t)
	for httpsPort == httpPort {
		httpsPort = freePort(t)
	}
	httpListen := fmt.Sprintf("127.0.0.1:%d", httpPort)
	httpsListen := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	f := forwarder.New(httpListen, httpsListen, httpTarget.Listener.Addr().String(), httpsTarget.Listener.Addr().String(), slog.Default())
	if err := f.Start(ctx); err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	defer func() { _ = f.Stop(ctx) }()

	if got := readBody(t, "http://"+httpListen+"/"); got != "http-target" {
		t.Fatalf("HTTP forward = %q", got)
	}
	if got := readBody(t, "http://"+httpsListen+"/"); got != "https-target" {
		t.Fatalf("HTTPS forward = %q", got)
	}

	status := f.Status()
	if len(status) != 2 {
		t.Fatalf("status entries = %d, want 2", len(status))
	}
	if status[0].Name != "http" || status[0].Listen != httpListen || status[0].Target != httpTarget.Listener.Addr().String() || !status[0].OK {
		t.Fatalf("HTTP status = %+v", status[0])
	}
	if status[1].Name != "https" || status[1].Listen != httpsListen || status[1].Target != httpsTarget.Listener.Addr().String() || !status[1].OK {
		t.Fatalf("HTTPS status = %+v", status[1])
	}
}

func TestForwarderStatusStopAndIdempotence(t *testing.T) {
	ctx := context.Background()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	port := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	f := forwarder.New(listen, "", target.Listener.Addr().String(), "", slog.Default())

	before := f.Status()
	if len(before) != 1 {
		t.Fatalf("status entries = %d, want 1", len(before))
	}
	if before[0].OK {
		t.Fatalf("status before start = %+v, want not OK", before[0])
	}
	if before[0].Listen != listen || before[0].Target != target.Listener.Addr().String() {
		t.Fatalf("status before start = %+v", before[0])
	}

	if err := f.Start(ctx); err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	if err := f.Start(ctx); err == nil {
		t.Fatal("second start succeeded; want an error")
	}
	if got := f.Status()[0]; !got.OK || got.Error != "" {
		t.Fatalf("status after start = %+v", got)
	}

	if err := f.Stop(ctx); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := f.Stop(ctx); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	after := f.Status()[0]
	if after.OK {
		t.Fatalf("status after stop = %+v, want not OK", after)
	}
	if after.Error == "" {
		t.Fatalf("status after stop = %+v, want an explanation", after)
	}
	if conn, err := net.DialTimeout("tcp", listen, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("listener still accepts connections after Stop")
	}
}

func TestForwarderStopClosesActiveConnections(t *testing.T) {
	ctx := context.Background()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	port := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	f := forwarder.New(listen, "", target.Listener.Addr().String(), "", slog.Default())
	if err := f.Start(ctx); err != nil {
		t.Fatalf("start forwarder: %v", err)
	}

	conn, err := net.DialTimeout("tcp", listen, time.Second)
	if err != nil {
		t.Fatalf("dial forwarder: %v", err)
	}
	defer func() { _ = conn.Close() }()

	stopped := make(chan error, 1)
	go func() { stopped <- f.Stop(ctx) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop with an active connection: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked on an active connection")
	}
}

func TestForwarderRejectsInvalidTargets(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		target string
	}{
		{name: "public IP", target: "192.0.2.10:8080"},
		{name: "hostname", target: "example.com:8080"},
		{name: "missing port", target: "127.0.0.1"},
		{name: "zero port", target: "127.0.0.1:0"},
		{name: "out of range port", target: "127.0.0.1:70000"},
		{name: "empty", target: ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
			f := forwarder.New(addr, "", tt.target, "", slog.Default())
			err := f.Start(ctx)
			if err == nil {
				_ = f.Stop(ctx)
				t.Fatalf("Start accepted target %q", tt.target)
			}
			if !strings.Contains(err.Error(), "target") {
				t.Fatalf("error = %v, want a target explanation", err)
			}
			// A rejected configuration must not leave a listener behind.
			ln, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				t.Fatalf("listener left behind after rejection: %v", listenErr)
			}
			_ = ln.Close()
		})
	}
}

func TestForwarderAcceptsLoopbackIPv6Target(t *testing.T) {
	ctx := context.Background()
	target, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer func() { _ = target.Close() }()

	port := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	f := forwarder.New(listen, "", target.Addr().String(), "", slog.Default())
	if err := f.Start(ctx); err != nil {
		t.Fatalf("start forwarder with IPv6 loopback target: %v", err)
	}
	if err := f.Stop(ctx); err != nil {
		t.Fatalf("stop forwarder: %v", err)
	}
}
