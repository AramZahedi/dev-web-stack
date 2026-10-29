//go:build windows

package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

func stopDaemonPlatform(ctx context.Context, home string) (bool, error) {
	return daemonStopWindows(ctx, home)
}

func daemonStopWindows(ctx context.Context, home string) (bool, error) {
	status := readDaemonStatus(home)
	pid, pidErr := readPID(daemon.PidPath(home))
	if !status.Running && pidErr != nil {
		return false, nil
	}
	if pid == 1 {
		return false, daemonStopRefused(home, "refusing to signal pid 1")
	}

	// A pid file is not proof of identity on Windows: it can be stale and the
	// PID reused. Only a process whose image is devstack.exe may be force-killed.
	pidVerified := pid > 0 && processIsDevstack(pid)
	if !status.Running && !pidVerified {
		_ = os.Remove(daemon.PidPath(home))
		return false, nil
	}

	endpoint := daemon.Endpoint(home)
	transport := &http.Transport{
		DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			return ipc.Dial(dialCtx, endpoint, 2*time.Second)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://devstack/api/v1/daemon/stop", nil)
	if err == nil {
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	deadline := time.Now().Add(daemonStopTimeout)
	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return true, wrapError(ctx.Err(), "DAEMON_STOP_FAILED", "The daemon is still stopping; check `devstack daemon status`.")
			default:
			}
		}
		st := readDaemonStatus(home)
		_, err := os.Stat(daemon.PidPath(home))
		pidFileExists := err == nil
		stillRunning := st.Running
		if pidVerified && pidFileExists && processRunning(pid) {
			stillRunning = true
		}
		if !stillRunning {
			_ = os.Remove(daemon.PidPath(home))
			return true, nil
		}
		if time.Now().After(deadline) {
			// Force-kill only a PID that both looked like devstack.exe and had
			// this home's live control pipe. A stale pid file can name another
			// install's devstack.exe, which must never be killed.
			if status.Running && pidVerified && processIsDevstack(pid) {
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Kill()
				}
			}
			return true, &Error{
				Code:        "DAEMON_STOP_TIMEOUT",
				Message:     fmt.Sprintf("devstack daemon (pid %d) did not stop within %s", pid, daemonStopTimeout),
				Remediation: "Inspect the daemon log, then stop the process manually if needed.",
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}
