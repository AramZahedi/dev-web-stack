package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (a *App) newSiteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Manage local sites",
	}
	cmd.AddCommand(
		a.newSiteAddCmd(),
		a.newSiteSourceCmd(),
		a.newSiteListCmd(),
		a.newSiteShowCmd(),
		a.newSitePHPShowCmd(),
		a.newSiteIniCmd(),
		a.newSiteExtCmd(),
		a.newSiteActionCmd("start"),
		a.newSiteActionCmd("stop"),
		a.newSiteActionCmd("restart"),
		a.newSiteRemoveCmd(),
		a.newSiteLogsCmd(),
		a.newSiteOpenCmd(),
		a.newSiteBuildCmd(),
	)
	return cmd
}

type sourceModeClient interface {
	ChangeSourceMode(context.Context, string, model.SourceMode, string, bool) (model.Site, error)
}

func (a *App) newSiteSourceCmd() *cobra.Command {
	var mode, path string
	var removeSource bool
	cmd := &cobra.Command{
		Use:   "source <name>",
		Short: "Copy a stopped site's source into another source mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode != string(model.SourceLocal) && mode != string(model.SourceWindows) && mode != string(model.SourceWSL) {
				return &Error{Code: "INVALID_SOURCE_MODE", Message: "--mode must be local, windows, or wsl", Remediation: "Choose a supported source mode and pass the destination with --path."}
			}
			if strings.TrimSpace(path) == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--path is required", Remediation: "Pass the copy destination path with --path."}
			}
			c, _, err := a.clientReady(cmd.Context())
			if err != nil {
				return err
			}
			copyClient, ok := c.(sourceModeClient)
			if !ok {
				return &Error{Code: "SOURCE_COPY_UNAVAILABLE", Message: "the daemon client does not support verified source copies", Remediation: "Update the daemon client before changing a site's source mode."}
			}
			site, err := copyClient.ChangeSourceMode(cmd.Context(), args[0], model.SourceMode(mode), strings.TrimSpace(path), removeSource)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, site)
			}
			disposition := "kept"
			if removeSource {
				disposition = "removed"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Changed %s source mode to %s. The previous source was %s.\n", site.Name, site.SourceMode, disposition)
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "destination source mode: local, windows, or wsl")
	cmd.Flags().StringVar(&path, "path", "", "destination project path")
	cmd.Flags().BoolVar(&removeSource, "remove-source", false, "request source removal after verified copy (requires staged agent support)")
	return cmd
}

type siteAddOptions struct {
	name            string
	path            string
	kind            string
	sourceMode      string
	node            string
	php             string
	phpBuild        string
	frontController bool
	command         string
	documentRoot    string
	www             bool
	noHTTPS         bool
	forceHTTPS      bool
	spa             bool
	env             []string
}

