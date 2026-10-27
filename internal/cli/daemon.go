package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

const daemonStopTimeout = 10 * time.Second

// readProcCmdline and readProcStartTicks are test seams for the daemon-stop
// ownership checks.
var (
	readProcCmdline = func(pid int) ([]byte, error) {
		return os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	}
	readProcStartTicks = procStartTicks
)

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
	cmd.AddCommand(a.newDaemonRunCmd(), a.newDaemonStatusCmd(), a.newDaemonStopCmd(), a.newDaemonRestartCmd())
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

func restoreConfigFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "config-restore-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (a *App) newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop and start the devstack daemon using config.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome(a.home)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			// Validate the durable source before stopping the running daemon;
			// an invalid config must not strand the user with no dashboard.
			if _, err := config.Load(home); err != nil {
				return wrapError(err, "CONFIG_INVALID", "Fix config.yaml, then rerun `devstack daemon restart`.")
			}
			configPath := filepath.Join(home, "config.yaml")
			previous, err := os.ReadFile(configPath)
			if err != nil {
				return wrapError(err, "CONFIG_INVALID", "Read config.yaml before restart.")
			}
			// Do not carry one-off run flags across a restart: config.yaml is
			// the durable source for listener ports.
			if _, err := stopDaemon(ctx, home); err != nil {
				return err
			}
			opts := daemon.Options{Home: home, HelperSocket: a.helperSocketPath()}
			if err := ensureRunning(ctx, opts); err != nil {
				// A saved port can be taken after PATCH validated it. Restore the
				// previous config and bring the old daemon back rather than leaving
				// the user with no dashboard/API.
				restoreErr := restoreConfigFile(configPath, previous)
				if restoreErr == nil {
					restoreErr = ensureRunning(ctx, opts)
				}
				if restoreErr != nil {
					return wrapError(errors.Join(err, fmt.Errorf("restore previous daemon: %w", restoreErr)),
						"DAEMON_UNAVAILABLE", "The new configuration did not start and the previous daemon could not be restored; inspect the daemon log.")
				}
				return wrapError(err, "DAEMON_UNAVAILABLE",
					"New ports did not start; the previous config was restored and the daemon was restarted.")
			}
			dashboard := ""
			if cfg, err := config.Load(home); err == nil {
				dashboard = fmt.Sprintf("http://127.0.0.1:%d", cfg.UIPort)
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Restarted    bool   `json:"restarted"`
					DashboardURL string `json:"dashboard_url,omitempty"`
				}{Restarted: true, DashboardURL: dashboard})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "restarted")
			if dashboard != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Dashboard: %s\n", dashboard)
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
	endpoint := daemon.Endpoint(home)
	status := daemonStatus{Socket: endpoint.Address}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, endpoint, time.Second)
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
	if expected, err := readStartTicks(daemon.StartTicksPath(home)); err == nil {
		actual, err := readProcStartTicks(pid)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			// The recorded process is gone; treat the stale files as stopped.
			_ = os.Remove(daemon.PidPath(home))
			_ = os.Remove(daemon.StartTicksPath(home))
			return false, nil
		}
		if err != nil {
			return false, wrapError(fmt.Errorf("inspect start time for pid %d: %w", pid, err), "DAEMON_STOP_FAILED", fmt.Sprintf("Inspect %s.", daemon.PidPath(home)))
		}
		if actual != expected {
			return false, daemonStopRefused(home, fmt.Sprintf("pid %d was reused by another process", pid))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, wrapError(err, "DAEMON_STOP_FAILED", fmt.Sprintf("Inspect %s.", daemon.StartTicksPath(home)))
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

func readStartTicks(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	ticks, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid start-time file %s", path)
	}
	return ticks, nil
}

func procStartTicks(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return 0, fmt.Errorf("truncated /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(data[closeParen+2:]))
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return 0, fmt.Errorf("truncated /proc/%d/stat", pid)
	}
	return strconv.ParseUint(fields[startTimeIndex], 10, 64)
}

func signalProcess(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}
