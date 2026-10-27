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

const systemPHPBuildPath = "/usr/local/php"

// stubPHPSmoke replaces the PHP registration smoke test for tests that use
// fixture archives without real PHP binaries.
func stubPHPSmoke(t *testing.T) {
	t.Helper()
	old := phpSmoke
	phpSmoke = func(context.Context, string) error { return nil }
	t.Cleanup(func() { phpSmoke = old })
}

func TestSmokePHPBuildRealFPM(t *testing.T) {
	for _, rel := range []string{"bin/php", "sbin/php-fpm"} {
		info, err := os.Stat(filepath.Join(systemPHPBuildPath, rel))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			t.Skipf("%s/%s is not an executable PHP binary", systemPHPBuildPath, rel)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := smokePHPBuild(ctx, systemPHPBuildPath); err != nil {
		t.Fatal(err)
	}
}

func TestSmokePHPBuildRejectsBrokenBuild(t *testing.T) {
	t.Run("missing php", func(t *testing.T) {
		build := t.TempDir()
		writeSmokeScript(t, filepath.Join(build, "sbin"), "php-fpm", "exit 0")
		if err := smokePHPBuild(context.Background(), build); err == nil {
			t.Fatal("smoke accepted a build without bin/php")
		}
	})

	t.Run("missing php-fpm", func(t *testing.T) {
		build := t.TempDir()
		writeSmokeScript(t, filepath.Join(build, "bin"), "php", "exit 0")
		if err := smokePHPBuild(context.Background(), build); err == nil {
			t.Fatal("smoke accepted a build without sbin/php-fpm")
		}
	})

	t.Run("fpm exits before listening", func(t *testing.T) {
		build := t.TempDir()
		writeSmokeScript(t, filepath.Join(build, "bin"), "php", `echo "PHP 8.4.4 (cli)"`)
		writeSmokeScript(t, filepath.Join(build, "sbin"), "php-fpm", `
if [ "$1" = "-v" ]; then
  echo "PHP 8.4.4 (fpm-fcgi)"
  exit 0
fi
echo "fpm exploded" >&2
exit 1
`)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := smokePHPBuild(ctx, build)
		if err == nil {
			t.Fatal("smoke accepted an FPM master that exits before listening")
		}
		if !strings.Contains(err.Error(), "exited before listening") {
			t.Fatalf("smoke error = %v", err)
		}
	})
}

func TestPHPInstallRunsSmokeAndLeavesNoRuntimeOnFailure(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	srv := newAnyFixtureServer(t, archive)
	cat := catalogFor(t, phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r1-linux-x64.tar.gz", 1))
	m, _, home, _ := newManagerWithCatalogFor(t, cat, srv.Client())

	old := phpSmoke
	t.Cleanup(func() { phpSmoke = old })

	var smokePath string
	phpSmoke = func(_ context.Context, buildPath string) error {
		smokePath = buildPath
		// Smoke must run on the complete temporary tree, before the atomic
		// rename publishes it.
		if _, err := os.Stat(filepath.Join(buildPath, manifestName)); err != nil {
			t.Errorf("smoke ran before the manifest was written: %v", err)
		}
		if filepath.Base(buildPath) == "8.4.4-r1-linux-x64" {
			t.Errorf("smoke ran after the build was published at %s", buildPath)
		}
		return errors.New("fpm failed")
	}

	if _, err := m.Install(context.Background(), "php@8.4.4-r1"); err == nil ||
		!strings.Contains(err.Error(), "fpm failed") {
		t.Fatalf("Install error = %v, want the smoke failure", err)
	}
	if smokePath == "" {
		t.Fatal("phpSmoke was not called")
	}
	if _, err := os.Stat(smokePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("smoke temp directory %s was left behind: %v", smokePath, err)
	}
	builds, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 0 {
		t.Fatalf("runtime registered after smoke failure: %+v", builds)
	}
	target := filepath.Join(home, "runtimes", "php", "8.4.4-r1-linux-x64")
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target directory exists after smoke failure: %v", err)
	}
	assertNoPartialOrTempFiles(t, home)

	phpSmoke = func(context.Context, string) error { return nil }
	build, err := m.Install(context.Background(), "php@8.4.4-r1")
	if err != nil {
		t.Fatalf("Install with successful smoke: %v", err)
	}
	if build.ID != "php@8.4.4-r1" {
		t.Fatalf("build = %+v", build)
	}
	builds, err = m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 1 || builds[0].ID != "php@8.4.4-r1" {
		t.Fatalf("List = %+v", builds)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target directory missing after smoke success: %v", err)
	}
}

func writeSmokeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
