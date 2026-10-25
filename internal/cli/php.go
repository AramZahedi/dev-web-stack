package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

var phpLinePattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

var phpVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

var phpDirectivePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

var phpExtensionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// newPHPCmd runs a site's PHP binary through the daemon. Only arguments after
// "--" are forwarded so devstack flags are never mistaken for PHP flags.
func (a *App) newPHPCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "php --site <name> -- <args...>",
		Short: "Run a site's PHP binary",
		Long:  "Run a site's PHP binary with its generated php.ini. Arguments after -- are forwarded unchanged.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(site)
			if ref == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--site is required", Remediation: "Pass `--site <name>` before `--`, for example `devstack php --site blog -- -v`."}
			}
			if len(args) == 0 {
				return &Error{Code: "INVALID_ARGUMENT", Message: "php requires arguments after --", Remediation: "Pass the PHP arguments after `--`, for example `devstack php --site blog -- -v`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			result, err := c.PHPExec(ctx, ref, args)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ExitCode int    `json:"exit_code"`
					Stdout   string `json:"stdout"`
					Stderr   string `json:"stderr"`
				}{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr})
			}
			if result.Stdout != "" {
				fmt.Fprint(cmd.OutOrStdout(), result.Stdout)
			}
			if result.Stderr != "" {
				fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
			}
			if result.ExitCode != 0 {
				return &Error{
					Code:        "PHP_EXEC_FAILED",
					Message:     fmt.Sprintf("php exited with status %d", result.ExitCode),
					Remediation: "Fix the PHP error reported above, then retry.",
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site name")
	cmd.AddCommand(a.newPHPDefaultCmd())
	return cmd
}

// newSitePHPShowCmd prints or changes a site's PHP version and build.
func (a *App) newSitePHPShowCmd() *cobra.Command {
	var (
		build string
		list  bool
	)
	cmd := &cobra.Command{
		Use:   "php <name> [version]",
		Short: "Show or change a site's PHP version",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			site, err := c.GetSite(ctx, args[0])
			if err != nil {
				return err
			}
			if site.Kind != model.SitePHP {
				return notPHPSite(site.Name)
			}
			if list {
				return a.runSitePHPList(cmd, c, site, args[1:])
			}
			if len(args) == 1 && strings.TrimSpace(build) == "" {
				return a.printSitePHP(cmd, site)
			}

			version := ""
			if len(args) == 2 {
				normalized, ok := normalizePHPLine(args[1])
				if !ok {
					return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP version %q (want a line such as 8.4)", args[1]), Remediation: "Pass a PHP major.minor line, for example `devstack site php blog 8.5`."}
				}
				version = normalized
			}
			buildID := strings.TrimSpace(build)
			if buildID != "" {
				id, err := catalog.ParseID(buildID)
				if err != nil || id.Kind != "php" {
					return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP build id %q", buildID), Remediation: "Pass an installed build id such as `php@8.5.0-r1`."}
				}
				buildLine := catalog.LineOf(id.Version)
				if version == "" {
					version = buildLine
				}
				if version != buildLine {
					return &Error{Code: "INVALID_FLAGS", Message: fmt.Sprintf("build %s belongs to PHP %s, not %s", buildID, buildLine, version), Remediation: "Choose a build on the requested line, or omit the version argument."}
				}
			}
			if version == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "a PHP version or --build is required", Remediation: "Pass a version such as 8.5, or `--build php@8.5.0-r1`."}
			}

			if site.Status == model.StatusRunning && !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf(
					"Switch %s from PHP %s (%s) to PHP %s (%s)? The new FPM master is validated before the running one is replaced.",
					site.Name, defaultString(site.PHPVersion, "-"), defaultString(site.PHPBuild, "-"),
					version, defaultString(buildID, "newest installed build")))
				if err != nil {
					return err
				}
				if !ok {
					if a.jsonOutput {
						return a.writeJSON(cmd, site)
					}
					fmt.Fprintln(cmd.OutOrStdout(), "PHP change cancelled.")
					return nil
				}
			}

			updated, err := c.ApplyPHP(ctx, args[0], sites.PHPChange{Version: version, Build: buildID})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, updated)
			}
			return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
				{"Name", updated.Name},
				{"PHP version", defaultString(updated.PHPVersion, "-")},
				{"PHP build", defaultString(updated.PHPBuild, "-")},
				{"Status", defaultString(string(updated.Status), "-")},
			})
		},
	}
	cmd.Flags().StringVar(&build, "build", "", "exact PHP build id (for example php@8.5.0-r1)")
	cmd.Flags().BoolVar(&list, "list", false, "list installed and available builds for the line")
	return cmd
}

