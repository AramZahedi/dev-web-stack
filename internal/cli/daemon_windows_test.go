//go:build windows

package cli

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/daemon"
)

func TestWindowsCLIStatusAndStopOverNamedPipe(t *testing.T) {
	home := t.TempDir()
	httpPort, httpsPort, uiPort := freeTestTCPPort(t), freeTestTCPPort(t), freeTestTCPPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{
			Home:      home,
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
			UIPort:    uiPort,
		})
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
			t.Fatal("daemon did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 1. Status should report running
	st := readDaemonStatus(home)
	if !st.Running {
		t.Fatalf("readDaemonStatus reported not running, want running")
	}

	// 2. Stop over named pipe
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	stopped, err := stopDaemonPlatform(stopCtx, home)
	if err != nil {
		t.Fatalf("stopDaemonPlatform: %v", err)
	}
	if !stopped {
		t.Fatalf("stopDaemonPlatform reported stopped=false, want true")
	}

	// Wait for Run to return
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run exited with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit after stop")
	}

	// 3. Status should report stopped
	stAfter := readDaemonStatus(home)
	if stAfter.Running {
		t.Fatalf("readDaemonStatus after stop reported running, want stopped")
	}
}

func TestWindowsDaemonStopDoesNotKillAnUnrelatedStalePID(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	victim := exec.Command("ping.exe", "-n", "60", "127.0.0.1")
	if err := victim.Start(); err != nil {
		t.Skipf("cannot start a victim process: %v", err)
	}
	victimPID := victim.Process.Pid
	t.Cleanup(func() {
		if processRunning(victimPID) {
			_ = victim.Process.Kill()
		}
		_ = victim.Wait()
	})
	if err := os.WriteFile(daemon.PidPath(home), []byte(strconv.Itoa(victimPID)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped, err := stopDaemonPlatform(ctx, home)
	if err != nil {
		t.Fatalf("stopDaemonPlatform: %v", err)
	}
	if stopped {
		t.Fatal("stopDaemonPlatform claimed to stop a daemon for an unrelated stale PID")
	}
	if !processRunning(victimPID) {
		t.Fatal("stopDaemonPlatform killed an unrelated process named by a stale pid file")
	}
	if _, err := os.Stat(daemon.PidPath(home)); !os.IsNotExist(err) {
		t.Fatalf("stale pid file was not removed: %v", err)
	}
}

func freeTestTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
