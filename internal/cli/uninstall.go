package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// uninstallResult is the JSON report printed by `devstack uninstall --json`.
type uninstallResult struct {
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

	stopped, err := stopDaemon(ctx, home)
	if err != nil {
		return err
	}

	socket := a.helperSocketPath()
	helper, err := dialHelper(socket)
	if err != nil {
		return wrapError(fmt.Errorf("connect to devstack-helper at %s: %w", socket, err), "HELPER_UNAVAILABLE", "Ensure devstack-helper is running, or remove its unit manually using the printed commands.")
	}
	defer func() { _ = helper.Close() }()
	uninstallErr := helperCallError(helper.Uninstall(ctx), socket)

	result := uninstallResult{
		Uninstalled:   uninstallErr == nil,
		DaemonStopped: stopped,
		Command:       elevatedRemovalCommand,
		Checklist: []string{
			"Run the elevated command above to remove the devstack-helper unit and system binaries.",
			"Remove the per-user devstack binary from your PATH if the installer added one.",
			"Review ~/.devstack and remove it manually only if you no longer need configuration or logs.",
			"Project files were left untouched; no site directories were removed.",
		},
		ProjectFilesRemoved: false,
	}

	if a.jsonOutput {
		if err := a.writeJSON(cmd, result); err != nil {
			return err
		}
		return uninstallErr
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Uninstalled devstack integration.")
	if stopped {
		fmt.Fprintln(out, "Daemon: stopped")
	} else {
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
	return uninstallErr
}

const elevatedRemovalCommand = "sudo systemctl disable --now devstack-helper && sudo rm -f /etc/systemd/system/devstack-helper.service && sudo rm -rf /usr/local/lib/devstack"