// printSitePHP renders the current version and build.
func (a *App) printSitePHP(cmd *cobra.Command, site model.Site) error {
	if a.jsonOutput {
		return a.writeJSON(cmd, struct {
			Name            string `json:"name"`
			PHPVersion      string `json:"php_version"`
			PHPBuild        string `json:"php_build"`
			FrontController bool   `json:"front_controller"`
		}{Name: site.Name, PHPVersion: site.PHPVersion, PHPBuild: site.PHPBuild, FrontController: site.FrontController})
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"Name", site.Name},
		{"PHP version", defaultString(site.PHPVersion, "-")},
		{"PHP build", defaultString(site.PHPBuild, "-")},
		{"Front controller", strconv.FormatBool(site.FrontController)},
	})
}

// runSitePHPList prints the installed and catalog builds for one line.
func (a *App) runSitePHPList(cmd *cobra.Command, c Client, site model.Site, args []string) error {
	line := strings.TrimSpace(site.PHPVersion)
	if len(args) == 1 {
		normalized, ok := normalizePHPLine(args[0])
		if !ok {
			return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP version %q (want a line such as 8.4)", args[0]), Remediation: "Pass a PHP major.minor line."}
		}
		line = normalized
	}
	if line == "" {
		return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("site %q has no PHP version", site.Name), Remediation: "Pass the line to list, for example `devstack site php blog 8.4 --list`."}
	}
	installed, err := c.Runtimes(cmd.Context())
	if err != nil {
		return err
	}
	available, err := c.AvailableRuntimes(cmd.Context())
	if err != nil {
		return err
	}
	var installedIDs, availableIDs []string
	for _, build := range installed {
		if buildLine, ok := runtimeBuildPHPLine(build); !ok || buildLine != line {
			continue
		}
		installedIDs = append(installedIDs, build.ID)
	}
	for _, entry := range available {
		if entryLine, ok := catalogEntryPHPLine(entry); !ok || entryLine != line {
			continue
		}
		availableIDs = append(availableIDs, entry.ID)
	}
	sort.Strings(installedIDs)
	sort.Strings(availableIDs)
	if a.jsonOutput {
		return a.writeJSON(cmd, struct {
			Version   string   `json:"version"`
			Installed []string `json:"installed"`
			Available []string `json:"available"`
		}{Version: line, Installed: installedIDs, Available: availableIDs})
	}
	rows := make([][]string, 0, max(len(installedIDs), len(availableIDs)))
	for i := 0; i < max(len(installedIDs), len(availableIDs)); i++ {
		row := []string{"", ""}
		if i < len(installedIDs) {
			row[0] = installedIDs[i]
		}
		if i < len(availableIDs) {
			row[1] = availableIDs[i]
		}
		rows = append(rows, row)
	}
	return writeTable(cmd, []string{"INSTALLED", "AVAILABLE"}, rows)
}

type siteIniOptions struct {
	sets      []string
	unsets    []string
	raw       string
	rawFile   string
	applyOnly bool
}

