package cli

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

const (
	// Documented defaults shown by `devstack vhost path` before any external
	// release exists. Active external modes derive their current path from the
	// helper's CurrentRelease.
	defaultNginxVhostCurrent  = "/etc/devstack/vhosts/nginx/current"
	defaultApacheVhostCurrent = "/etc/devstack/vhosts/apache/current"
	defaultNginxBridge        = "/etc/nginx/conf.d/devstack-vhosts.conf"
	defaultApacheBridge       = "/etc/apache2/conf-enabled/devstack-vhosts.conf"
)

func (a *App) newVhostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vhost",
		Short: "Manage native nginx and Apache vhosts",
	}
	cmd.AddCommand(
		a.newVhostStatusCmd(),
		a.newVhostUseCmd(),
		a.newVhostSyncCmd(),
		a.newVhostPreviewCmd(),
		a.newVhostPathCmd(),
	)
	return cmd
}

func (a *App) newVhostStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the serving mode and vhost state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			status, err := c.VhostStatus(ctx)
			if err != nil {
				return err
			}
			return a.printVhostStatus(cmd, status)
		},
	}
}

func (a *App) newVhostUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use nginx|apache|builtin",
		Short: "Switch the serving mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := strings.ToLower(strings.TrimSpace(args[0]))
			if !config.ValidServingMode(mode) {
				return &Error{
					Code:        "INVALID_ARGUMENT",
					Message:     fmt.Sprintf("unsupported serving mode %q (want builtin, nginx, or apache)", args[0]),
					Remediation: "Use `devstack vhost use builtin`, `nginx`, or `apache`.",
				}
			}

			if !a.assumeYes {
				if a.jsonOutput {
					return &Error{
						Code:        "CONFIRMATION_REQUIRED",
						Message:     fmt.Sprintf("switching the serving mode to %s requires confirmation", mode),
						Remediation: fmt.Sprintf("Re-run `devstack vhost use %s --yes`.", mode),
					}
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), vhostUseWarning(mode)); err != nil {
					return err
				}
				ok, err := a.confirm(cmd, fmt.Sprintf("Switch the serving mode to %s?", mode))
				if err != nil {
					return err
				}
				if !ok {
					return &Error{
						Code:        "CANCELLED",
						Message:     "serving mode was not changed",
						Remediation: fmt.Sprintf("Run `devstack vhost use %s --yes` to switch without a prompt.", mode),
					}
				}
			}

			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			status, err := c.SetServingMode(ctx, mode)
			if err != nil {
				return err
			}
			return a.printVhostStatus(cmd, status)
		},
	}
}

func (a *App) newVhostSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reinstall the complete vhost set",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			status, err := c.SyncVhosts(ctx)
			if err != nil {
				return err
			}
			return a.printVhostStatus(cmd, status)
		},
	}
}

func (a *App) newVhostPreviewCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "preview <site>",
		Short: "Render a site's nginx or Apache vhost",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return &Error{
					Code:        "INVALID_ARGUMENT",
					Message:     "a site name or id is required",
					Remediation: "Run `devstack site list` to see registered sites.",
				}
			}

			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}

			selected := strings.ToLower(strings.TrimSpace(server))
			if selected == "" {
				status, err := c.VhostStatus(ctx)
				if err != nil {
					return err
				}
				selected = activeVhostServer(status)
			}
			switch selected {
			case "nginx", "apache":
			default:
				return &Error{
					Code:        "INVALID_ARGUMENT",
					Message:     fmt.Sprintf("unsupported vhost server %q (want nginx or apache)", server),
					Remediation: "Use `--server nginx` or `--server apache`.",
				}
			}

			content, err := c.PreviewVhost(ctx, ref, selected)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Server  string `json:"server"`
					Site    string `json:"site"`
					Content string `json:"content"`
				}{Server: selected, Site: ref, Content: content})
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), content); err != nil {
				return err
			}
			if content != "" && !strings.HasSuffix(content, "\n") {
				_, err = fmt.Fprintln(cmd.OutOrStdout())
			}
			return err
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "vhost server: nginx or apache (default: the active external server)")
	return cmd
}

