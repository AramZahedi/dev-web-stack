package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestTerminalEnvironmentMatchesSiteShellContract(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	phpIni := filepath.Join(root, "sites", "site-1", "php.ini")
	if err := os.MkdirAll(filepath.Dir(phpIni), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(phpIni, []byte("memory_limit=256M\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "site-1",
		Kind:        "php",
		BackendPath: filepath.Join(root, "projects", "app"),
		PHPIniPath:  phpIni,
	}
	env, err := runtime.terminalEnvironment(spec, &terminalTicket{siteID: "site-1", siteName: "blog"})
	if err != nil {
		t.Fatal(err)
	}
	if env["DEVSTACK_SITE"] != "blog" {
		t.Fatalf("DEVSTACK_SITE = %q, want the site name", env["DEVSTACK_SITE"])
	}
	if env["DEVSTACK_PROJECT"] != spec.BackendPath {
		t.Fatalf("DEVSTACK_PROJECT = %q, want %q", env["DEVSTACK_PROJECT"], spec.BackendPath)
	}
	if env["DEVSTACK_HOME"] != root {
		t.Fatalf("DEVSTACK_HOME = %q, want %q", env["DEVSTACK_HOME"], root)
	}
	if env["PHPRC"] != phpIni {
		t.Fatalf("PHPRC = %q, want %q", env["PHPRC"], phpIni)
	}
	if env["TERM"] != "xterm-256color" {
		t.Fatalf("TERM = %q", env["TERM"])
	}

	fallback, err := runtime.terminalEnvironment(spec, &terminalTicket{siteID: "site-1"})
	if err != nil {
		t.Fatal(err)
	}
	if fallback["DEVSTACK_SITE"] != "site-1" {
		t.Fatalf("fallback DEVSTACK_SITE = %q, want the site id", fallback["DEVSTACK_SITE"])
	}

	// A missing generated ini must not leave PHPRC pointing at nothing.
	if err := os.Remove(phpIni); err != nil {
		t.Fatal(err)
	}
	missing, err := runtime.terminalEnvironment(spec, &terminalTicket{siteID: "site-1"})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := missing["PHPRC"]; ok {
		t.Fatalf("PHPRC = %q for a missing php.ini, want unset", value)
	}
}

// A terminal ticket carries the exact Node build the daemon resolved for the
// session. The agent must prefer it over a stale prepared site spec, and only
// fall back to the spec when the ticket does not name a version.
func TestTerminalTicketUsesDaemonResolvedNodeVersion(t *testing.T) {
	runtime, root, spec := newStaticTestRuntime(t)
	spec.RuntimeVersion = "22.11.0"
	for _, version := range []string{"22.11.0", "24.1.0"} {
		bin := filepath.Join(root, "runtimes", "node@"+version, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	ticketResponse, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{NodeVersion: "24.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	runtime.termMu.Lock()
	ticket := runtime.termTickets[ticketResponse.Ticket]
	runtime.termMu.Unlock()
	if ticket == nil {
		t.Fatal("issued ticket was not stored")
	}
	if ticket.nodeVersion != "24.1.0" {
		t.Fatalf("ticket node version = %q, want the daemon-resolved exact build", ticket.nodeVersion)
	}

	site, err := runtime.site(spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected := terminalSiteSpec(site.spec, ticket)
	got := runtime.sitePathEnv(selected, "/usr/bin:/bin")
	exactBin := filepath.Join(root, "runtimes", "node@24.1.0", "bin")
	if !strings.HasPrefix(got, exactBin+":") {
		t.Fatalf("terminal PATH = %q, want exact ticket Node bin %q first", got, exactBin)
	}
	if stale := filepath.Join(root, "runtimes", "node@22.11.0", "bin"); strings.Contains(got, stale) {
		t.Fatalf("terminal PATH = %q still contains the stale site Node bin %q", got, stale)
	}

	fallbackResponse, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{})
	if err != nil {
		t.Fatal(err)
	}
	runtime.termMu.Lock()
	fallbackTicket := runtime.termTickets[fallbackResponse.Ticket]
	runtime.termMu.Unlock()
	if fallbackTicket == nil {
		t.Fatal("fallback ticket was not stored")
	}
	selected = terminalSiteSpec(site.spec, fallbackTicket)
	got = runtime.sitePathEnv(selected, "/usr/bin:/bin")
	siteBin := filepath.Join(root, "runtimes", "node@22.11.0", "bin")
	if !strings.HasPrefix(got, siteBin+":") {
		t.Fatalf("terminal PATH = %q, want site RuntimeVersion bin %q first", got, siteBin)
	}
}
