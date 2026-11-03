package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestMigrateSiteFoldersRenamesLegacyPaths(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		filepath.Join(root, "sites", "legacy", "fpm.conf"): "fpm",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := migrateSiteFolders(root, agentproto.SiteSpec{ID: "legacy", Name: "blog"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for oldPath := range files {
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatalf("legacy path %s remains: %v", oldPath, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "sites", "blog", "fpm.conf")); err != nil {
		t.Fatalf("migrated path: %v", err)
	}
}

func TestMigrateSiteFoldersNeverOverwritesNamePaths(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(root, "sites", "legacy", "marker"): "legacy",
		filepath.Join(root, "sites", "blog", "marker"):   "named",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateSiteFolders(root, agentproto.SiteSpec{ID: "legacy", Name: "blog"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "sites", "blog", "marker")); err != nil || string(data) != "named" {
		t.Fatalf("target marker = %q, %v", data, err)
	}
}

func TestArchiveOrphanSiteFolders(t *testing.T) {
	root := t.TempDir()
	legacy := "0123456789abcdef0123456789abcdef"
	for _, name := range []string{legacy, "laravel-new"} {
		if err := os.MkdirAll(filepath.Join(root, "sites", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := archiveOrphanSiteFolders(root, map[string]struct{}{"laravel-new": {}}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sites", "laravel-new")); err != nil {
		t.Fatalf("registered folder moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sites", "_orphaned", legacy)); err != nil {
		t.Fatalf("orphan not archived: %v", err)
	}
}

func TestSiteFolderFallsBackToID(t *testing.T) {
	if got := siteFolder(agentproto.SiteSpec{ID: "opaque"}); got != "opaque" {
		t.Fatalf("siteFolder without name = %q", got)
	}
	if got := siteFolder(agentproto.SiteSpec{ID: "opaque", Name: "blog"}); got != "blog" {
		t.Fatalf("siteFolder = %q, want blog", got)
	}
	if got := siteFolder(agentproto.SiteSpec{ID: "opaque", Name: "../escape"}); got != "opaque" {
		t.Fatalf("unsafe siteFolder = %q, want fallback", got)
	}
}