func (a *App) newSiteAddCmd() *cobra.Command {
	opts := &siteAddOptions{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create and start a site",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runSiteAdd(cmd, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.name, "name", "", "site name (the domain becomes <name>.test)")
	flags.StringVar(&opts.path, "path", "", "project directory")
	flags.StringVar(&opts.kind, "kind", "static", "site kind: static, php, or node")
	flags.StringVar(&opts.sourceMode, "source-mode", "", "project source mode: local, windows, or wsl")
	flags.StringVar(&opts.node, "node", "", "Node version (for example 22 or 24)")
	flags.StringVar(&opts.php, "php", "", "PHP version line (for example 8.4)")
	flags.StringVar(&opts.phpBuild, "php-build", "", "exact PHP build id (for example php@8.4.4-r1)")
	flags.BoolVar(&opts.frontController, "front-controller", false, "route requests to the site's index.php")
	flags.StringVar(&opts.command, "command", "", "Node start command")
	flags.StringVar(&opts.documentRoot, "document-root", "", "document root relative to the project or absolute within it")
	flags.BoolVar(&opts.www, "www", false, "also serve www.<name>.test")
	flags.BoolVar(&opts.noHTTPS, "no-https", false, "serve over HTTP only")
	flags.BoolVar(&opts.forceHTTPS, "force-https", false, "redirect HTTP requests to HTTPS")
	flags.BoolVar(&opts.spa, "spa", false, "serve index.html for non-file paths")
	flags.StringArrayVar(&opts.env, "env", nil, "environment variable KEY=VALUE (repeatable)")
	return cmd
}

func (a *App) runSiteAdd(cmd *cobra.Command, opts *siteAddOptions) error {
	name := strings.TrimSpace(opts.name)
	path := strings.TrimSpace(opts.path)
	kind := strings.ToLower(strings.TrimSpace(opts.kind))
	if kind == "" {
		kind = string(model.SiteStatic)
	}
	if name == "" {
		return &Error{Code: "INVALID_ARGUMENT", Message: "--name is required", Remediation: "Pass a short site name, for example `--name blog`."}
	}
	if path == "" {
		return &Error{Code: "INVALID_ARGUMENT", Message: "--path is required", Remediation: "Pass the project directory, for example `--path ~/projects/blog`."}
	}
	switch kind {
	case string(model.SiteStatic), string(model.SiteNode), string(model.SitePHP):
	default:
		return &Error{
			Code:        "INVALID_KIND",
			Message:     fmt.Sprintf("unsupported site kind %q (want static, php, or node)", opts.kind),
			Remediation: "Use `--kind static`, `--kind php`, or `--kind node`.",
		}
	}
	if opts.noHTTPS && opts.forceHTTPS {
		return &Error{Code: "INVALID_FLAGS", Message: "--no-https and --force-https cannot be used together", Remediation: "Choose one TLS mode."}
	}
	if kind == string(model.SitePHP) {
		if strings.TrimSpace(opts.php) == "" && strings.TrimSpace(opts.phpBuild) == "" {
			c, _, err := a.clientReady(cmd.Context())
			if err == nil {
				if def, err := c.PHPDefault(cmd.Context()); err == nil && def.Build != nil {
					opts.php = catalog.LineOf(def.Build.Version)
					opts.phpBuild = def.Build.ID
				}
			}
			if strings.TrimSpace(opts.php) == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "PHP sites require --php", Remediation: "Pass `--php 8.4` (or another supported line)."}
			}
		}
		if strings.TrimSpace(opts.node) != "" || strings.TrimSpace(opts.command) != "" || opts.spa {
			return &Error{Code: "INVALID_FLAGS", Message: "--node, --command, and --spa are only valid for node sites", Remediation: "Remove the Node flags, or use `--kind node`."}
		}
	} else if strings.TrimSpace(opts.php) != "" || strings.TrimSpace(opts.phpBuild) != "" || opts.frontController {
		return &Error{Code: "INVALID_FLAGS", Message: "--php, --php-build, and --front-controller are only valid for PHP sites", Remediation: "Use `--kind php`, or remove the PHP flags."}
	}
	if kind == string(model.SiteStatic) {
		if strings.TrimSpace(opts.node) != "" || strings.TrimSpace(opts.command) != "" {
			return &Error{Code: "INVALID_FLAGS", Message: "--node and --command are only valid for node sites", Remediation: "Use `--kind node`, or remove the Node flags."}
		}
	}
	if kind == string(model.SiteNode) {
		if strings.TrimSpace(opts.node) == "" {
			return &Error{Code: "INVALID_ARGUMENT", Message: "node sites require --node", Remediation: "Pass `--node 22` or `--node 24`."}
		}
		if strings.TrimSpace(opts.command) == "" {
			return &Error{Code: "INVALID_ARGUMENT", Message: "node sites require --command", Remediation: "Pass the start command, for example `--command \"npm run dev\"`."}
		}
	}
	env, err := parseEnv(opts.env)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	c, _, err := a.clientReady(ctx)
	if err != nil {
		return err
	}

	nodeVersion := ""
	if kind == string(model.SiteNode) {
		nodeVersion, err = a.resolveNode(ctx, c, cmd, opts.node)
		if err != nil {
			return err
		}
	}
	phpVersion := ""
	phpBuild := ""
	if kind == string(model.SitePHP) {
		phpVersion, phpBuild, err = a.resolvePHP(ctx, c, cmd, opts.php, opts.phpBuild)
		if err != nil {
			return err
		}
	}

	site, err := c.AddSite(ctx, sites.CreateRequest{
		Name:            name,
		Path:            path,
		Kind:            kind,
		SourceMode:      strings.TrimSpace(opts.sourceMode),
		NodeVersion:     nodeVersion,
		Command:         strings.TrimSpace(opts.command),
		DocumentRoot:    strings.TrimSpace(opts.documentRoot),
		WWW:             opts.www,
		TLS:             !opts.noHTTPS,
		ForceHTTPS:      opts.forceHTTPS,
		SPAFallback:     opts.spa,
		Env:             env,
		PHPVersion:      phpVersion,
		PHPBuild:        phpBuild,
		FrontController: opts.frontController,
	})
	if err != nil {
		return err
	}

	if a.jsonOutput {
		return a.writeJSON(cmd, site)
	}
	status := defaultString(string(site.Status), "unknown")
	fmt.Fprintf(cmd.OutOrStdout(), "Created site %s\nURL:      %s\nStatus:   %s\n", site.Name, siteURL(site), status)
	return nil
}

