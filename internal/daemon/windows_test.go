//go:build windows

package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWindowsRunOpensSQLiteWritesLogAndCleansReadiness(t *testing.T) {
	home := t.TempDir()
	httpPort, httpsPort, uiPort := freeTCPPort(t), freeTCPPort(t), freeTCPPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Home: home, HTTPPort: httpPort, HTTPSPort: httpsPort, UIPort: uiPort})
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

func TestWindowsSQLiteHomeRequiresNTFS(t *testing.T) {
	original := windowsFileSystemForPath
	windowsFileSystemForPath = func(string) (string, error) { return "exFAT", nil }
	t.Cleanup(func() { windowsFileSystemForPath = original })
	if err := validateWindowsHome(t.TempDir()); err == nil || !strings.Contains(err.Error(), "NTFS") {
		t.Fatalf("validateWindowsHome error = %v, want NTFS requirement", err)
	}
}

func TestWindowsEnsureArgsPreserveRunOverrides(t *testing.T) {
	got := windowsDaemonArgs(Options{
		Home: `C:\devstack-test`, HelperSocket: `\\.\pipe\devstack-helper-test`,
		HostsPath: `C:\temp\hosts`, HTTPPort: 18080, HTTPSPort: 18443, UIPort: 17070, LogLevel: "debug",
	})
	want := []string{"daemon", "run", "--home", `C:\devstack-test`, "--helper-socket", `\\.\pipe\devstack-helper-test`, "--hosts", `C:\temp\hosts`, "--http-port", "18080", "--https-port", "18443", "--ui-port", "17070", "--log-level", "debug"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows daemon args = %#v, want %#v", got, want)
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
