package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func (a *App) newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage installed runtime builds",
	}
	cmd.AddCommand(a.newRuntimeListCmd(), a.newRuntimeInstallCmd(), a.newRuntimeRemoveCmd(), a.newRuntimeMigrateCmd())
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
			message := fmt.Sprintf("Install runtime %s?", id)
			available, err := c.AvailableRuntimes(ctx)
			if err != nil {
				return err
			}
			if entry, ok := findCatalogEntry(available, id); ok {
				message = fmt.Sprintf("Install runtime %s? %s.", id, describeCatalogEntry(entry))
			}
			if !a.assumeYes {
				ok, err := a.confirm(cmd, message)
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

// newRuntimeMigrateCmd moves every PHP site on one line to a target build.
func (a *App) newRuntimeMigrateCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "migrate <from> --to <to>",
		Short: "Migrate every PHP site from one version to another",
		Long:  "Migrate every PHP site on a line or build. <from> and --to accept a line (8.4) or a build id (php@8.4.4-r1); a build id selects that exact target build.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromLine, _, err := parsePHPMigrateTarget(args[0], true)
			if err != nil {
				return err
			}
			toLine, toBuild, err := parsePHPMigrateTarget(target, false)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			allSites, err := c.ListSites(ctx)
			if err != nil {
				return err
			}
			affected := make([]model.Site, 0, len(allSites))
			for _, site := range allSites {
				if site.Kind == model.SitePHP && strings.TrimSpace(site.PHPVersion) == fromLine {
					affected = append(affected, site)
				}
			}
			sort.Slice(affected, func(i, j int) bool { return affected[i].Name < affected[j].Name })

			if len(affected) == 0 {
				if a.jsonOutput {
					return a.writeJSON(cmd, []sites.PHPMigrationResult{})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "No PHP sites use PHP %s.\n", fromLine)
				return nil
			}

			out := cmd.OutOrStdout()
			if !a.jsonOutput {
				fmt.Fprintf(out, "The following PHP sites use PHP %s:\n", fromLine)
				rows := make([][]string, 0, len(affected))
				for _, site := range affected {
					rows = append(rows, []string{site.Name, defaultString(site.PHPBuild, "-")})
				}
				if err := writeTable(cmd, []string{"SITE", "BUILD"}, rows); err != nil {
					return err
				}
			}

			targetLabel := "PHP " + toLine
			targetArg := toLine
			if toBuild != "" {
				targetLabel = toBuild
				targetArg = toBuild
			}
			if !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf("Migrate %d PHP site(s) from PHP %s to %s? Each site is validated and swapped independently.", len(affected), fromLine, targetLabel))
				if err != nil {
					return err
				}
				if !ok {
					if a.jsonOutput {
						return a.writeJSON(cmd, []sites.PHPMigrationResult{})
					}
					fmt.Fprintln(out, "PHP migration cancelled.")
					return nil
				}
			}

			results, err := c.MigratePHP(ctx, sites.PHPMigrationRequest{
				FromVersion: fromLine,
				ToVersion:   toLine,
				ToBuild:     toBuild,
			})
			if err != nil {
				return err
			}
			if results == nil {
				results = []sites.PHPMigrationResult{}
			}
			if a.jsonOutput {
				if err := a.writeJSON(cmd, results); err != nil {
					return err
				}
			} else {
				rows := make([][]string, 0, len(results))
				for _, result := range results {
					status := "failed"
					if result.OK {
						status = "ok"
					}
					rows = append(rows, []string{result.Site, status, result.Error})
				}
				if err := writeTable(cmd, []string{"SITE", "RESULT", "ERROR"}, rows); err != nil {
					return err
				}
			}

			failed := 0
			for _, result := range results {
				if !result.OK {
					failed++
				}
			}
			if failed != 0 {
				return &Error{
					Code:        "PHP_MIGRATION_FAILED",
					Message:     fmt.Sprintf("%d of %d PHP sites failed to migrate", failed, len(results)),
					Remediation: "Inspect the per-site errors, fix them, and rerun `devstack runtime migrate " + fromLine + " --to " + targetArg + "`.",
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "to", "", "target PHP line or build id")
	return cmd
}

// parsePHPMigrateTarget normalizes a line or build id. For a source target the
// build id only narrows the line; for a destination it also selects the exact
// build.
func parsePHPMigrateTarget(value string, source bool) (line, build string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", &Error{Code: "INVALID_ARGUMENT", Message: "a PHP line or build id is required", Remediation: "Pass a line such as 8.4 or a build id such as php@8.4.4-r1."}
	}
	if strings.Contains(value, "@") {
		id, parseErr := catalog.ParseID(value)
		if parseErr != nil || id.Kind != "php" {
			return "", "", &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP build id %q", value), Remediation: "Pass a build id such as php@8.4.4-r1."}
		}
		line := catalog.LineOf(id.Version)
		if source {
			return line, "", nil
		}
		return line, value, nil
	}
	line, ok := normalizePHPLine(value)
	if !ok {
		return "", "", &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP version %q", value), Remediation: "Pass a PHP major.minor line such as 8.4, or a build id such as php@8.4.4-r1."}
	}
	return line, "", nil
}
