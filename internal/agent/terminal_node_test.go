package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

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
