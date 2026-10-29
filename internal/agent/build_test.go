package agent

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestStartFPMRejectsAStaleListenerWhenTheMasterExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX php-fpm fixture")
	}
	root := t.TempDir()
	build := filepath.Join(root, "build")
	if err := os.MkdirAll(filepath.Join(build, "sbin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "sbin", "php-fpm"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stale.Close() }()
	port := stale.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process, err := (&Runtime{}).startFPM(ctx, "site", "default",
		filepath.Join(root, "fpm.conf"), filepath.Join(root, "php.ini"), build, port)
	if err == nil {
		if process != nil {
			process.stop()
		}
		t.Fatal("startFPM accepted a dead master because another listener owned the port")
	}
}

func TestResolvePHPDocumentRootRejectsSymlinkedIntermediateDirectory(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	if err := os.MkdirAll(filepath.Join(project, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	spec := agentproto.SiteSpec{
		ID:           "php-linked",
		Kind:         "php",
		BackendPath:  project,
		DocumentRoot: "linked/sub",
	}
	if _, err := resolvePHPDocumentRoot(spec); err == nil {
		t.Fatal("resolvePHPDocumentRoot accepted a document root below a symlinked directory")
	}

	spec.DocumentRoot = "public"
	resolved, err := resolvePHPDocumentRoot(spec)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(project, "public") {
		t.Fatalf("resolved document root = %q", resolved)
	}
}

func TestResolvePHPDocumentRootRequiresAbsoluteBackendPath(t *testing.T) {
	spec := agentproto.SiteSpec{ID: "php-relative", Kind: "php", BackendPath: "relative/project"}
	if _, err := resolvePHPDocumentRoot(spec); err == nil {
		t.Fatal("resolvePHPDocumentRoot accepted a relative backend path")
	}
}

func TestResolvePHPDocumentRootAllowsSubdirectoryNamedWithDots(t *testing.T) {
	project := t.TempDir()
	// A directory whose name starts with ".." is inside the project; the old
	// strings.HasPrefix(rel, "..") check rejected it.
	if err := os.MkdirAll(filepath.Join(project, "..cache"), 0o755); err != nil {
		t.Skipf("cannot create a dot-prefixed directory: %v", err)
	}
	spec := agentproto.SiteSpec{ID: "php-dots", Kind: "php", BackendPath: project, DocumentRoot: "..cache"}
	resolved, err := resolvePHPDocumentRoot(spec)
	if err != nil {
		t.Fatalf("resolvePHPDocumentRoot rejected a contained directory: %v", err)
	}
	if resolved != filepath.Join(project, "..cache") {
		t.Fatalf("resolved document root = %q", resolved)
	}
}

func TestCappedBufferBoundsCapturedOutput(t *testing.T) {
	buffer := newCappedBuffer(8)
	if n, err := buffer.Write([]byte("0123456789")); n != 10 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if buffer.String() != "01234567\n[devstack: output truncated]\n" {
		t.Fatalf("String = %q", buffer.String())
	}
	if n, err := buffer.Write([]byte("more")); n != 4 || err != nil {
		t.Fatalf("second Write = %d, %v", n, err)
	}
}

func TestCommandTimeoutClampsToCeiling(t *testing.T) {
	if got := commandTimeout(0, 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("fallback timeout = %v", got)
	}
	if got := commandTimeout(1000, time.Minute); got != time.Second {
		t.Fatalf("requested timeout = %v", got)
	}
	if got := commandTimeout(int((48 * time.Hour).Milliseconds()), time.Minute); got != maxCommandTimeout {
		t.Fatalf("unclamped timeout = %v", got)
	}
}

func TestExecSiteTruncatesRunawayOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	runtimeRoot := t.TempDir()
	agentRuntime, err := NewRuntime(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(runtimeRoot, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{ID: "exec-site", Kind: "static", BackendPath: project, DocumentRoot: "."}
	if _, err := agentRuntime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	response, err := agentRuntime.ExecSite(context.Background(), spec.ID, agentproto.SiteExecRequest{
		Argv: []string{"/bin/sh", "-c", "head -c 20000000 /dev/zero"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Stdout) > maxCapturedOutput+128 {
		t.Fatalf("captured stdout is unbounded: %d bytes", len(response.Stdout))
	}
	if !strings.Contains(response.Stdout, "output truncated") {
		t.Fatal("truncated output is not marked")
	}
}
