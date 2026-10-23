// Command devstack is the user-facing CLI for dev-web-stack.
package main

import (
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
	"github.com/AramZahedi/dev-web-stack/internal/version"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "devstack:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "devstack",
		Short:         "Local web development stack",
		Long:          "devstack manages local .test sites, their runtimes, and trusted TLS.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	root.AddCommand(newServeStaticCmd())
	root.AddCommand(newDaemonCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the devstack version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the devstack background daemon",
	}
	cmd.AddCommand(newDaemonRunCmd(), newDaemonStatusCmd(), newDaemonStopCmd())
	return cmd
}

func newDaemonRunCmd() *cobra.Command {
	var (
		home         string
		helperSocket string
		hostsPath    string
		httpPort     int
		httpsPort    int
		uiPort       int
		logLevel     string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the devstack daemon in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return daemon.Run(ctx, daemon.Options{
				Home:         home,
				HelperSocket: helperSocket,
				HostsPath:    hostsPath,
				HTTPPort:     httpPort,
				HTTPSPort:    httpsPort,
				UIPort:       uiPort,
				LogLevel:     logLevel,
			})
		},
	}
	cmd.Flags().StringVar(&home, "home", "", "devstack home directory")
	cmd.Flags().StringVar(&helperSocket, "helper-socket", "", "privileged helper Unix socket")
	cmd.Flags().StringVar(&hostsPath, "hosts", "", "hosts file inspected by doctor")
	cmd.Flags().IntVar(&httpPort, "http-port", 0, "proxy HTTP port (0 keeps config.yaml)")
	cmd.Flags().IntVar(&httpsPort, "https-port", 0, "proxy HTTPS port (0 keeps config.yaml)")
	cmd.Flags().IntVar(&uiPort, "ui-port", 0, "dashboard/API port (0 keeps config.yaml)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "debug, info, warn, or error")
	_ = cmd.Flags().MarkHidden("hosts")
	return cmd
}

func newDaemonStatusCmd() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report whether the devstack daemon is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveHome(home)
			if err != nil {
				return err
			}
			socket := daemon.SocketPath(resolved)
			conn, err := net.DialTimeout("unix", socket, time.Second)
			if err != nil {
				_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), "stopped")
				return writeErr
			}
			_ = conn.Close()

			pid, err := readPID(daemon.PidPath(resolved))
			if err != nil {
				_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), "running")
				return writeErr
			}
			_, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "running (pid %d)\n", pid)
			return writeErr
		},
	}
	cmd.Flags().StringVar(&home, "home", "", "devstack home directory")
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	var home string
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the devstack daemon via its pid file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveHome(home)
			if err != nil {
				return err
			}
			pid, err := readPID(daemon.PidPath(resolved))
			if errors.Is(err, os.ErrNotExist) {
				_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), "not running")
				return writeErr
			}
			if err != nil {
				return err
			}
			if err := signalProcess(pid, syscall.SIGTERM); err != nil {
				if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
					_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), "not running")
					return writeErr
				}
				return fmt.Errorf("stop devstack daemon: %w", err)
			}

			deadline := time.Now().Add(10 * time.Second)
			for processRunning(pid) {
				if time.Now().After(deadline) {
					return fmt.Errorf("devstack daemon (pid %d) did not stop within 10s", pid)
				}
				time.Sleep(50 * time.Millisecond)
			}
			_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), "stopped")
			return writeErr
		},
	}
	cmd.Flags().StringVar(&home, "home", "", "devstack home directory")
	return cmd
}

// resolveHome follows the same precedence as config.Load but creates nothing.
func resolveHome(home string) (string, error) {
	if home == "" {
		home = os.Getenv("DEVSTACK_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		home = filepath.Join(userHome, ".devstack")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve devstack home %q: %w", home, err)
	}
	return abs, nil
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
