package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/client"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// nodeReleaseClient is the discovered Node release surface the runtime commands
// need. The shared Client interface is owned by the integration lane, so these
// commands assert the methods they need instead of widening it here.
type nodeReleaseClient interface {
	NodeReleases(ctx context.Context, major string, all, offline bool) (nodereleases.Snapshot, error)
	NodeRelease(ctx context.Context, version string) (nodereleases.Preflight, error)
	RefreshNodeReleases(ctx context.Context) (nodereleases.Snapshot, error)
}

func (a *App) newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage installed runtime builds",
	}
	cmd.AddCommand(a.newRuntimeListCmd(), a.newRuntimeAvailableCmd(), a.newRuntimeInstallCmd(), a.newRuntimeRemoveCmd(), a.newRuntimeMigrateCmd())
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

// newRuntimeAvailableCmd lists discovered Node releases grouped by major and
// the available PHP builds. Node rows default to the newest patch of each
// major; --all expands every stable release and --major filters one line.
func (a *App) newRuntimeAvailableCmd() *cobra.Command {
	var (
		kind     string
		majorArg string
		all      bool
		refresh  bool
		offline  bool
	)
	cmd := &cobra.Command{
		Use:   "available",
		Short: "List available runtime releases",
		Long:  "List available Node releases grouped by major and the available PHP builds. Node rows show the newest patch of each major unless --all is set.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kindFilter := strings.ToLower(strings.TrimSpace(kind))
			switch kindFilter {
			case "":
				kindFilter = "all"
			case "all", "node", "php":
			default:
				return &Error{
					Code:        "INVALID_ARGUMENT",
					Message:     fmt.Sprintf("unknown runtime kind %q", kind),
					Remediation: "Use --kind node, --kind php, or omit --kind to list both.",
				}
			}
			major := 0
			if value := strings.TrimSpace(majorArg); value != "" {
				parsed, err := parseRuntimeMajor(value)
				if err != nil {
					return err
				}
				major = parsed
			}
			if kindFilter == "php" {
				switch {
				case major != 0:
					return &Error{Code: "INVALID_ARGUMENT", Message: "--major only applies to Node releases", Remediation: "Use --kind node with --major, or omit --major."}
				case all:
					return &Error{Code: "INVALID_ARGUMENT", Message: "--all only applies to Node releases", Remediation: "Use --kind node with --all, or omit --all."}
				case refresh:
					return &Error{Code: "INVALID_ARGUMENT", Message: "--refresh only applies to Node releases", Remediation: "Use --kind node with --refresh, or omit --refresh."}
				case offline:
					return &Error{Code: "INVALID_ARGUMENT", Message: "--offline only applies to Node releases", Remediation: "Use --kind node with --offline, or omit --offline."}
				}
			}
			if refresh && offline {
				return &Error{Code: "INVALID_FLAGS", Message: "--refresh and --offline cannot be used together", Remediation: "Use --refresh to fetch fresh releases or --offline to stay on the cache, not both."}
			}

			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			view := runtimeAvailableView{}
			if kindFilter != "php" {
				service, ok := c.(nodeReleaseClient)
				if !ok {
					return &Error{
						Code:        "SERVICE_UNAVAILABLE",
						Message:     "the daemon does not support Node release discovery",
						Remediation: "Update devstack and restart the daemon, then retry.",
					}
				}
				snapshot, err := loadRuntimeNodeSnapshot(ctx, service, major, all, offline, refresh)
				if err != nil {
					return err
				}
				view.node = &snapshot
			}
			if kindFilter != "node" {
				available, err := c.AvailableRuntimes(ctx)
				if err != nil {
					return err
				}
				view.php = filterPHPEntries(available)
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, view.jsonValue())
			}
			return view.write(cmd, all)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "all", "runtime kind to list: node, php, or all")
	cmd.Flags().StringVar(&majorArg, "major", "", "limit Node releases to one major version")
	cmd.Flags().BoolVar(&all, "all", false, "show every stable Node release in each major instead of only the newest patch")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "force a Node release index refresh")
	cmd.Flags().BoolVar(&offline, "offline", false, "use the cached or embedded Node release index only")
	return cmd
}

// parseRuntimeMajor accepts one positive decimal major version.
func parseRuntimeMajor(value string) (int, error) {
	invalid := func() error {
		return &Error{
			Code:        "INVALID_ARGUMENT",
			Message:     fmt.Sprintf("invalid Node major %q", value),
			Remediation: "Pass a positive integer such as 22.",
		}
	}
	if !isRuntimeDigits(value) {
		return 0, invalid()
	}
	major, err := strconv.Atoi(value)
	if err != nil || major < 1 {
		return 0, invalid()
	}
	return major, nil
}

