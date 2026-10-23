package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/daemon"
)

const daemonStopTimeout = 10 * time.Second

// readProcCmdline is a test seam for the daemon-stop ownership check.
var readProcCmdline = func(pid int) ([]byte, error) {
	return os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
}

// daemonStatus is the JSON shape reported by `daemon status`.
type daemonStatus struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Socket  string `json:"socket"`
}

func (a *App) newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the devstack background daemon",
	}
	cmd.AddCommand(a.newDaemonRunCmd(), a.newDaemonStatusCmd(), a.newDaemonStopCmd())
	return cmd
}

func (a *App) newDaemonRunCmd() *cobra.Command {
	var (
		hostsPath string
		httpPort  int
		httpsPort int
		uiPort    int
		logLevel  string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the devstack daemon in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(a.home)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runDaemon(ctx, daemon.Options{
				Home:         home,
				HelperSocket: a.helperSocketPath(),
				HostsPath:    hostsPath,
				HTTPPort:     httpPort,
				HTTPSPort:    httpsPort,
				UIPort:       uiPort,
				LogLevel:     logLevel,
			})
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&hostsPath, "hosts", "", "hosts file inspected by doctor")
	flags.IntVar(&httpPort, "http-port", 0, "proxy HTTP port (0 keeps config.yaml)")
	flags.IntVar(&httpsPort, "https-port", 0, "proxy HTTPS port (0 keeps config.yaml)")
	flags.IntVar(&uiPort, "ui-port", 0, "dashboard/API port (0 keeps config.yaml)")
	flags.StringVar(&logLevel, "log-level", "info", "debug, info, warn, or error")
	_ = flags.MarkHidden("hosts")
	return cmd
}

func (a *App) newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether the devstack daemon is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(a.home)
			if err != nil {
				return err
			}
			status := readDaemonStatus(home)
			if a.jsonOutput {
				return a.writeJSON(cmd, status)
			}
			switch {
			case status.Running && status.PID > 0:
				fmt.Fprintf(cmd.OutOrStdout(), "running (pid %d)\n", status.PID)
			case status.Running:
				fmt.Fprintln(cmd.OutOrStdout(), "running")
			default:
				fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			}
			return nil
		},
	}
}

func (a *App) newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the devstack daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(a.home)
			if err != nil {
				return err
			}
			stopped, err := stopDaemon(cmd.Context(), home)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Stopped bool `json:"stopped"`
				}{Stopped: stopped})
			}
			if stopped {
				fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "not running")
			}
			return nil
		},
	}
}

func readDaemonStatus(home string) daemonStatus {
	socket := daemon.SocketPath(home)
	status := daemonStatus{Socket: socket}
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return status
	}
	_ = conn.Close()
	status.Running = true
	if pid, err := readPID(daemon.PidPath(home)); err == nil {
		status.PID = pid
	}
	return status
}

// daemonStop sends SIGTERM to the pid in home and waits for the process to
// exit. The pid is verified as a devstack process first so a stale pid file can
// never signal an unrelated process. A missing or already-dead process reports
// false without an error.
func daemonStop(ctx context.Context, home string) (bool, error) {
	pid, err := readPID(daemon.PidPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, wrapError(err, "DAEMON_STOP_FAILED", fmt.Sprintf("Inspect %s.", daemon.PidPath(home)))
	}
	if pid == 1 {
		return false, daemonStopRefused(home, "refusing to signal pid 1")
	}
	cmdline, err := readProcCmdline(pid)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, wrapError(fmt.Errorf("inspect pid %d: %w", pid, err), "DAEMON_STOP_FAILED", fmt.Sprintf("Inspect %s.", daemon.PidPath(home)))
	}
	if len(cmdline) == 0 {
		return false, nil
	}
	if !isDevstackCmdline(cmdline) {
		return false, daemonStopRefused(home, fmt.Sprintf("pid %d does not belong to devstack", pid))
	}
	if err := signalProcess(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, wrapError(fmt.Errorf("stop devstack daemon: %w", err), "DAEMON_STOP_FAILED", "Stop the process manually and rerun.")
	}

	deadline := time.Now().Add(daemonStopTimeout)
	for processRunning(pid) {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return true, wrapError(ctx.Err(), "DAEMON_STOP_FAILED", "The daemon is still stopping; check `devstack daemon status`.")
			default:
			}
		}
		if time.Now().After(deadline) {
			return true, &Error{
				Code:        "DAEMON_STOP_TIMEOUT",
				Message:     fmt.Sprintf("devstack daemon (pid %d) did not stop within %s", pid, daemonStopTimeout),
				Remediation: "Inspect the daemon log, then stop the process manually if needed.",
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true, nil
}

func daemonStopRefused(home, message string) error {
	return &Error{
		Code:        "DAEMON_STOP_REFUSED",
		Message:     message,
		Remediation: fmt.Sprintf("Remove the stale pid file %s only after confirming no devstack daemon is running.", daemon.PidPath(home)),
	}
}

// isDevstackCmdline reports whether argv[0] is the devstack or devstack-helper
// binary. /proc/<pid>/cmdline separates arguments with NUL bytes.
func isDevstackCmdline(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	argv0 := data
	if index := bytes.IndexByte(data, 0); index >= 0 {
		argv0 = data[:index]
	}
	base := filepath.Base(strings.TrimSpace(string(argv0)))
	return base == "devstack" || base == "devstack-helper"
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file %s", path)
	}
	return pid, nil
}

func signalProcess(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

// processRunning treats a zombie as stopped; a detached daemon may remain a
// zombie until its new parent reaps it.
func processRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return !errors.Is(err, syscall.ESRCH)
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	return !(end >= 0 && end+2 < len(text) && text[end+2] == 'Z')
}