// resolveNode picks a catalog build for the requested version and installs it
// when it is missing. The prompt goes to stderr in JSON mode.
func (a *App) resolveNode(ctx context.Context, c Client, cmd *cobra.Command, requested string) (string, error) {
	available, err := c.AvailableRuntimes(ctx)
	if err != nil {
		return "", err
	}
	entry, err := pickNodeEntry(requested, available)
	if err != nil {
		return "", &Error{
			Code:        "RUNTIME_UNAVAILABLE",
			Message:     err.Error(),
			Remediation: "Run `devstack runtime list` to see available Node versions.",
		}
	}

	installed, err := c.Runtimes(ctx)
	if err != nil {
		return "", err
	}
	if runtimeInstalled(installed, "node", entry.Version) {
		return entry.Version, nil
	}

	install := a.assumeYes
	if !install {
		ok, promptErr := a.confirm(cmd, fmt.Sprintf("Node %s is not installed. Install %s now?", entry.Version, entry.ID))
		if promptErr != nil {
			return "", promptErr
		}
		install = ok
	}
	if !install {
		return "", &Error{
			Code:        "RUNTIME_MISSING",
			Message:     fmt.Sprintf("node %s is required but is not installed", entry.Version),
			Remediation: fmt.Sprintf("Run `devstack runtime install %s` or rerun with --yes.", entry.ID),
		}
	}
	build, err := c.InstallRuntime(ctx, entry.ID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(build.Version) == "" {
		return entry.Version, nil
	}
	return build.Version, nil
}

func pickNodeEntry(requested string, entries []catalog.Entry) (catalog.Entry, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	requested = strings.TrimPrefix(requested, "node@")
	var versions []string
	for _, entry := range entries {
		if entry.Kind != "node" {
			continue
		}
		versions = append(versions, entry.Version)
		if entry.Version == requested {
			return entry, nil
		}
	}
	sort.Strings(versions)
	for _, entry := range entries {
		if entry.Kind == "node" && strings.HasPrefix(entry.Version, requested+".") {
			return entry, nil
		}
	}
	available := "none"
	if len(versions) != 0 {
		available = strings.Join(versions, ", ")
	}
	return catalog.Entry{}, fmt.Errorf("node %s is not in the runtime catalog (available: %s)", requested, available)
}

func runtimeInstalled(builds []model.RuntimeBuild, kind, version string) bool {
	for _, build := range builds {
		if build.Kind == kind && build.Version == version {
			return true
		}
	}
	return false
}

func parseEnv(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(values))
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, &Error{
				Code:        "INVALID_ENV",
				Message:     fmt.Sprintf("invalid --env %q, want KEY=VALUE", raw),
				Remediation: "Pass each variable as `--env KEY=VALUE`; repeat the flag for more variables.",
			}
		}
		if !envKeyPattern.MatchString(key) {
			return nil, &Error{
				Code:        "INVALID_ENV",
				Message:     fmt.Sprintf("invalid environment variable name %q", key),
				Remediation: "Environment variable names must match [A-Za-z_][A-Za-z0-9_]*.",
			}
		}
		env[key] = value
	}
	return env, nil
}

