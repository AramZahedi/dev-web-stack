package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

// uninstallResult is the JSON report printed by `devstack uninstall --json`.
type uninstallResult struct {
	Status              string   `json:"status"`
	Uninstalled         bool     `json:"uninstalled"`
	DaemonStopped       bool     `json:"daemon_stopped"`
	Command             string   `json:"command"`
	Checklist           []string `json:"checklist"`
	ProjectFilesRemoved bool     `json:"project_files_removed"`
}

func (a *App) newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the devstack integration (requires --yes)",
		Long:  "uninstall stops the daemon and asks the helper to remove trust, hosts entries, and forwarders. It never removes project files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runUninstall(cmd)
		},
	}
}

func (a *App) runUninstall(cmd *cobra.Command) error {
	ctx := cmd.Context()
	home, err := resolveHome(a.home)
	if err != nil {
		return err
	}
	if !a.assumeYes {
		return &Error{
			Code:        "CONFIRMATION_REQUIRED",
			Message:     "uninstall removes the devstack integration and requires confirmation",
			Remediation: "Re-run `devstack uninstall --yes`. Project files are never removed.",
		}
	}

	stopped, stopErr := stopDaemon(ctx, home)
	userUnitRemoved := false
	if stopErr == nil {
		userUnitRemoved = removeDefaultUserDaemonUnit(ctx, home)
	}
	result := uninstallResult{
		Status:        "complete",
		Uninstalled:   true,
		DaemonStopped: stopped,
		Command:       elevatedRemovalCommand(home),
		Checklist: []string{
			userDaemonRemovalChecklistItem(userUnitRemoved),
			"Run the elevated command above to remove the devstack-helper unit and system binaries.",
			"Remove the per-user devstack binary from your PATH if the installer added one.",
			fmt.Sprintf("Review %s and remove it manually only if you no longer need configuration or logs.", home),
			"Project files were left untouched; no site directories were removed.",
		},
		ProjectFilesRemoved: false,
	}

	var uninstallErr error
	if stopErr != nil {
		// Do not remove helper-owned integration while the daemon may still be
		// running. The instructions below still tell the user how to finish.
		uninstallErr = stopErr
	} else {
		socket := a.helperSocketPath()
		helper, err := dialHelper(socket)
		if err != nil {
			uninstallErr = wrapError(
				fmt.Errorf("connect to devstack-helper at %s: %w", socket, err),
				"HELPER_UNAVAILABLE",
				"Ensure devstack-helper is running, then rerun `devstack uninstall --yes`.",
			)
		} else {
			defer func() { _ = helper.Close() }()
			uninstallErr = helperCallError(helper.Uninstall(ctx), socket)
		}
	}
	if uninstallErr != nil {
		result.Status = "incomplete"
		result.Uninstalled = false
	}

	if a.jsonOutput {
		if err := a.writeJSON(cmd, result); err != nil {
			return err
		}
		return uninstallErr
	}
	out := cmd.OutOrStdout()
	if uninstallErr == nil {
		fmt.Fprintln(out, "Uninstalled devstack integration.")
	} else {
		fmt.Fprintln(out, "Uninstall incomplete.")
	}
	switch {
	case stopErr != nil:
		fmt.Fprintf(out, "Daemon: stop failed: %v\n", stopErr)
	case stopped:
		fmt.Fprintln(out, "Daemon: stopped")
	default:
		fmt.Fprintln(out, "Daemon: was not running")
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "To remove the helper unit and system binaries, run:")
	fmt.Fprintf(out, "  %s\n", result.Command)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Checklist:")
	for _, item := range result.Checklist {
		fmt.Fprintf(out, "  [ ] %s\n", item)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Project files were left untouched.")
	if uninstallErr != nil {
		fmt.Fprintf(out, "Uninstall did not complete: %v\n", uninstallErr)
	}
	return uninstallErr
}

func userDaemonRemovalChecklistItem(removed bool) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Per-user daemon scheduled task removed: %t. If false, run `schtasks /Delete /TN devstack-daemon /F`.", removed)
	}
	return fmt.Sprintf("Per-user daemon unit removed: %t. If false, run `systemctl --user disable --now devstack-daemon.service` and remove the unit file manually.", removed)
}

// removeDefaultUserDaemonUnit removes the unit installed by the default
// installer. A custom --home may share a machine with a differently installed
// unit, so it is left to the printed checklist.
func removeDefaultUserDaemonUnit(ctx context.Context, home string) bool {
	defaultHome, err := resolveHome("")
	if err != nil || filepath.Clean(home) != filepath.Clean(defaultHome) {
		return false
	}
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(ctx, "schtasks", "/Delete", "/TN", "devstack-daemon", "/F")
		return cmd.Run() == nil
	}
	_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", "devstack-daemon.service").Run()
	configDir, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	unit := filepath.Join(configDir, "systemd", "user", "devstack-daemon.service")
	if err := os.Remove(unit); err != nil && !os.IsNotExist(err) {
		return false
	}
	_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
	return true
}

func elevatedRemovalCommand(home string) string {
	if runtime.GOOS == "windows" {
		script := filepath.Join(home, "uninstall.ps1")
		return fmt.Sprintf(`powershell.exe -ExecutionPolicy Bypass -File "%s"`, script)
	}
	return "sudo systemctl disable --now devstack-helper && sudo rm -f /etc/systemd/system/devstack-helper.service && sudo rm -rf /usr/local/lib/devstack /var/lib/devstack-helper /etc/devstack && sudo systemctl daemon-reload"
}