// newSiteIniCmd prints or replaces a site's php.ini overrides.
func (a *App) newSiteIniCmd() *cobra.Command {
	opts := &siteIniOptions{}
	cmd := &cobra.Command{
		Use:   "ini <name>",
		Short: "Show or edit a site's php.ini overrides",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawChanged := cmd.Flags().Changed("raw")
			if rawChanged && strings.TrimSpace(opts.rawFile) != "" {
				return &Error{Code: "INVALID_FLAGS", Message: "--raw and --raw-file cannot be used together", Remediation: "Pass the directives inline with --raw, or from a file with --raw-file."}
			}
			mutating := rawChanged || strings.TrimSpace(opts.rawFile) != "" ||
				len(opts.sets) != 0 || len(opts.unsets) != 0 || opts.applyOnly

			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if !mutating {
				info, err := c.PHPInfo(ctx, args[0])
				if err != nil {
					return err
				}
				if a.jsonOutput {
					return a.writeJSON(cmd, info)
				}
				out := cmd.OutOrStdout()
				if info.GeneratedIni != "" {
					fmt.Fprint(out, info.GeneratedIni)
					if !strings.HasSuffix(info.GeneratedIni, "\n") {
						fmt.Fprintln(out)
					}
					fmt.Fprintln(out)
				}
				keys := sortedStringKeys(info.Ini)
				rows := make([][]string, 0, len(keys))
				for _, key := range keys {
					rows = append(rows, []string{key, info.Ini[key]})
				}
				if err := writeTable(cmd, []string{"OVERRIDE", "VALUE"}, rows); err != nil {
					return err
				}
				if info.IniRaw != "" {
					fmt.Fprintln(out)
					fmt.Fprintln(out, "Raw section:")
					fmt.Fprint(out, info.IniRaw)
					if !strings.HasSuffix(info.IniRaw, "\n") {
						fmt.Fprintln(out)
					}
				}
				return nil
			}

			site, err := c.GetSite(ctx, args[0])
			if err != nil {
				return err
			}
			if site.Kind != model.SitePHP {
				return notPHPSite(site.Name)
			}
			merged := cloneStringMap(site.PHPIni)
			for _, raw := range opts.sets {
				key, value, ok := strings.Cut(raw, "=")
				key = strings.TrimSpace(key)
				if !ok || key == "" {
					return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid --set %q, want KEY=VALUE", raw), Remediation: "Pass each override as `--set key=value`."}
				}
				if !phpDirectivePattern.MatchString(key) || strings.ContainsAny(value, "\x00\r\n") {
					return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid php.ini directive in --set %q", raw), Remediation: "Directive names may contain letters, digits, dots, dashes, and underscores; values may not contain newlines."}
				}
				merged[key] = value
			}
			for _, raw := range opts.unsets {
				key := strings.TrimSpace(raw)
				if key == "" || !phpDirectivePattern.MatchString(key) {
					return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid --unset %q", raw), Remediation: "Pass the directive name to remove, for example `--unset memory_limit`."}
				}
				delete(merged, key)
			}
			var rawPtr *string
			if rawChanged || strings.TrimSpace(opts.rawFile) != "" {
				raw := opts.raw
				if strings.TrimSpace(opts.rawFile) != "" {
					data, err := os.ReadFile(opts.rawFile)
					if err != nil {
						return wrapError(fmt.Errorf("read --raw-file %s: %w", opts.rawFile, err), "INVALID_ARGUMENT", "Check the file path and permissions.")
					}
					raw = string(data)
				}
				rawPtr = &raw
			}

			if !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf("Apply php.ini changes to %s? The whole override set and raw section are replaced.", site.Name))
				if err != nil {
					return err
				}
				if !ok {
					if a.jsonOutput {
						return a.writeJSON(cmd, site)
					}
					fmt.Fprintln(cmd.OutOrStdout(), "PHP configuration change cancelled.")
					return nil
				}
			}
			updated, err := c.ApplyPHP(ctx, args[0], sites.PHPChange{Ini: merged, IniRaw: rawPtr})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, updated)
			}
			return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
				{"Name", updated.Name},
				{"INI overrides", strconv.Itoa(len(updated.PHPIni))},
				{"Raw section", strconv.FormatBool(updated.PHPIniRaw != "")},
				{"Status", defaultString(string(updated.Status), "-")},
			})
		},
	}
	flags := cmd.Flags()
	flags.StringArrayVar(&opts.sets, "set", nil, "set a php.ini directive KEY=VALUE (repeatable)")
	flags.StringArrayVar(&opts.unsets, "unset", nil, "remove a site php.ini override (repeatable)")
	flags.StringVar(&opts.raw, "raw", "", "replace the raw php.ini section with this text")
	flags.StringVar(&opts.rawFile, "raw-file", "", "replace the raw php.ini section with a file's contents")
	flags.BoolVar(&opts.applyOnly, "apply", false, "re-apply the current overrides without changing them")
	return cmd
}

type siteExtOptions struct {
	enable  []string
	disable []string
	inherit []string
}

