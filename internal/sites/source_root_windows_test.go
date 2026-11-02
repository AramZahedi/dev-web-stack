//go:build windows

package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestEnsureSourceRootWindows(t *testing.T) {
	dir := t.TempDir()
	if err := ensureSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: dir}); err != nil {
		t.Fatalf("existing directory: %v", err)
	}

	missing := filepath.Join(dir, "nested", "app")
	if err := ensureSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: missing, Kind: model.SiteStatic}); err != nil {
		t.Fatalf("missing directory was not created: %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("created directory = %v, %v", info, err)
	}

	file := filepath.Join(dir, "file.txt")
	if writeErr := os.WriteFile(file, []byte("x"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	err := ensureSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: file})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file path error = %v", err)
	}

	untouched := filepath.Join(dir, "local-missing")
	if err := ensureSourceRoot(model.Site{SourceMode: model.SourceLocal, HostPath: untouched}); err != nil {
		t.Fatalf("local mode should be validated elsewhere: %v", err)
	}
	if _, err := os.Stat(untouched); !os.IsNotExist(err) {
		t.Fatalf("local mode created a directory: %v", err)
	}
}

func TestEnsureSourceRootCreatesPHPDocumentRoot(t *testing.T) {
	t.Run("missing project and document root", func(t *testing.T) {
		project := filepath.Join(t.TempDir(), "apps", "laravel")
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "public",
		}
		if err := ensureSourceRoot(site); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{project, filepath.Join(project, "public")} {
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Fatalf("%s = %v, %v", path, info, err)
			}
		}
	})

	t.Run("existing project with missing document root", func(t *testing.T) {
		project := t.TempDir()
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "public",
		}
		if err := ensureSourceRoot(site); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(project, "public")); err != nil || !info.IsDir() {
			t.Fatalf("document root = %v, %v", info, err)
		}
	})

	t.Run("nested document root", func(t *testing.T) {
		project := t.TempDir()
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "storage/app/public",
		}
		if err := ensureSourceRoot(site); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(project, "storage", "app", "public")); err != nil || !info.IsDir() {
			t.Fatalf("nested document root = %v, %v", info, err)
		}
	})

	t.Run("existing document root", func(t *testing.T) {
		project := t.TempDir()
		if err := os.MkdirAll(filepath.Join(project, "public"), 0o755); err != nil {
			t.Fatal(err)
		}
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "public",
		}
		if err := ensureSourceRoot(site); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("document root collides with a file", func(t *testing.T) {
		project := t.TempDir()
		if err := os.WriteFile(filepath.Join(project, "public"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "public",
		}
		err := ensureSourceRoot(site)
		if err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("file collision error = %v", err)
		}
	})

	for _, tc := range []struct {
		name         string
		documentRoot string
		target       func(project string) string
	}{
		{name: "absolute", documentRoot: filepath.Join(t.TempDir(), "outside"), target: func(string) string { return "" }},
		{name: "leading slash", documentRoot: "/public", target: func(string) string { return "" }},
		{name: "leading backslash", documentRoot: `\public`, target: func(string) string { return "" }},
		{name: "parent escape", documentRoot: "../escape", target: func(project string) string {
			return filepath.Join(filepath.Dir(project), "escape")
		}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			project := t.TempDir()
			site := model.Site{
				SourceMode:   model.SourceWindows,
				HostPath:     project,
				Kind:         model.SitePHP,
				DocumentRoot: tc.documentRoot,
			}
			if err := ensureSourceRoot(site); err == nil {
				t.Fatalf("document root %q was accepted", tc.documentRoot)
			}
			if escaped := tc.target(project); escaped != "" {
				if _, statErr := os.Stat(escaped); !os.IsNotExist(statErr) {
					t.Fatalf("unsafe document root escaped the project: %v", statErr)
				}
			}
			if _, statErr := os.Stat(filepath.Join(project, "public")); !os.IsNotExist(statErr) {
				t.Fatalf("unsafe document root created a directory: %v", statErr)
			}
		})
	}

	t.Run("non-PHP site keeps a missing document root", func(t *testing.T) {
		project := t.TempDir()
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SiteStatic,
			DocumentRoot: "public",
		}
		if err := ensureSourceRoot(site); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(project, "public")); !os.IsNotExist(err) {
			t.Fatalf("static site created a document root: %v", err)
		}
	})

	t.Run("symlinked document root is rejected", func(t *testing.T) {
		project := t.TempDir()
		real := filepath.Join(project, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(project, "public")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "public",
		}
		err := ensureSourceRoot(site)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlinked document root error = %v", err)
		}
	})

	t.Run("symlinked project root is rejected", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "project")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		site := model.Site{
			SourceMode: model.SourceWindows,
			HostPath:   link,
			Kind:       model.SiteStatic,
		}
		err := ensureSourceRoot(site)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlinked project root error = %v", err)
		}
	})

	t.Run("node site keeps a symlinked project root", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "project")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		site := model.Site{
			SourceMode: model.SourceWindows,
			HostPath:   link,
			Kind:       model.SiteNode,
		}
		if err := ensureSourceRoot(site); err != nil {
			t.Fatalf("node site rejected a symlinked project root: %v", err)
		}
	})

	t.Run("document root escaping through a symlink is rejected", func(t *testing.T) {
		base := t.TempDir()
		project := filepath.Join(base, "project")
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(base, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(project, "public")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		site := model.Site{
			SourceMode:   model.SourceWindows,
			HostPath:     project,
			Kind:         model.SitePHP,
			DocumentRoot: "public",
		}
		if err := ensureSourceRoot(site); err == nil {
			t.Fatal("escaping document root was accepted")
		}
	})
}
