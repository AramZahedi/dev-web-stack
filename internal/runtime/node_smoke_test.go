//go:build !windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNodeSmokeAcceptsMatchingVersion(t *testing.T) {
	buildRoot := newFakeNodeBuild(t, "v"+testVersion)
	if err := nodeSmoke(buildRoot, testVersion); err != nil {
		t.Fatalf("nodeSmoke: %v", err)
	}
}

func TestNodeSmokeRejectsVersionMismatch(t *testing.T) {
	buildRoot := newFakeNodeBuild(t, "v24.99.1")
	err := nodeSmoke(buildRoot, testVersion)
	if err == nil {
		t.Fatal("nodeSmoke accepted a node binary reporting a different version")
	}
	for _, want := range []string{"24.99.1", testVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("nodeSmoke error = %v, want it to mention %q", err, want)
		}
	}
}

func TestNodeSmokeIgnoresStderr(t *testing.T) {
	buildRoot := t.TempDir()
	writeNodeFile(t, filepath.Join(buildRoot, "bin"), "node",
		"#!/bin/sh\nprintf 'host warning\\n' >&2\nprintf 'v"+testVersion+"\\n'\n")
	if err := nodeSmoke(buildRoot, testVersion); err != nil {
		t.Fatalf("nodeSmoke rejected a matching version because of stderr: %v", err)
	}
}

func TestNodeSmokeSanitizesHostNodeConfig(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require /definitely/missing")
	t.Setenv("NODE_PATH", "/host/node_modules")
	buildRoot := t.TempDir()
	writeNodeFile(t, filepath.Join(buildRoot, "bin"), "node",
		"#!/bin/sh\nif [ -n \"$NODE_OPTIONS\" ] || [ -n \"$NODE_PATH\" ]; then\n"+
			"  printf 'host node config leaked\\n' >&2\n  exit 1\nfi\n"+
			"printf 'v"+testVersion+"\\n'\n")
	if err := nodeSmoke(buildRoot, testVersion); err != nil {
		t.Fatalf("nodeSmoke did not sanitize host node config: %v", err)
	}
}

func TestNodeSmokeContextHonorsCancellation(t *testing.T) {
	buildRoot := newFakeNodeBuild(t, "v"+testVersion)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := nodeSmokeContext(ctx, buildRoot, testVersion); !errors.Is(err, context.Canceled) {
		t.Fatalf("nodeSmokeContext error = %v, want context.Canceled", err)
	}
}

func TestNodeSmokeContextStopsRunningNode(t *testing.T) {
	buildRoot := t.TempDir()
	writeNodeFile(t, filepath.Join(buildRoot, "bin"), "node",
		"#!/bin/sh\nprintf 'v"+testVersion+"\\n'\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := nodeSmokeContext(ctx, buildRoot, testVersion)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("nodeSmokeContext error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("nodeSmokeContext ignored cancellation for %s", elapsed)
	}
}

func TestNodeSmokeReportsStderrOnFailure(t *testing.T) {
	buildRoot := t.TempDir()
	writeNodeFile(t, filepath.Join(buildRoot, "bin"), "node",
		"#!/bin/sh\nprintf 'dynamic loader exploded\\n' >&2\nexit 7\n")
	err := nodeSmoke(buildRoot, testVersion)
	if err == nil || !strings.Contains(err.Error(), "dynamic loader exploded") {
		t.Fatalf("nodeSmoke error = %v, want the captured stderr", err)
	}
}

func TestNodeSmokeRequiresPackagedNPM(t *testing.T) {
	t.Run("npm absent is allowed", func(t *testing.T) {
		buildRoot := newFakeNodeBuild(t, "v"+testVersion)
		if err := nodeSmoke(buildRoot, testVersion); err != nil {
			t.Fatalf("nodeSmoke: %v", err)
		}
	})

	t.Run("packaged npm without bin/npm", func(t *testing.T) {
		buildRoot := newFakeNodeBuild(t, "v"+testVersion)
		writeNodeFile(t, filepath.Join(buildRoot, "lib", "node_modules", "npm"), "package.json", "{}")
		err := nodeSmoke(buildRoot, testVersion)
		if err == nil || !strings.Contains(err.Error(), "bin/npm") {
			t.Fatalf("nodeSmoke error = %v, want a missing bin/npm error", err)
		}
	})

	t.Run("dangling bin/npm", func(t *testing.T) {
		buildRoot := newFakeNodeBuild(t, "v"+testVersion)
		link := filepath.Join(buildRoot, "bin", "npm")
		if err := os.Symlink("../lib/node_modules/npm/bin/npm-cli.js", link); err != nil {
			t.Fatalf("symlink bin/npm: %v", err)
		}
		err := nodeSmoke(buildRoot, testVersion)
		if err == nil || !strings.Contains(err.Error(), "bin/npm") {
			t.Fatalf("nodeSmoke error = %v, want a bin/npm error", err)
		}
	})

	t.Run("packaged npm shim resolves", func(t *testing.T) {
		buildRoot := newFakeNodeBuild(t, "v"+testVersion)
		writeNodeFile(t, filepath.Join(buildRoot, "lib", "node_modules", "npm"), "package.json", "{}")
		writeNodeFile(t, filepath.Join(buildRoot, "lib", "node_modules", "npm", "bin"), "npm-cli.js", "#!/bin/sh\nexit 0\n")
		link := filepath.Join(buildRoot, "bin", "npm")
		if err := os.Symlink("../lib/node_modules/npm/bin/npm-cli.js", link); err != nil {
			t.Fatalf("symlink bin/npm: %v", err)
		}
		if err := nodeSmoke(buildRoot, testVersion); err != nil {
			t.Fatalf("nodeSmoke: %v", err)
		}
	})
}

// newFakeNodeBuild writes an executable bin/node that prints version.
func newFakeNodeBuild(t *testing.T, version string) string {
	t.Helper()
	buildRoot := t.TempDir()
	writeNodeFile(t, filepath.Join(buildRoot, "bin"), "node", "#!/bin/sh\nprintf '"+version+"\\n'\n")
	return buildRoot
}

func writeNodeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	// Set the mode explicitly so a restrictive umask cannot make a fixture
	// non-executable.
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}