// newSiteExtCmd lists or replaces a site's PHP extension overrides.
func (a *App) newSiteExtCmd() *cobra.Command {
	opts := &siteExtOptions{}
	cmd := &cobra.Command{
		Use:   "ext <name>",
		Short: "Show or change a site's PHP extensions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mutating := len(opts.enable) != 0 || len(opts.disable) != 0 || len(opts.inherit) != 0
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if !mutating {
				info, err := c.PHPInfo(ctx, args[0])
				if err != nil {
					return err
				}
				if a.jsonOutput {
					return a.writeJSON(cmd, info)
				}
				return writeExtensionTable(cmd, info)
			}

			selected := make(map[string]string, len(opts.enable)+len(opts.disable)+len(opts.inherit))
			remember := func(name, action string) error {
				name = strings.TrimSpace(name)
				if !phpExtensionPattern.MatchString(name) {
					return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP extension %q", name), Remediation: "Extension names are lowercase letters, digits, and underscores, for example `xdebug`."}
				}
				if previous, ok := selected[name]; ok && previous != action {
					return &Error{Code: "INVALID_FLAGS", Message: fmt.Sprintf("extension %q is both %s and %s", name, previous, action), Remediation: "Pass each extension in exactly one of --enable, --disable, or --inherit."}
				}
				selected[name] = action
				return nil
			}
			for _, name := range opts.enable {
				if err := remember(name, "enabled"); err != nil {
					return err
				}
			}
			for _, name := range opts.disable {
				if err := remember(name, "disabled"); err != nil {
					return err
				}
			}
			for _, name := range opts.inherit {
				if err := remember(name, "inherited"); err != nil {
					return err
				}
			}

			site, err := c.GetSite(ctx, args[0])
			if err != nil {
				return err
			}
			if site.Kind != model.SitePHP {
				return notPHPSite(site.Name)
			}
			merged := cloneBoolMap(site.PHPExtensions)
			for name, action := range selected {
				switch action {
				case "enabled":
					merged[name] = true
				case "disabled":
					merged[name] = false
				case "inherited":
					delete(merged, name)
				}
			}

			if !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf("Apply PHP extension changes to %s? The whole override set is replaced.", site.Name))
				if err != nil {
					return err
				}
				if !ok {
					if a.jsonOutput {
						return a.writeJSON(cmd, site)
					}
					fmt.Fprintln(cmd.OutOrStdout(), "PHP extension change cancelled.")
					return nil
				}
			}
			updated, err := c.ApplyPHP(ctx, args[0], sites.PHPChange{Extensions: merged})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, updated)
			}
			return writeExtensionOverrideTable(cmd, updated.PHPExtensions)
		},
	}
	flags := cmd.Flags()
	flags.StringArrayVar(&opts.enable, "enable", nil, "enable an extension (repeatable)")
	flags.StringArrayVar(&opts.disable, "disable", nil, "disable an extension (repeatable)")
	flags.StringArrayVar(&opts.inherit, "inherit", nil, "remove a site override and inherit the default (repeatable)")
	return cmd
}

// writeExtensionTable renders the effective, default, and source state of
// every extension and built-in module.
func writeExtensionTable(cmd *cobra.Command, info sites.PHPInfo) error {
	effective := make(map[string]bool, len(info.Extensions))
	for _, extension := range info.Extensions {
		effective[extension.Name] = true
	}
	type row struct {
		name    string
		builtin bool
	}
	byName := make(map[string]row, len(info.AvailableExtensions)+len(info.Builtins))
	for _, extension := range info.AvailableExtensions {
		byName[extension.Name] = row{name: extension.Name}
	}
	for _, name := range info.Builtins {
		byName[name] = row{name: name, builtin: true}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		entry := byName[name]
		if entry.builtin {
			rows = append(rows, []string{name, "yes", "yes", "builtin"})
			continue
		}
		defaultValue := info.Defaults.Extensions[name]
		source := "default"
		if _, ok := info.Site.PHPExtensions[name]; ok {
			source = "site"
		}
		rows = append(rows, []string{
			name,
			strconv.FormatBool(effective[name]),
			strconv.FormatBool(defaultValue),
			source,
		})
	}
	return writeTable(cmd, []string{"EXTENSION", "EFFECTIVE", "DEFAULT", "SOURCE"}, rows)
}

// writeExtensionOverrideTable renders the explicit site overrides.
func writeExtensionOverrideTable(cmd *cobra.Command, overrides map[string]bool) error {
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, []string{name, strconv.FormatBool(overrides[name])})
	}
	return writeTable(cmd, []string{"EXTENSION", "OVERRIDE"}, rows)
}