func (a *App) newVhostPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the installed vhost and bridge paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			status, err := c.VhostStatus(ctx)
			if err != nil {
				return err
			}

			mode := strings.ToLower(strings.TrimSpace(status.Mode))
			if mode == "" {
				mode = config.ServingModeBuiltin
			}
			server := strings.ToLower(strings.TrimSpace(status.Server))
			external := mode == config.ServingModeNginx || mode == config.ServingModeApache
			if external {
				if server != "nginx" && server != "apache" {
					server = mode
				}
			} else {
				server = config.ServingModeBuiltin
			}

			nginxCurrent := defaultNginxVhostCurrent
			apacheCurrent := defaultApacheVhostCurrent
			if external {
				if current, ok := currentFromRelease(status.CurrentRelease, server); ok {
					if server == "nginx" {
						nginxCurrent = current
					} else {
						apacheCurrent = current
					}
				}
			}

			bridge := defaultNginxBridge
			if external {
				if reported := strings.TrimSpace(status.BridgePath); reported != "" {
					bridge = reported
				} else if server == "apache" {
					bridge = defaultApacheBridge
				}
			}

			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					Mode          string `json:"mode"`
					Server        string `json:"server"`
					NginxCurrent  string `json:"nginx_current"`
					ApacheCurrent string `json:"apache_current"`
					Bridge        string `json:"bridge"`
				}{
					Mode:          mode,
					Server:        server,
					NginxCurrent:  nginxCurrent,
					ApacheCurrent: apacheCurrent,
					Bridge:        bridge,
				})
			}
			return writeTable(cmd, []string{"PATH", "LOCATION"}, [][]string{
				{"NGINX", nginxCurrent},
				{"APACHE", apacheCurrent},
				{"BRIDGE", bridge},
			})
		},
	}
}

// printVhostStatus renders the helper status as a table or the raw JSON object.
func (a *App) printVhostStatus(cmd *cobra.Command, status helperproto.VhostStatusResponse) error {
	if a.jsonOutput {
		return a.writeJSON(cmd, status)
	}
	rows := [][]string{
		{"Mode", defaultString(strings.TrimSpace(status.Mode), config.ServingModeBuiltin)},
		{"Server", vhostServerLabel(status)},
		{"Include", presentLabel(status.IncludePresent)},
		{"Config", configLabel(status.ConfigOK)},
		{"Release", defaultString(strings.TrimSpace(status.CurrentRelease), "-")},
		{"Last sync", vhostTimeLabel(status.LastSync)},
	}
	if lastError := strings.TrimSpace(status.LastError); lastError != "" {
		rows = append(rows, []string{"Last error", lastError})
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, rows)
}

func vhostUseWarning(mode string) string {
	if mode == config.ServingModeBuiltin {
		return "Switching to builtin stops nginx or Apache, removes the devstack vhosts and bridge include, restores the built-in proxy and 80/443 forwarders, and causes brief downtime for every site."
	}
	return fmt.Sprintf("Switching to %s makes %s own ports 80/443, stops the built-in proxy, reloads %s, and causes brief downtime for every site.", mode, mode, mode)
}

// activeVhostServer reports the external server behind the current status,
// falling back to nginx so preview works in builtin mode and on old helpers.
func activeVhostServer(status helperproto.VhostStatusResponse) string {
	mode := strings.ToLower(strings.TrimSpace(status.Mode))
	if mode == config.ServingModeNginx || mode == config.ServingModeApache {
		return mode
	}
	server := strings.ToLower(strings.TrimSpace(status.Server))
	if server == config.ServingModeNginx || server == config.ServingModeApache {
		return server
	}
	return config.ServingModeNginx
}

// currentFromRelease maps <server>/releases/<timestamp> to <server>/current.
func currentFromRelease(release, server string) (string, bool) {
	release = strings.TrimSpace(release)
	if release == "" {
		return "", false
	}
	if strings.HasPrefix(release, "/") {
		clean := path.Clean(release)
		releasesDir := path.Dir(clean)
		if path.Base(releasesDir) != "releases" {
			return "", false
		}
		serverDir := path.Dir(releasesDir)
		if server != "" && path.Base(serverDir) != server {
			return "", false
		}
		if serverDir == "." || serverDir == "/" {
			return "", false
		}
		return path.Join(serverDir, "current"), true
	}
	clean := filepath.Clean(release)
	releasesDir := filepath.Dir(clean)
	if filepath.Base(releasesDir) != "releases" {
		return "", false
	}
	serverDir := filepath.Dir(releasesDir)
	if server != "" && filepath.Base(serverDir) != server {
		return "", false
	}
	if serverDir == "." || serverDir == string(filepath.Separator) {
		return "", false
	}
	return filepath.Join(serverDir, "current"), true
}

func vhostServerLabel(status helperproto.VhostStatusResponse) string {
	server := strings.TrimSpace(status.Server)
	if server == "" {
		server = defaultString(strings.TrimSpace(status.Mode), config.ServingModeBuiltin)
	}
	if version := strings.TrimSpace(status.ServerVersion); version != "" {
		return server + " " + version
	}
	return server
}

func presentLabel(ok bool) string {
	if ok {
		return "present"
	}
	return "missing"
}

func configLabel(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func vhostTimeLabel(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}
