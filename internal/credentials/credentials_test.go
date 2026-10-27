package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var secretPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	return &Store{Root: root, Now: func() time.Time {
		return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	}}, root
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}

func TestEnsureCreatesSecureSecrets(t *testing.T) {
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "mysql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	values, err := store.Ensure("mysql", dataDir, "root_password", "devstack_password")
	if err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("values = %v, want two secrets", values)
	}
	if values["root_password"] == values["devstack_password"] {
		t.Fatal("secrets share the same value")
	}
	for name, value := range values {
		if !secretPattern.MatchString(value) {
			t.Errorf("%s = %q, want 64 lowercase hex characters", name, value)
		}
	}

	requireMode(t, filepath.Join(root, "secrets"), 0o700)
	requireMode(t, filepath.Join(root, "secrets", "mysql"), 0o700)
	requireMode(t, filepath.Join(root, "secrets", "mysql", "root_password"), 0o600)
	requireMode(t, filepath.Join(root, "secrets", "mysql", "devstack_password"), 0o600)

	again, err := store.Ensure("mysql", dataDir, "root_password", "devstack_password")
	if err != nil {
		t.Fatalf("second Ensure() = %v", err)
	}
	for name, value := range values {
		if again[name] != value {
			t.Errorf("second Ensure() %s = %q, want stable %q", name, again[name], value)
		}
	}

	read, err := store.Read("mysql", "root_password")
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if read != values["root_password"] {
		t.Errorf("Read() = %q, want %q", read, values["root_password"])
	}
}

func TestEnsureUsesIndependentSecretsPerEngine(t *testing.T) {
	store, root := testStore(t)
	mysqlData := filepath.Join(root, "services", "mysql", "data")
	mariadbData := filepath.Join(root, "services", "mariadb", "data")
	for _, dir := range []string{mysqlData, mariadbData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mysql, err := store.Ensure("mysql", mysqlData, "root_password")
	if err != nil {
		t.Fatal(err)
	}
	mariadb, err := store.Ensure("mariadb", mariadbData, "root_password")
	if err != nil {
		t.Fatal(err)
	}
	if mysql["root_password"] == mariadb["root_password"] {
		t.Fatal("MySQL and MariaDB share a root password")
	}
	if got := store.Path("mysql", "root_password"); !strings.HasSuffix(got, filepath.Join("secrets", "mysql", "root_password")) {
		t.Errorf("Path() = %q", got)
	}
}

func TestEnsureRejectsDataWithoutSecret(t *testing.T) {
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "mysql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "ibdata1"), []byte("initialized"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Ensure("mysql", dataDir, "root_password"); !errors.Is(err, ErrDataWithoutSecret) {
		t.Fatalf("Ensure() error = %v, want ErrDataWithoutSecret", err)
	}
	if _, err := os.Stat(store.Path("mysql", "root_password")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret file exists despite inconsistency: %v", err)
	}
}

func TestEnsureRejectsSecretWithoutData(t *testing.T) {
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "mysql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	values, err := store.Ensure("mysql", dataDir, "root_password")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatal(err)
	}

	_, err = store.Ensure("mysql", dataDir, "root_password")
	if !errors.Is(err, ErrSecretWithoutData) {
		t.Fatalf("Ensure() error = %v, want ErrSecretWithoutData", err)
	}
	if strings.Contains(err.Error(), values["root_password"]) {
		t.Fatal("error message leaks the secret value")
	}
}

func TestEnsureAllowsEmptyDataDirectoryAfterInstall(t *testing.T) {
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "mysql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := store.Ensure("mysql", dataDir, "root_password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Ensure("mysql", dataDir, "root_password")
	if err != nil {
		t.Fatalf("second Ensure() on empty data directory = %v", err)
	}
	if second["root_password"] != first["root_password"] {
		t.Fatal("secret changed between install and activation")
	}
}

func TestEnsureTreatsUnreadableDataDirectoryAsInitialized(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("root or Windows can read a mode-000 directory")
	}
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "postgresql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o755) })

	if _, err := store.Ensure("postgresql", dataDir, "postgres_password"); !errors.Is(err, ErrDataWithoutSecret) {
		t.Fatalf("Ensure() error = %v, want ErrDataWithoutSecret", err)
	}
}