func (a *App) newSiteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered sites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			list, err := c.ListSites(ctx)
			if err != nil {
				return err
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			if list == nil {
				list = []model.Site{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, list)
			}
			rows := make([][]string, 0, len(list))
			for _, site := range list {
				rows = append(rows, []string{
					site.Name,
					defaultString(string(site.Kind), "-"),
					defaultString(string(site.Status), "-"),
					listingURL(site),
				})
			}
			return writeTable(cmd, []string{"NAME", "KIND", "STATUS", "URL"}, rows)
		},
	}
}

func (a *App) newSiteShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one site",
		Args:  cobra.ExactArgs(1),
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
			if a.jsonOutput {
				return a.writeJSON(cmd, site)
			}
			rows := [][]string{
				{"Name", site.Name},
				{"Domain", site.Domain},
				{"URL", siteURL(site)},
				{"Kind", defaultString(string(site.Kind), "-")},
				{"Status", defaultString(string(site.Status), "-")},
				{"Path", defaultString(site.HostPath, "-")},
				{"Document root", defaultString(site.DocumentRoot, ".")},
				{"Node version", defaultString(site.NodeVersion, "-")},
				{"PHP version", defaultString(site.PHPVersion, "-")},
				{"PHP build", defaultString(site.PHPBuild, "-")},
				{"Front controller", fmt.Sprintf("%t", site.FrontController)},
				{"INI overrides", strconv.Itoa(len(site.PHPIni))},
				{"Extension overrides", strconv.Itoa(len(site.PHPExtensions))},
				{"Command", defaultString(site.Command, "-")},
				{"TLS", fmt.Sprintf("%t", site.TLS)},
				{"Force HTTPS", fmt.Sprintf("%t", site.ForceHTTPS)},
				{"SPA fallback", fmt.Sprintf("%t", site.SPAFallback)},
			}
			return writeTable(cmd, []string{"FIELD", "VALUE"}, rows)
		},
	}
}

func (a *App) newSiteActionCmd(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <name>",
		Short: titleAction(action) + " a site",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			ref := args[0]
			if err := c.SiteAction(ctx, ref, action); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Name   string `json:"name"`
					Action string `json:"action"`
					OK     bool   `json:"ok"`
				}{Name: ref, Action: action, OK: true})
			}
			past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[action]
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", ref, past)
			return nil
		},
	}
}

func (a *App) newSiteRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a site (requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.assumeYes {
				return &Error{
					Code:        "CONFIRMATION_REQUIRED",
					Message:     fmt.Sprintf("removing site %q requires confirmation", args[0]),
					Remediation: "Re-run the command with --yes.",
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			if err := c.DeleteSite(ctx, args[0]); err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Name    string `json:"name"`
					Removed bool   `json:"removed"`
				}{Name: args[0], Removed: true})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed site %q. Project files were left untouched.\n", args[0])
			return nil
		},
	}
}

func (a *App) newSiteLogsCmd() *cobra.Command {
	var (
		follow bool
		tail   int
	)
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show or follow a site's logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--tail must not be negative", Remediation: "Pass a positive line count or omit --tail."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			return streamSiteLogs(ctx, c, args[0], follow, tail, cmd.OutOrStdout(), a.jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false, "keep streaming new lines instead of printing a snapshot")
	cmd.Flags().IntVar(&tail, "tail", 0, "show only the last N lines")
	return cmd
}

