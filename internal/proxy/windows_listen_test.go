//go:build windows

package proxy

import (
	"net"
	"strings"
	"testing"
)

func TestWindowsPublicListenerBindsLoopbackAndWSLAdapterAddresses(t *testing.T) {
	addr, err := WindowsPublicListenAddr(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "0.0.0.0:") {
		t.Fatalf("public listen address = %q, want IPv4 wildcard for loopback and WSL adapter", addr)
	}
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		t.Fatalf("bind wildcard public listener: %v", err)
	}
	defer listener.Close()
	if listener.Addr().(*net.TCPAddr).IP.IsUnspecified() != true {
		t.Fatalf("listener bound %v, want all local IPv4 interfaces", listener.Addr())
	}
}

func TestWindowsPublicListenerRejectsInvalidPort(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		if _, err := WindowsPublicListenAddr(port); err == nil {
			t.Errorf("WindowsPublicListenAddr(%d) succeeded", port)
		}
	}
}