func TestEnsureGeneratesOnlyMissingSecretsForEmptyData(t *testing.T) {
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "mysql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := store.Ensure("mysql", dataDir, "root_password")
	if err != nil {
		t.Fatal(err)
	}
	values, err := store.Ensure("mysql", dataDir, "root_password", "devstack_password")
	if err != nil {
		t.Fatal(err)
	}
	if values["root_password"] != first["root_password"] {
		t.Fatal("existing secret was regenerated")
	}
	if !secretPattern.MatchString(values["devstack_password"]) {
		t.Fatalf("devstack_password = %q", values["devstack_password"])
	}
}

func TestWriteRefusesOverwrite(t *testing.T) {
	store, _ := testStore(t)
	first, err := store.Write("mysql", "root_password")
	if err != nil {
		t.Fatalf("Write() = %v", err)
	}
	second, err := store.Write("mysql", "root_password")
	if !errors.Is(err, ErrSecretExists) {
		t.Fatalf("second Write() error = %v, want ErrSecretExists", err)
	}
	if second != "" {
		t.Fatalf("second Write() returned %q, want empty", second)
	}
	read, err := store.Read("mysql", "root_password")
	if err != nil {
		t.Fatal(err)
	}
	if read != first {
		t.Fatalf("existing secret was overwritten: %q != %q", read, first)
	}
}

func TestReadErrors(t *testing.T) {
	store, root := testStore(t)
	if _, err := store.Read("mysql", "missing"); !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("Read() missing error = %v, want ErrSecretMissing", err)
	}

	dir := filepath.Join(root, "secrets", "mysql")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty"), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("mysql", "empty"); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("Read() empty error = %v, want ErrEmptySecret", err)
	}
}

func TestInvalidNamesAreRejected(t *testing.T) {
	store, root := testStore(t)
	dataDir := filepath.Join(root, "services", "mysql", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		engine string
		name   string
	}{
		{"../mysql", "root_password"},
		{"mysql", "../root_password"},
		{"mysql", "root/password"},
		{"", "root_password"},
		{"mysql", ""},
	}
	for _, tc := range cases {
		if _, err := store.Ensure(tc.engine, dataDir, tc.name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Ensure(%q, %q) error = %v, want ErrInvalidName", tc.engine, tc.name, err)
		}
		if _, err := store.Write(tc.engine, tc.name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Write(%q, %q) error = %v, want ErrInvalidName", tc.engine, tc.name, err)
		}
		if _, err := store.Read(tc.engine, tc.name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Read(%q, %q) error = %v, want ErrInvalidName", tc.engine, tc.name, err)
		}
	}
}

func TestReadValidatesStoreRoot(t *testing.T) {
	store := &Store{}
	if _, err := store.Read("mysql", "root_password"); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("Read() with empty root = %v, want ErrInvalidStore", err)
	}
}

func TestReadRejectsSymlinkedSecret(t *testing.T) {
	store, root := testStore(t)
	engineDir := filepath.Join(root, "secrets", "mysql")
	if err := os.MkdirAll(engineDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("not-a-devstack-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(engineDir, "root_password")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("mysql", "root_password"); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("Read() symlink error = %v, want ErrInvalidSecret", err)
	}
}

func TestSecretPathRejectsSymlinkedEngineDirectory(t *testing.T) {
	store, root := testStore(t)
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "root_password"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "secrets", "mysql")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("mysql", "root_password"); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("Read() engine symlink error = %v, want ErrInvalidSecret", err)
	}
	if _, err := store.Write("mysql", "devstack_password"); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("Write() engine symlink error = %v, want ErrInvalidSecret", err)
	}
}

func TestRotateReplacesSecret(t *testing.T) {
	store, _ := testStore(t)
	first, err := store.Write("postgresql", "postgres_password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Rotate("postgresql", "postgres_password")
	if err != nil {
		t.Fatalf("Rotate() = %v", err)
	}
	if second == first {
		t.Fatal("Rotate() returned the previous value")
	}
	if !secretPattern.MatchString(second) {
		t.Fatalf("rotated value = %q", second)
	}
	read, err := store.Read("postgresql", "postgres_password")
	if err != nil {
		t.Fatal(err)
	}
	if read != second {
		t.Fatalf("Read() after Rotate() = %q, want %q", read, second)
	}
	requireMode(t, store.Path("postgresql", "postgres_password"), 0o600)
}

func TestEnsureNoSecretNamesCreatesSecureDirectory(t *testing.T) {
	store, root := testStore(t)
	values, err := store.Ensure("redis", filepath.Join(root, "services", "redis", "data"))
	if err != nil {
		t.Fatalf("Ensure() = %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("values = %v, want none", values)
	}
	requireMode(t, filepath.Join(root, "secrets", "redis"), 0o700)
}
