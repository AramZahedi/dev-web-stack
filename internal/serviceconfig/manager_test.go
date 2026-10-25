package serviceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedBaseWritesMissingFile(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/nginx.conf"
	data := []byte("# base nginx config\n")
	written, err := m.SeedBase(rel, data, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected SeedBase to report written=true")
	}

	st, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if st.Class != ClassBase || st.Modified || st.Detached {
		t.Fatalf("unexpected status: %+v", st)
	}

	// Read from disk
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(data) {
		t.Fatalf("file content = %q, want %q", string(content), string(data))
	}
}

func TestSeedBaseDoesNotOverwriteExistingFile(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/nginx.conf"
	data1 := []byte("# base nginx config\n")
	if _, err := m.SeedBase(rel, data1, 0o644); err != nil {
		t.Fatal(err)
	}

	data2 := []byte("# modified by someone\n")
	written, err := m.SeedBase(rel, data2, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("expected SeedBase to not overwrite existing file")
	}

	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(data1) {
		t.Fatalf("content was overwritten: %q", string(content))
	}
}

func TestWriteGeneratedReplacesUnmodifiedFile(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/conf.d/site.conf"
	gen1 := []byte("server { listen 80; }\n")
	res1, err := m.WriteGenerated(rel, gen1, 0o644, "initial sync")
	if err != nil {
		t.Fatal(err)
	}
	if !res1.Changed || res1.Conflict != nil {
		t.Fatalf("unexpected result: %+v", res1)
	}

	gen2 := []byte("server { listen 80; server_name site.test; }\n")
	res2, err := m.WriteGenerated(rel, gen2, 0o644, "update domain")
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Changed || res2.Conflict != nil {
		t.Fatalf("unexpected result: %+v", res2)
	}

	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(gen2) {
		t.Fatalf("expected content %q, got %q", string(gen2), string(content))
	}
}

func TestWriteGeneratedDetectsConflictOnModification(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/conf.d/site.conf"
	gen1 := []byte("server { listen 80; }\n")
	if _, err := m.WriteGenerated(rel, gen1, 0o644, "initial"); err != nil {
		t.Fatal(err)
	}

	// Directly modify file on disk
	fullPath := filepath.Join(root, filepath.FromSlash(rel))
	userMod := []byte("server { listen 80; # user comment\n}\n")
	if err := os.WriteFile(fullPath, userMod, 0o644); err != nil {
		t.Fatal(err)
	}

	// Try writing proposed new generated content
	gen2 := []byte("server { listen 8080; }\n")
	res, err := m.WriteGenerated(rel, gen2, 0o644, "port change")
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict == nil {
		t.Fatal("expected conflict, got nil")
	}
	if !strings.Contains(res.Conflict.Diff, "-server { listen 80; # user comment") {
		t.Fatalf("conflict diff missing user comment: %s", res.Conflict.Diff)
	}

	// Ensure file was NOT touched
	content, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(userMod) {
		t.Fatalf("file content was modified during conflict: %q", string(content))
	}
}

func TestKeepMineDetachesFile(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/conf.d/site.conf"
	gen1 := []byte("server { listen 80; }\n")
	if _, err := m.WriteGenerated(rel, gen1, 0o644, "initial"); err != nil {
		t.Fatal(err)
	}

	if err := m.KeepMine(rel); err != nil {
		t.Fatal(err)
	}

	st, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Detached {
		t.Fatal("expected status to be detached")
	}

	// Subsequent generated write is ignored (no change, no conflict)
	gen2 := []byte("server { listen 8080; }\n")
	res, err := m.WriteGenerated(rel, gen2, 0o644, "update")
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Conflict != nil {
		t.Fatalf("expected no change and no conflict for detached file, got %+v", res)
	}
}

func TestUseGeneratedCreatesBackupAndOverwrites(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/conf.d/site.conf"
	gen1 := []byte("server { listen 80; }\n")
	if _, err := m.WriteGenerated(rel, gen1, 0o644, "initial"); err != nil {
		t.Fatal(err)
	}

	// User modification
	fullPath := filepath.Join(root, filepath.FromSlash(rel))
	userMod := []byte("server { listen 80; # user edit\n}\n")
	if err := os.WriteFile(fullPath, userMod, 0o644); err != nil {
		t.Fatal(err)
	}

	gen2 := []byte("server { listen 9000; }\n")
	backupPath, err := m.UseGenerated(rel, gen2)
	if err != nil {
		t.Fatal(err)
	}
	if backupPath == "" {
		t.Fatal("expected non-empty backup path")
	}

	// Verify backup content
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backupBytes) != string(userMod) {
		t.Fatalf("backup content mismatch: got %q, want %q", string(backupBytes), string(userMod))
	}

	// Verify file is updated
	content, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(gen2) {
		t.Fatalf("file content = %q, want %q", string(content), string(gen2))
	}

	// Status should be clean
	st, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if st.Modified || st.Detached {
		t.Fatalf("unexpected status after UseGenerated: %+v", st)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, invalid := range []string{"../escape.conf", "/etc/passwd", "foo/../../bar"} {
		if _, err := m.SeedBase(invalid, []byte("x"), 0o644); err == nil {
			t.Errorf("SeedBase allowed traversal path %q", invalid)
		}
		if _, err := m.WriteGenerated(invalid, []byte("x"), 0o644, "test"); err == nil {
			t.Errorf("WriteGenerated allowed traversal path %q", invalid)
		}
	}
}

func TestAtomicWritesPreserveMode(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "services/nginx/config/conf.d/site.conf"
	data := []byte("server { listen 80; }\n")
	if _, err := m.WriteGenerated(rel, data, 0o600, "initial"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}
