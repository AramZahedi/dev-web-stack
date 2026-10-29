//go:build !windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestNodeSiteStartsAndStopsWithManagedRuntime(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "agent")
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "runtimes", "node@24.1.0", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "runtimes", "node@24.1.0", "bin", "node"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentRuntime, err := NewRuntime(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}

	spec := agentproto.SiteSpec{
		ID:             "node-site",
		Kind:           "node",
		BackendPath:    project,
		Command:        "sleep 60",
		RuntimeVersion: "24",
	}
	prepared, err := agentRuntime.PrepareSite(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.HTTPPort <= 0 {
		t.Fatalf("prepared HTTP port = %d", prepared.HTTPPort)
	}
	started, err := agentRuntime.StartSite(context.Background(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.PID <= 0 {
		t.Fatalf("started PID = %d", started.PID)
	}
	status, err := agentRuntime.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" || status.PID != started.PID {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if err := agentRuntime.StopSite(context.Background(), spec.ID, true); err != nil {
		t.Fatal(err)
	}
	status, err = agentRuntime.SiteStatus(spec.ID)
	if err != nil || status.Status != "stopped" {
		t.Fatalf("stopped status = %+v, %v", status, err)
	}
}

func TestPrepareSiteRejectsPHPKindOnStaticRuntime(t *testing.T) {
	agentRuntime, err := NewRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = agentRuntime.PrepareSite(context.Background(), agentproto.SiteSpec{
		ID: "php-site", Kind: "php", BackendPath: t.TempDir(),
	})
	if err == nil {
		t.Fatal("PrepareSite accepted a PHP site")
	}
}

func TestNodeRuntimePathRejectsMissingBuild(t *testing.T) {
	agentRuntime, err := NewRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agentRuntime.mu.Lock()
	_, err = agentRuntime.nodeRuntimePathLocked("24")
	agentRuntime.mu.Unlock()
	if err == nil {
		t.Fatal("nodeRuntimePathLocked accepted a missing Node build")
	}
}

func TestNodeSiteUsesConfiguredVersionLine(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "agent")
	bin := filepath.Join(runtimeRoot, "runtimes", "node@24.22.1", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentRuntime, err := NewRuntime(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	agentRuntime.mu.Lock()
	got, err := agentRuntime.nodeRuntimePathLocked("24")
	agentRuntime.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(runtimeRoot, "runtimes", "node@24.22.1"); got != want {
		t.Fatalf("node runtime = %q, want %q", got, want)
	}
}