// loadRuntimeNodeSnapshot applies the discovery flags. A refresh only returns
// the default newest-per-major view, so filtered views read the fresh cache.
func loadRuntimeNodeSnapshot(ctx context.Context, service nodeReleaseClient, major int, all, offline, refresh bool) (nodereleases.Snapshot, error) {
	majorArg := ""
	if major > 0 {
		majorArg = strconv.Itoa(major)
	}
	if refresh {
		snapshot, err := service.RefreshNodeReleases(ctx)
		if err != nil {
			return nodereleases.Snapshot{}, err
		}
		if majorArg == "" && !all {
			return snapshot, nil
		}
		return service.NodeReleases(ctx, majorArg, all, false)
	}
	return service.NodeReleases(ctx, majorArg, all, offline)
}

// runtimeAvailableView holds the independent Node and PHP sections.
type runtimeAvailableView struct {
	node *nodereleases.Snapshot
	php  []catalog.Entry
}

func (v runtimeAvailableView) jsonValue() any {
	php := v.php
	if php == nil {
		php = []catalog.Entry{}
	}
	var node *nodereleases.Snapshot
	if v.node != nil {
		copy := *v.node
		if copy.Majors == nil {
			copy.Majors = []nodereleases.Major{}
		}
		if copy.All == nil {
			copy.All = []nodereleases.Release{}
		}
		node = &copy
	}
	return struct {
		Node *nodereleases.Snapshot `json:"node,omitempty"`
		PHP  []catalog.Entry        `json:"php"`
	}{Node: node, PHP: php}
}

func (v runtimeAvailableView) write(cmd *cobra.Command, all bool) error {
	if v.node != nil {
		if err := writeNodeAvailable(cmd, *v.node, all); err != nil {
			return err
		}
	}
	if v.php != nil && v.node != nil {
		fmt.Fprintln(cmd.OutOrStdout())
	}
	if v.php != nil {
		return writePHPAvailable(cmd, v.php)
	}
	return nil
}

// writeNodeAvailable renders one row per major (newest patch) or every stable
// release when all is set. The daemon already orders majors and releases.
func writeNodeAvailable(cmd *cobra.Command, snapshot nodereleases.Snapshot, all bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Node releases")
	if snapshot.Source != "" {
		source := "source: " + snapshot.Source
		if !snapshot.CachedAt.IsZero() {
			source += ", cached " + snapshot.CachedAt.UTC().Format(time.RFC3339)
		}
		if snapshot.Stale {
			source += ", stale"
		}
		fmt.Fprintln(out, source)
	}
	if snapshot.Warning != "" {
		fmt.Fprintln(out, "Warning: "+snapshot.Warning)
	}

	rows := make([][]string, 0, len(snapshot.Majors))
	for _, major := range snapshot.Majors {
		releases := major.Releases
		if !all || len(releases) == 0 {
			releases = []nodereleases.Release{major.Latest}
		}
		for _, release := range releases {
			if release.Version == "" {
				continue
			}
			security := "no"
			if release.Security {
				security = "yes"
			}
			rows = append(rows, []string{
				strconv.Itoa(major.Major),
				release.Version,
				defaultString(release.NPM, "-"),
				defaultString(release.LTS, "-"),
				security,
			})
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "No Node releases available.")
		return nil
	}
	return writeTable(cmd, []string{"MAJOR", "VERSION", "NPM", "LTS", "SECURITY"}, rows)
}

// writePHPAvailable keeps PHP builds in their own section so Node release rows
// never mix with embedded catalog entries.
func writePHPAvailable(cmd *cobra.Command, entries []catalog.Entry) error {
	fmt.Fprintln(cmd.OutOrStdout(), "PHP runtimes")
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, []string{
			entry.ID,
			defaultString(entry.Version, "-"),
			defaultString(entry.Build, "-"),
		})
	}
	return writeTable(cmd, []string{"AVAILABLE", "VERSION", "BUILD"}, rows)
}

// filterPHPEntries returns the embedded PHP builds in stable id order.
func filterPHPEntries(entries []catalog.Entry) []catalog.Entry {
	php := make([]catalog.Entry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := catalogEntryPHPLine(entry); !ok {
			continue
		}
		php = append(php, entry)
	}
	sort.Slice(php, func(i, j int) bool { return php[i].ID < php[j].ID })
	return php
}