func (a *App) newSiteOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <name>",
		Short: "Open a site in the default browser",
		Args:  cobra.ExactArgs(1),
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
			url := siteURL(site)
			openErr := openBrowser(url)
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Name   string `json:"name"`
					URL    string `json:"url"`
					Opened bool   `json:"opened"`
				}{Name: site.Name, URL: url, Opened: openErr == nil})
			}
			fmt.Fprintln(cmd.OutOrStdout(), url)
			if openErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "devstack: could not open a browser: %v\n", openErr)
			}
			return nil
		},
	}
}

func titleAction(action string) string {
	if action == "" {
		return action
	}
	return strings.ToUpper(action[:1]) + action[1:]
}

func siteURL(site model.Site) string {
	scheme := "https"
	if !site.TLS {
		scheme = "http"
	}
	host := strings.TrimSpace(site.Domain)
	if host == "" {
		host = strings.TrimSpace(site.Name) + ".test"
	}
	if port := publicSitePort(site.TLS); port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

// listingURL is the canonical dashboard URL shown by `site list`. HTTPS is the
// canonical entry point even for an HTTP-only site, where the edge redirects
// to HTTP.
func listingURL(site model.Site) string {
	host := strings.TrimSpace(site.Domain)
	if host == "" {
		host = strings.TrimSpace(site.Name) + ".test"
	}
	if port := publicSitePort(true); port != "" {
		host = net.JoinHostPort(host, port)
	}
	return "https://" + host
}

// publicSitePort is the non-default host port published for a site URL. It is
// empty on a normal port-80/443 install and can be set for a non-default
// reverse-proxy or published-port layout.
func publicSitePort(tls bool) string {
	key := "DEVSTACK_PUBLIC_HTTP_PORT"
	defaultPort := "80"
	if tls {
		key = "DEVSTACK_PUBLIC_HTTPS_PORT"
		defaultPort = "443"
	}
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" || value == defaultPort {
		return ""
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return ""
	}
	return strconv.Itoa(n)
}

// streamSiteLogs renders the daemon's SSE log stream. A finite snapshot is the
// default; --follow keeps the live subscription open. A follow with a tail
// first prints the snapshot and then switches to the live subscription.
func streamSiteLogs(ctx context.Context, c Client, ref string, follow bool, tail int, out io.Writer, jsonOutput bool) error {
	if tail > 0 {
		body, err := c.Logs(ctx, ref, false)
		if err != nil {
			return err
		}
		err = copyLogLines(body, out, tail, jsonOutput)
		_ = body.Close()
		if err != nil {
			return err
		}
		if !follow {
			return nil
		}
	}
	body, err := c.Logs(ctx, ref, follow)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	if follow && tail > 0 {
		return copyLogLines(body, out, 0, jsonOutput)
	}
	return copyLogLines(body, out, tail, jsonOutput)
}

// logLineJSON is the JSONL record emitted by `site logs --json`.
type logLineJSON struct {
	Time   string `json:"time"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

func copyLogLines(reader io.Reader, out io.Writer, tail int, jsonOutput bool) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var kept []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var record logs.Line
		parsed := json.Unmarshal([]byte(payload), &record) == nil
		var rendered string
		switch {
		case jsonOutput:
			item := logLineJSON{Text: payload}
			if parsed {
				item.Time = formatLogTime(record.Time)
				item.Source = record.Source
				item.Text = record.Text
			}
			data, err := json.Marshal(item)
			if err != nil {
				return err
			}
			rendered = string(data)
		case parsed:
			rendered = formatLogLine(record)
		default:
			rendered = payload
		}
		if tail <= 0 {
			if _, err := fmt.Fprintln(out, rendered); err != nil {
				return err
			}
			continue
		}
		kept = append(kept, rendered)
		if len(kept) > tail {
			kept = kept[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if tail > 0 {
		for _, line := range kept {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatLogTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatLogLine(line logs.Line) string {
	parts := make([]string, 0, 3)
	if stamp := formatLogTime(line.Time); stamp != "" {
		parts = append(parts, stamp)
	}
	if line.Source != "" {
		parts = append(parts, line.Source)
	}
	parts = append(parts, line.Text)
	return strings.Join(parts, " ")
}
