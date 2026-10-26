//go:build linux

package ipc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixListenDialAndPeerUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")
	ln, err := Listen(Endpoint{Network: NetworkUnix, Address: path}, WithAllowUID(os.Getuid()))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	server := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			server <- err
			return
		}
		defer conn.Close()
		peer, err := PeerIdentityOf(conn)
		if err != nil {
			server <- err
			return
		}
		if peer.UID != os.Getuid() {
			server <- errors.New("unexpected peer uid")
			return
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			server <- err
			return
		}
		_, err = conn.Write(buf)
		server <- err
	}()

	conn, err := Dial(context.Background(), Endpoint{Network: NetworkUnix, Address: path}, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
	if err := <-server; err != nil {
		t.Fatalf("server: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
	}
}

func TestUnixListenerRejectsForeignUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")
	// The current process is the only possible connector here, so an
	// impossible allowed UID must reject it.
	ln, err := Listen(Endpoint{Network: NetworkUnix, Address: path}, WithAllowUID(os.Getuid()+1))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	// Connect succeeds at the socket layer, but the listener verifies the
	// peer on Accept and closes a foreign connection. The read observes that
	// closure instead of waiting for Accept, which intentionally keeps
	// serving other callers.
	conn, err := Dial(context.Background(), Endpoint{Network: NetworkUnix, Address: path}, 2*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("foreign peer was accepted")
	}
}

func TestDialTimeout(t *testing.T) {
	dir := t.TempDir()
	_, err := Dial(context.Background(), Endpoint{Network: NetworkUnix, Address: filepath.Join(dir, "missing.sock")}, 100*time.Millisecond)
	if err == nil {
		t.Fatal("Dial succeeded on a missing socket")
	}
}
