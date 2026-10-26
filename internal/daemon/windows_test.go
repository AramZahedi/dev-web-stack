//go:build windows

package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsRunOpensSQLiteWritesLogAndCleansReadiness(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Home: home, HTTPPort: freeTCPPort(t), HTTPSPort: freeTCPPort(t), UIPort: freeTCPPort(t)})
	}()
	ready := filepath.Join(home, "run", "daemon.ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned before readiness: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("daemon did not publish readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
	for _, path := range []string{filepath.Join(home, "state.db"), filepath.Join(home, "logs", "daemon.log")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("required Windows daemon file %q: %v", path, err)
		}
	}
	if _, err := os.Stat(ready); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("readiness marker after shutdown error = %v, want not-exist", err)
	}
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != home && strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("Windows daemon created dot-prefixed name %q", entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
