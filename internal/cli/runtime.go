package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func (a *App) newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage installed runtime builds",
	}
	cmd.AddCommand(a.newRuntimeListCmd(), a.newRuntimeInstallCmd(), a.newRuntimeRemoveCmd())
	return cmd
}

func (a *App) newRuntimeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed and available runtimes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			installed, err := c.Runtimes(ctx)
			if err != nil {
				return err
			}
			available, err := c.AvailableRuntimes(ctx)
			if err != nil {
				return err
			}
			if installed == nil {
				installed = []model.RuntimeBuild{}
			}
			if available == nil {
				available = []catalog.Entry{}
			}
			sort.Slice(installed, func(i, j int) bool { return installed[i].ID < installed[j].ID })
			sort.Slice(available, func(i, j int) bool { return available[i].ID < available[j].ID })

			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Installed []model.RuntimeBuild `json:"installed"`
					Available []catalog.Entry      `json:"available"`
				}{Installed: installed, Available: available})
			}

			out := cmd.OutOrStdout()
			if len(installed) == 0 {
				fmt.Fprintln(out, "No runtimes installed.")
			} else {
				rows := make([][]string, 0, len(installed))
				for _, build := range installed {
					installedAt := ""
					if !build.InstalledAt.IsZero() {
						installedAt = build.InstalledAt.UTC().Format(time.RFC3339)
					}
					rows = append(rows, []string{
						build.ID,
						defaultString(build.Kind, "-"),
						defaultString(build.Version, "-"),
						defaultString(build.Path, "-"),
						installedAt,
					})
				}
				if err := writeTable(cmd, []string{"ID", "KIND", "VERSION", "PATH", "INSTALLED AT"}, rows); err != nil {
					return err
				}
			}

			fmt.Fprintln(out)
			rows := make([][]string, 0, len(available))
			for _, entry := range available {
				rows = append(rows, []string{
					entry.ID,
					defaultString(entry.Kind, "-"),
					defaultString(entry.Version, "-"),
					defaultString(entry.Build, "-"),
				})
			}
			return writeTable(cmd, []string{"AVAILABLE", "KIND", "VERSION", "BUILD"}, rows)
		},
	}
}

func (a *App) newRuntimeInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <id>",
		Short: "Install a runtime build",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "runtime id is required", Remediation: "Run `devstack runtime list` to see available ids."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf("Install runtime %s?", id))
				if err != nil {
					return err
				}
				if !ok {
					if a.jsonOutput {
						return a.writeJSON(cmd, struct {
							ID        string `json:"id"`
							Installed bool   `json:"installed"`
						}{ID: id, Installed: false})
					}
					fmt.Fprintln(cmd.OutOrStdout(), "Installation cancelled.")
					return nil
				}
			}
			build, err := c.InstallRuntime(ctx, id)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, build)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s (%s).\n", build.ID, build.Version)
			return nil
		},
	}
}

func (a *App) newRuntimeRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove an installed runtime (requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.assumeYes {
				return &Error{
					Code:        "CONFIRMATION_REQUIRED",
					Message:     fmt.Sprintf("removing runtime %q requires confirmation", args[0]),
					Remediation: "Re-run the command with --yes.",
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if err := c.RemoveRuntime(ctx, args[0]); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ID      string `json:"id"`
					Removed bool   `json:"removed"`
				}{ID: args[0], Removed: true})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed runtime %s.\n", args[0])
			return nil
		},
	}
}
