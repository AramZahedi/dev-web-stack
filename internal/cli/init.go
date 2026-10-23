package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

// initResult is the JSON report printed by `devstack init --json`.
type initResult struct {
	CAFingerprint string         `json:"ca_fingerprint"`
	Trust         []trust.Target `json:"trust"`
	Hosts         []string       `json:"hosts"`
	Doctor        doctor.Report  `json:"doctor"`
	DashboardURL  string         `json:"dashboard_url"`
	FallbackURL   string         `json:"fallback_url"`
}

func (a *App) newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Install and verify the devstack integration",
		Long:  "init ensures the helper CA, trust stores, managed hosts block, daemon, and diagnostics are ready.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runInit(cmd)
		},
	}
}

func (a *App) runInit(cmd *cobra.Command) error {
	ctx := cmd.Context()
	home, err := resolveHome(a.home)
	if err != nil {
		return err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return wrapError(err, "CONFIG_INVALID", "Fix config.yaml or choose a different --home.")
	}
	userHome, err := userHomeDir()
	if err != nil {
		return wrapError(fmt.Errorf("resolve user home: %w", err), "HOME_UNRESOLVED", "Set HOME to the user whose trust stores should be managed.")
	}

	socket := a.helperSocketPath()
	helper, err := dialHelper(socket)
	if err != nil {
		return wrapError(fmt.Errorf("connect to devstack-helper at %s: %w", socket, err), "HELPER_UNAVAILABLE", "Ensure devstack-helper is installed and running, then rerun `devstack init`.")
	}
	defer func() { _ = helper.Close() }()

	ca, err := helper.EnsureCA(ctx)
	if err != nil {
		return helperCallError(err, socket)
	}

	// External modes let nginx/Apache own 80/443. Enabling the built-in
	// forwarders would either fail with EADDRINUSE or steal the ports from the
	// server. The daemon reconciles helper state from config.yaml after it
	// starts, so init only needs to avoid forcing the forwarders on.
	helperExternal := false
	if status, statusErr := helper.VhostStatus(ctx); statusErr == nil {
		switch strings.ToLower(strings.TrimSpace(status.Mode)) {
		case "nginx", "apache":
			helperExternal = true
		}
	}
	if !helperExternal && cfg.ServingMode == config.ServingModeBuiltin {
		if err := helper.SetForwarders(ctx, cfg.HTTPPort, cfg.HTTPSPort, true); err != nil {
			return helperCallError(err, socket)
		}
	}
	targets, err := helper.InstallTrust(ctx, userHome)
	if err != nil {
		return helperCallError(err, socket)
	}
	if targets == nil {
		targets = []trust.Target{}
	}

	// Publish the dashboard host before the daemon starts so it can answer at
	// devstack.test immediately, then replace the block with the full set after
	// existing sites are discovered.
	if err := helper.SyncHosts(ctx, []string{"devstack.test"}); err != nil {
		return helperCallError(err, socket)
	}

	if err := ensureRunning(ctx, daemon.Options{Home: home, HelperSocket: socket}); err != nil {
		logPath := filepath.Join(home, "run", "daemon.log")
		return wrapError(err, "DAEMON_UNAVAILABLE", fmt.Sprintf("Inspect %s, then rerun `devstack init`.", logPath))
	}
	c, err := a.newClient(home)
	if err != nil {
		return wrapError(err, "CLIENT_UNAVAILABLE", "Rerun `devstack init` after fixing the daemon error.")
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return err
	}
	domains := dashboardDomains(sites)
	if err := helper.SyncHosts(ctx, domains); err != nil {
		return helperCallError(err, socket)
	}
	if cfg.ServingMode != config.ServingModeBuiltin {
		// The daemon reconciles the helper during startup; syncing once more
		// here surfaces a broken vhost set to init instead of leaving it hidden
		// behind an otherwise healthy daemon.
		if _, err := c.SyncVhosts(ctx); err != nil {
			return err
		}
	}
	report, err := c.Doctor(ctx)
	if err != nil {
		return err
	}
	if report.Checks == nil {
		report.Checks = []doctor.Check{}
	}

	fallback := fmt.Sprintf("http://127.0.0.1:%d", cfg.UIPort)
	result := initResult{
		CAFingerprint: defaultString(ca.Fingerprint, "unknown"),
		Trust:         targets,
		Hosts:         domains,
		Doctor:        report,
		DashboardURL:  dashboardURL,
		FallbackURL:   fallback,
	}

	if a.jsonOutput {
		if err := a.writeJSON(cmd, result); err != nil {
			return err
		}
	} else {
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Helper:     ready")
		fmt.Fprintf(out, "CA:         %s\n", result.CAFingerprint)
		fmt.Fprintf(out, "Trust:      %d target(s)\n", len(targets))
		fmt.Fprintf(out, "Hosts:      %d domain(s) synced\n", len(domains))
		fmt.Fprintln(out, "Daemon:     running")
		fmt.Fprintln(out)
		if err := a.printDoctor(cmd, report); err != nil {
			fmt.Fprintf(out, "\nDashboard:  %s\nFallback:   %s\n", dashboardURL, fallback)
			return err
		}
		fmt.Fprintf(out, "\nDashboard:  %s\nFallback:   %s\n", dashboardURL, fallback)
	}

	if !report.OK {
		return &Error{
			Code:        "DOCTOR_FAILED",
			Message:     "init completed with failing doctor checks",
			Remediation: "Review the doctor report, then run `devstack doctor --repair`.",
		}
	}
	return nil
}

// dashboardDomains returns the dashboard host plus every registered site and
// its optional www alias, sorted and deduplicated.
func dashboardDomains(sites []model.Site) []string {
	set := map[string]struct{}{"devstack.test": {}}
	for _, site := range sites {
		domain := strings.ToLower(strings.TrimSpace(site.Domain))
		if domain == "" {
			continue
		}
		set[domain] = struct{}{}
		if site.WWW {
			set["www."+domain] = struct{}{}
		}
	}
	domains := make([]string, 0, len(set))
	for domain := range set {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}
