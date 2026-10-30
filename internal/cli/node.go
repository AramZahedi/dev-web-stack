package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// nodeDefaultClient is the Node CLI default surface. The shared Client
// interface is owned by the integration lane, so this command asserts the
// methods it needs instead of widening it here.
type nodeDefaultClient interface {
	NodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error)
	SetNodeDefault(ctx context.Context, value string, force bool) (tooling.NodeDefaultStatus, error)
	ClearNodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error)
}

// newNodeCmd manages Node runtimes and the machine-wide Node CLI default.
func (a *App) newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage Node runtimes and the global Node CLI default",
	}
	cmd.AddCommand(a.newNodeDefaultCmd())
	return cmd
}

// newNodeDefaultCmd manages the machine-wide Node CLI default. With no
// arguments it reports status, matching the read-only view users expect from
// `devstack node default`.
func (a *App) newNodeDefaultCmd() *cobra.Command {
	var (
		status bool
		clear  bool
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "default [version|build]",
		Short: "Show or set the machine-wide Node CLI default",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status && clear {
				return &Error{Code: "INVALID_FLAGS", Message: "--status and --clear cannot be used together", Remediation: "Choose one action."}
			}
			if status && len(args) != 0 {
				return &Error{Code: "INVALID_FLAGS", Message: "--status does not take a value", Remediation: "Run `devstack node default --status`."}
			}
			if clear && len(args) != 0 {
				return &Error{Code: "INVALID_FLAGS", Message: "--clear does not take a value", Remediation: "Run `devstack node default --clear`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			service, ok := c.(nodeDefaultClient)
			if !ok {
				return &Error{Code: "SERVICE_UNAVAILABLE", Message: "the daemon does not support the Node default command", Remediation: "Update devstack and restart the daemon, then retry."}
			}
			var result tooling.NodeDefaultStatus
			switch {
			case clear:
				result, err = service.ClearNodeDefault(ctx)
			case status || len(args) == 0:
				result, err = service.NodeDefault(ctx)
			default:
				result, err = service.SetNodeDefault(ctx, args[0], force)
			}
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, result)
			}
			return writeNodeDefaultTable(cmd, result)
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "show the current default")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the machine-wide default and fall back to the newest installed build")
	cmd.Flags().BoolVar(&force, "force", false, "back up and replace an existing system node wrapper")
	return cmd
}

// writeNodeDefaultTable renders the resolved build, configured value, shim
// state, and selection source.
func writeNodeDefaultTable(cmd *cobra.Command, status tooling.NodeDefaultStatus) error {
	out := cmd.OutOrStdout()
	switch {
	case status.Build == nil && status.Configured != "":
		fmt.Fprintf(out, "The configured Node default %s is not installed; the managed Node CLI shim is not available.\n", status.Configured)
	case status.Build == nil:
		fmt.Fprintln(out, "No Node build is installed; the managed Node CLI shim is not available.")
	}
	buildID, version := "-", "-"
	if status.Build != nil {
		buildID = defaultString(status.Build.ID, "-")
		version = defaultString(status.Build.Version, "-")
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"Configured", defaultString(status.Configured, "-")},
		{"Build", buildID},
		{"Version", version},
		{"Shim", defaultString(status.ShimDir, "-")},
		{"Installed", fmt.Sprintf("%t", status.ShimInstalled)},
		{"Source", defaultString(status.Source, "-")},
	})
}