// resolvePHP picks an installed PHP build for the requested line or installs
// the newest catalog build when none is installed.
func (a *App) resolvePHP(ctx context.Context, c Client, cmd *cobra.Command, requested, requestedBuild string) (string, string, error) {
	line, buildID, err := parsePHPRequest(requested, requestedBuild)
	if err != nil {
		return "", "", err
	}
	available, err := c.AvailableRuntimes(ctx)
	if err != nil {
		return "", "", err
	}
	installed, err := c.Runtimes(ctx)
	if err != nil {
		return "", "", err
	}

	if buildID != "" {
		for _, build := range installed {
			if build.ID == buildID {
				return line, buildID, nil
			}
		}
		entry, ok := findCatalogEntry(available, buildID)
		if !ok {
			return "", "", &Error{
				Code:        "RUNTIME_UNAVAILABLE",
				Message:     fmt.Sprintf("PHP build %s is not in the runtime catalog and is not installed", buildID),
				Remediation: "Run `devstack runtime list` to see available PHP builds.",
			}
		}
		return a.installPHPEntry(ctx, c, cmd, line, entry)
	}

	if installedID := newestInstalledPHP(installed, line); installedID != "" {
		return line, installedID, nil
	}
	entry, ok := newestAvailablePHP(available, line)
	if !ok {
		return "", "", &Error{
			Code:        "RUNTIME_UNAVAILABLE",
			Message:     fmt.Sprintf("PHP %s is not in the runtime catalog (available lines: %s)", line, strings.Join(catalogPHPLines(available), ", ")),
			Remediation: "Run `devstack runtime list` to see available PHP builds.",
		}
	}
	return a.installPHPEntry(ctx, c, cmd, line, entry)
}

// installPHPEntry confirms and installs one catalog entry.
func (a *App) installPHPEntry(ctx context.Context, c Client, cmd *cobra.Command, line string, entry catalog.Entry) (string, string, error) {
	install := a.assumeYes
	if !install {
		ok, err := a.confirm(cmd, fmt.Sprintf("Install PHP %s (%s)? %s.", entry.Version, entry.ID, describeCatalogEntry(entry)))
		if err != nil {
			return "", "", err
		}
		install = ok
	}
	if !install {
		return "", "", &Error{
			Code:        "RUNTIME_MISSING",
			Message:     fmt.Sprintf("PHP %s is required but is not installed", line),
			Remediation: fmt.Sprintf("Run `devstack runtime install %s` or rerun with --yes.", entry.ID),
		}
	}
	build, err := c.InstallRuntime(ctx, entry.ID)
	if err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(build.ID)
	if id == "" {
		id = entry.ID
	}
	return line, id, nil
}

// parsePHPRequest normalizes a --php line and an optional --php-build.
func parsePHPRequest(requested, requestedBuild string) (string, string, error) {
	line, ok := normalizePHPLine(requested)
	if !ok {
		return "", "", &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP version %q (want a line such as 8.4)", requested), Remediation: "Pass a PHP major.minor line, for example `--php 8.4`."}
	}
	build := strings.TrimSpace(requestedBuild)
	if build == "" {
		return line, "", nil
	}
	id, err := catalog.ParseID(build)
	if err != nil || id.Kind != "php" {
		return "", "", &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("invalid PHP build id %q", build), Remediation: "Pass a PHP build id such as `php@8.4.4-r1`."}
	}
	if catalog.LineOf(id.Version) != line {
		return "", "", &Error{Code: "INVALID_FLAGS", Message: fmt.Sprintf("build %s belongs to PHP %s, not %s", build, catalog.LineOf(id.Version), line), Remediation: "Choose a build on the requested line."}
	}
	return line, build, nil
}

// normalizePHPLine accepts a full version or a major.minor line and returns
// the line.
func normalizePHPLine(value string) (string, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "php@")
	if phpLinePattern.MatchString(value) {
		return value, true
	}
	if phpVersionPattern.MatchString(value) {
		return catalog.LineOf(value), true
	}
	return "", false
}

// findCatalogEntry returns one entry by id.
func findCatalogEntry(entries []catalog.Entry, id string) (catalog.Entry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return catalog.Entry{}, false
}

