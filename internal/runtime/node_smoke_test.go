//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
