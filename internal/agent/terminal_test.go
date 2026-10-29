package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestSitePathEnvPrependsRuntimeShims(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	phpRoot := filepath.Join(root, "runtimes", "php@8.4.0")
	nodeRoot := filepath.Join(root, "runtimes", "node@24.1.0")
	writeShim := func(path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeShim(filepath.Join(phpRoot, "bin", "php"))
	writeShim(filepath.Join(nodeRoot, "bin", "node"))
	runtime.runtimes["node@24.1.0"] = agentproto.RuntimeResponse{
		ID: "node@24.1.0", Kind: "node", Version: "24.1.0", Path: nodeRoot,
	}

	got := runtime.sitePathEnv(agentproto.SiteSpec{
		PHPBuild:       "php@8.4.0",
		RuntimeVersion: "24",
	}, "/usr/bin:/bin")
	want := filepath.Join(nodeRoot, "bin") + ":" +
		filepath.Join(phpRoot, "bin") + ":/usr/bin:/bin"
	if got != want {
		t.Fatalf("sitePathEnv = %q, want %q", got, want)
	}

	// A site without a runtime still gets the installed global PHP shim.
	writeShim(filepath.Join(root, "tools", "php-default", "bin", "php"))
	got = runtime.sitePathEnv(agentproto.SiteSpec{}, "/usr/bin")
	want = filepath.Join(root, "tools", "php-default", "bin") + ":/usr/bin"
	if got != want {
		t.Fatalf("global sitePathEnv = %q, want %q", got, want)
	}
}

func TestIssueTerminalTicketClampsDimensionsAndPrunesExpired(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ticket, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{Cols: 100000, Rows: -4})
	if err != nil {
		t.Fatal(err)
	}
	runtime.termMu.Lock()
	entry := runtime.termTickets[ticket.Ticket]
	runtime.termMu.Unlock()
	if entry == nil {
		t.Fatal("issued ticket was not stored")
	}
	if entry.cols != maxTerminalDimension || entry.rows != 24 {
		t.Fatalf("ticket dimensions = %dx%d, want %dx24", entry.cols, entry.rows, maxTerminalDimension)
	}

	runtime.termMu.Lock()
	runtime.termTickets["expired-ticket"] = &terminalTicket{
		token:     "expired-ticket",
		siteID:    spec.ID,
		expiresAt: time.Now().Add(-time.Second),
	}
	runtime.termMu.Unlock()
	if _, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{}); err != nil {
		t.Fatal(err)
	}
	runtime.termMu.Lock()
	_, retained := runtime.termTickets["expired-ticket"]
	runtime.termMu.Unlock()
	if retained {
		t.Fatal("expired ticket was not pruned when a new ticket was issued")
	}
}

// A broken CA bundle makes Runtime.Environment return an error. The terminal
// handler used to ignore that error and assign into a nil map, panicking after
// the connection had already been hijacked.
func TestTerminalEnvironmentFailureClosesInsteadOfPanicking(t *testing.T) {
	runtime, root, spec := newStaticTestRuntime(t)
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	// A directory where the CA bundle file belongs is enough to make the
	// environment lookup fail without removing the path.
	if err := os.MkdirAll(filepath.Join(root, "certs", "ca-bundle.crt"), 0o755); err != nil {
		t.Fatal(err)
	}

	var panicked atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/terminal", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked.Store(true)
			}
		}()
		runtime.HandleTerminalWebSocket(w, r, spec.ID)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	ticket, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/terminal?ticket=" + url.QueryEscape(ticket.Ticket)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		if panicked.Load() {
			t.Fatal("terminal handler panicked")
		}
		return
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("terminal stayed open with an unavailable environment")
	}
	if panicked.Load() {
		t.Fatal("terminal handler panicked on an environment error")
	}
}

func TestTerminalConsumesTicketOnlyOnce(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ticket, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.consumeTerminalTicket(ticket.Ticket, spec.ID); !ok {
		t.Fatal("first consume rejected a fresh ticket")
	}
	if _, ok := runtime.consumeTerminalTicket(ticket.Ticket, spec.ID); ok {
		t.Fatal("ticket was accepted twice")
	}
	if _, ok := runtime.consumeTerminalTicket(ticket.Ticket, "another-site"); ok {
		t.Fatal("ticket was accepted for a different site")
	}
}