// newestInstalledPHP returns the newest installed PHP build on a line.
func newestInstalledPHP(builds []model.RuntimeBuild, line string) string {
	best := ""
	var bestID catalog.ID
	for _, build := range builds {
		id, ok := parsePHPBuildID(build)
		if !ok || catalog.LineOf(id.Version) != line {
			continue
		}
		if best == "" || comparePHPIDs(id, bestID) > 0 {
			best, bestID = build.ID, id
		}
	}
	return best
}

// parsePHPBuildID parses an installed PHP build's id, falling back to its
// recorded version when the id is not a canonical runtime id.
func parsePHPBuildID(build model.RuntimeBuild) (catalog.ID, bool) {
	if build.Kind != "" && build.Kind != "php" {
		return catalog.ID{}, false
	}
	if id, err := catalog.ParseID(build.ID); err == nil && id.Kind == "php" {
		return id, true
	}
	version := strings.TrimSpace(build.Version)
	if version == "" {
		return catalog.ID{}, false
	}
	return catalog.ID{Kind: "php", Version: version}, true
}

// runtimeBuildPHPLine reports the PHP line of an installed build.
func runtimeBuildPHPLine(build model.RuntimeBuild) (string, bool) {
	id, ok := parsePHPBuildID(build)
	if !ok {
		return "", false
	}
	return catalog.LineOf(id.Version), true
}

// newestAvailablePHP returns the newest catalog PHP entry on a line.
func newestAvailablePHP(entries []catalog.Entry, line string) (catalog.Entry, bool) {
	best := catalog.Entry{}
	var bestID catalog.ID
	found := false
	for _, entry := range entries {
		entryLine, ok := catalogEntryPHPLine(entry)
		if !ok || entryLine != line {
			continue
		}
		id, err := catalog.ParseID(entry.ID)
		if err != nil {
			id = catalog.ID{Kind: "php", Version: entry.Version}
		}
		if !found || comparePHPIDs(id, bestID) > 0 {
			best, bestID, found = entry, id, true
		}
	}
	return best, found
}

func comparePHPIDs(a, b catalog.ID) int {
	if compared := compareDotted(a.Version, b.Version); compared != 0 {
		return compared
	}
	switch {
	case a.Revision < b.Revision:
		return -1
	case a.Revision > b.Revision:
		return 1
	}
	return 0
}

// compareDotted compares dotted numeric versions component by component.
func compareDotted(a, b string) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var av, bv int
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}

// catalogPHPLines returns the sorted PHP lines present in the catalog.
func catalogPHPLines(entries []catalog.Entry) []string {
	seen := make(map[string]bool)
	for _, entry := range entries {
		if line, ok := catalogEntryPHPLine(entry); ok {
			seen[line] = true
		}
	}
	lines := make([]string, 0, len(seen))
	for line := range seen {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return []string{"none"}
	}
	return lines
}

// catalogEntryPHPLine reports the PHP line of one catalog entry.
func catalogEntryPHPLine(entry catalog.Entry) (string, bool) {
	if entry.Kind != "" && entry.Kind != "php" {
		return "", false
	}
	if id, err := catalog.ParseID(entry.ID); err == nil && id.Kind == "php" {
		return catalog.LineOf(id.Version), true
	}
	version := strings.TrimSpace(entry.Version)
	if version == "" {
		return "", false
	}
	return catalog.LineOf(version), true
}

// describeCatalogEntry renders the install-consent metadata for one entry.
func describeCatalogEntry(entry catalog.Entry) string {
	parts := []string{
		"version " + entry.Version,
		fmt.Sprintf("revision %d", entry.Revision),
		"size " + formatBytes(entry.Size),
		"sha256 " + entry.SHA256,
	}
	if strings.TrimSpace(entry.Build) != "" {
		parts = append(parts, "build "+entry.Build)
	}
	return strings.Join(parts, ", ")
}

// formatBytes renders a compact binary size for prompts.
func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	index := -1
	for value >= unit && index+1 < len(units) {
		value /= unit
		index++
	}
	return fmt.Sprintf("%.1f %s", value, units[index])
}

func notPHPSite(name string) error {
	return &Error{
		Code:        "INVALID_SITE_KIND",
		Message:     fmt.Sprintf("site %q is not a PHP site", name),
		Remediation: "PHP commands only apply to sites created with `--kind php`.",
	}
}

func sortedStringKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneStringMap(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