func (a *App) newRuntimeInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <id|selector>",
		Short: "Install a runtime build",
		Long:  "Install an embedded runtime build id or a Node release selector. Node selectors such as node@22, node@22.11, and node@22.11.0 resolve through discovery and are preflighted before installation.",
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

			installID := id
			message := fmt.Sprintf("Install runtime %s?", id)
			selector, exact, isNode, err := parseNodeInstallSelector(id)
			if err != nil {
				return err
			}
			if isNode {
				if service, ok := c.(nodeReleaseClient); ok {
					preflight, preflightErr := service.NodeRelease(ctx, selector)
					switch {
					case preflightErr == nil:
						if err := validateNodePreflightEntry(preflight.Entry); err != nil {
							return err
						}
						installID = preflight.Entry.ID
						message = describeNodeInstall(preflight)
					case exact:
						entry, found, err := findAvailableEntry(ctx, c, id)
						if err != nil {
							return err
						}
						if !found {
							return nodeSelectorUnavailable(selector, preflightErr)
						}
						message = fmt.Sprintf("Install runtime %s? %s.", id, describeCatalogEntry(entry))
					default:
						return nodeSelectorUnavailable(selector, preflightErr)
					}
				} else {
					// Compatibility: exact embedded Node ids keep the catalog path
					// when the daemon predates discovery. Selectors need discovery.
					if !exact {
						return &Error{
							Code:        "SERVICE_UNAVAILABLE",
							Message:     "the daemon does not support Node release discovery",
							Remediation: "Update devstack and restart the daemon, then retry.",
						}
					}
					entry, found, err := findAvailableEntry(ctx, c, id)
					if err != nil {
						return err
					}
					if found {
						message = fmt.Sprintf("Install runtime %s? %s.", id, describeCatalogEntry(entry))
					}
				}
			} else {
				entry, found, err := findAvailableEntry(ctx, c, id)
				if err != nil {
					return err
				}
				if found {
					message = fmt.Sprintf("Install runtime %s? %s.", id, describeCatalogEntry(entry))
				}
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
			build, err := c.InstallRuntime(ctx, installID)
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

// parseNodeInstallSelector recognizes node@<major>, node@<major>.<minor>, and
// node@<major>.<minor>.<patch>. A non-Node id is left to the embedded catalog
// path. Malformed Node selectors are rejected instead of being forwarded.
func parseNodeInstallSelector(id string) (selector string, exact, ok bool, err error) {
	if !strings.HasPrefix(id, "node@") {
		return "", false, false, nil
	}
	raw := strings.TrimPrefix(id, "node@")
	parts := strings.Split(raw, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return "", false, false, invalidNodeSelectorError(id)
	}
	for _, part := range parts {
		if !isRuntimeDigits(part) {
			return "", false, false, invalidNodeSelectorError(id)
		}
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 1 {
		return "", false, false, invalidNodeSelectorError(id)
	}
	return raw, len(parts) == 3, true, nil
}

// isRuntimeDigits accepts non-empty decimal components without signs, spaces,
// or leading zeros.
func isRuntimeDigits(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func invalidNodeSelectorError(id string) error {
	return &Error{
		Code:        "INVALID_ARGUMENT",
		Message:     fmt.Sprintf("invalid Node version selector %q", id),
		Remediation: "Use a major (node@22), major.minor (node@22.11), or exact version (node@22.11.0).",
	}
}

// findAvailableEntry looks up one embedded catalog entry by exact id.
func findAvailableEntry(ctx context.Context, c Client, id string) (catalog.Entry, bool, error) {
	available, err := c.AvailableRuntimes(ctx)
	if err != nil {
		return catalog.Entry{}, false, err
	}
	entry, ok := findCatalogEntry(available, id)
	return entry, ok, nil
}

// validateNodePreflightEntry refuses install metadata that is not a fully
// pinned Node archive. The daemon remains authoritative; this keeps an
// incomplete response from reaching the install call.
func validateNodePreflightEntry(entry catalog.Entry) error {
	if entry.Kind != "node" || entry.Version == "" || entry.ID != "node@"+entry.Version ||
		entry.Build != "linux-x64" || len(entry.SHA256) != 64 || entry.Size <= 0 {
		return &Error{
			Code:        "NODE_PREFLIGHT_INVALID",
			Message:     "the daemon returned incomplete install metadata for the selected Node release",
			Remediation: "Refresh available versions and retry; run `devstack doctor` if the problem persists.",
		}
	}
	return nil
}

// describeNodeInstall renders the pinned version and verified artifact details
// shown before a Node install starts.
func describeNodeInstall(preflight nodereleases.Preflight) string {
	entry := preflight.Entry
	return fmt.Sprintf(
		"Install Node %s (%s)? npm %s, size %s, sha256 %s, build %s.",
		entry.Version,
		entry.ID,
		defaultString(preflight.Release.NPM, "-"),
		formatBytes(entry.Size),
		shortSHA256(entry.SHA256),
		defaultString(entry.Build, "linux-x64"),
	)
}

// shortSHA256 renders the checksum prefix used in install confirmations.
func shortSHA256(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12] + "…"
}

// nodeSelectorUnavailable turns a discovery miss into an actionable selector
// error while preserving transport and preflight failures.
func nodeSelectorUnavailable(selector string, err error) error {
	var apiErr *client.Error
	notFound := errors.Is(err, nodereleases.ErrReleaseNotFound) ||
		(errors.As(err, &apiErr) && apiErr.Code == "NOT_FOUND")
	if !notFound {
		return err
	}
	return &Error{
		Code:        "NODE_VERSION_UNAVAILABLE",
		Message:     fmt.Sprintf("Node %s is not available", selector),
		Remediation: "Run `devstack runtime available --kind node` to list available releases, then retry with a major, major.minor, or exact version.",
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
