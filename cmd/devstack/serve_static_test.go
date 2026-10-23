package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeStaticCommandFlagsAndRootValidation(t *testing.T) {
	cmd := newServeStaticCmd()
	for _, name := range []string{"root", "addr", "spa"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("--%s flag missing", name)
		}
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--root") {
		t.Fatalf("Execute() error = %v, want a --root error", err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestRunServeStaticServesAndShutsDown(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("hello static"), 0o644); err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServeStatic(ctx, root, addr, false) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("serve-static did not stop")
		}
	})

	client := &http.Client{Timeout: time.Second}
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		resp, err = client.Get("http://" + addr + "/")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve-static did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello static" {
		t.Fatalf("code=%d body=%q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestRunServeStaticReturnsListenError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if err := runServeStatic(context.Background(), root, ln.Addr().String(), false); err == nil {
		t.Fatal("runServeStatic succeeded on an occupied address")
	}
}

func TestRunServeStaticValidatesRoot(t *testing.T) {
	if err := runServeStatic(context.Background(), "", "127.0.0.1:0", false); err == nil {
		t.Fatal("empty root accepted")
	}
	if err := runServeStatic(context.Background(), filepath.Join(t.TempDir(), "missing"), "127.0.0.1:0", false); err == nil {
		t.Fatal("missing root accepted")
	}
}
