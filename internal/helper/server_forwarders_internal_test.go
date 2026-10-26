//go:build !windows

package helper

import (
	"path/filepath"
	"testing"
)

func TestForwarderPortsDefaults(t *testing.T) {
	s := &Server{}
	httpListen, httpsListen, httpTarget, httpsTarget, err := s.forwarderPorts()
	if err != nil {
		t.Fatalf("forwarderPorts: %v", err)
	}
	if httpListen != 80 || httpsListen != 443 {
		t.Fatalf("listen ports = %d, %d; want 80, 443", httpListen, httpsListen)
	}
	if httpTarget != 8080 || httpsTarget != 8443 {
		t.Fatalf("proxy ports = %d, %d; want 8080, 8443", httpTarget, httpsTarget)
	}
}

func TestForwarderPortsValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{
			name: "zero-resolved listen ports equal",
			cfg:  Config{ForwardHTTPPort: 80, ForwardHTTPSPort: 80},
		},
		{
			name: "proxy ports equal",
			cfg:  Config{ProxyHTTPPort: 8080, ProxyHTTPSPort: 8080},
		},
		{
			name: "listen equals proxy",
			cfg:  Config{ForwardHTTPPort: 8080, ProxyHTTPPort: 8080},
		},
		{
			name: "cross listen and proxy equal",
			cfg:  Config{ForwardHTTPPort: 8443, ProxyHTTPSPort: 8443},
		},
		{
			name: "listen out of range",
			cfg:  Config{ForwardHTTPPort: 70000},
		},
		{
			name: "negative proxy port",
			cfg:  Config{ProxyHTTPPort: -1},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{cfg: tt.cfg}
			if _, _, _, _, err := s.forwarderPorts(); err == nil {
				t.Fatalf("forwarderPorts(%+v) succeeded, want an error", tt.cfg)
			}
		})
	}
}

func TestNewServerRejectsInvalidForwarderPorts(t *testing.T) {
	_, err := NewServer(Config{
		SocketPath:      filepath.Join(t.TempDir(), "helper.sock"),
		ForwardHTTPPort: 8080,
		ProxyHTTPPort:   8080,
	})
	if err == nil {
		t.Fatal("NewServer accepted colliding forwarder ports")
	}
}
