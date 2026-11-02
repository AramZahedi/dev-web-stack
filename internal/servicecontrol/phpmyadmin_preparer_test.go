package servicecontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestPhpMyAdminPreparerSeedsUserConfig(t *testing.T) {
	root := t.TempDir()
	prep := NewPhpMyAdminPreparer(root)
	svc, ok := services.DefaultCatalog().Get(services.IDPHPMyAdmin)
	if !ok {
		t.Fatal("catalog missing phpmyadmin")
	}

	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatalf("PrepareInstall() = %v", err)
	}
	path := filepath.Join(root, "services", "phpmyadmin", "config", "config.user.inc.php")
	content := readTestFile(t, path)
	for _, want := range []string{
		"$cfg['Servers'][1]['host'] = 'db';",
		"$cfg['Servers'][1]['port'] = '3306';",
		"$cfg['Servers'][1]['auth_type'] = 'signon';",
		"$cfg['Servers'][1]['SignonSession'] = 'devstack_signon';",
		"/signon.php",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config.user.inc.php missing %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{"auth_type'] = 'config'", "auth_type'] = 'cookie'", "password'] = '"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("config.user.inc.php contains insecure or credential material %q:\n%s", forbidden, content)
		}
	}
	requireTestMode(t, path, 0o644)
	requireTestMode(t, filepath.Dir(path), 0o755)
	signonPath := filepath.Join(root, "services", "phpmyadmin", "config", "signon.php")
	if content := readTestFile(t, signonPath); !strings.Contains(content, "devstack_signon_verify_token") {
		t.Fatalf("signon.php missing token validation:\n%s", content)
	}
	requireTestMode(t, signonPath, 0o644)
	keyPath := filepath.Join(root, "secrets", "phpmyadmin", "signon_key")
	key := strings.TrimSpace(readTestFile(t, keyPath))
	if len(key) != 64 {
		t.Fatalf("signon_key length = %d, want 64 hex characters", len(key))
	}
	requireTestMode(t, keyPath, 0o644)
	if got := strings.TrimSpace(readTestFile(t, filepath.Join(root, "services", "phpmyadmin", "secrets", "signon_key"))); got != key {
		t.Fatalf("agent signon_key = %q, want the same key", got)
	}
	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	if got := strings.TrimSpace(readTestFile(t, keyPath)); got != key {
		t.Fatal("PrepareActivate() rotated an existing signon key")
	}
}

func TestPhpMyAdminPreparerDoesNotOverwriteEditedConfig(t *testing.T) {
	root := t.TempDir()
	prep := NewPhpMyAdminPreparer(root)
	svc, _ := services.DefaultCatalog().Get(services.IDPHPMyAdmin)
	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "services", "phpmyadmin", "config", "config.user.inc.php")
	edited := readTestFile(t, path) + "\n// operator edit\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	if got := readTestFile(t, path); got != edited {
		t.Fatal("PrepareActivate() overwrote an edited config")
	}
}

func TestPhpMyAdminPreparerRejectsWrongService(t *testing.T) {
	prep := NewPhpMyAdminPreparer(t.TempDir())
	svc, _ := services.DefaultCatalog().Get(services.IDMySQL)
	if err := prep.PrepareInstall(context.Background(), svc); err == nil {
		t.Fatal("PrepareInstall(mysql) = nil, want an error")
	}
}

func TestPhpMyAdminPreparerSyncsActiveMySQLRootPassword(t *testing.T) {
	root := t.TempDir()
	const secret = "root-secret-for-pma"
	writeSecret(t, root, "mysql", secret)
	store := newFakeStore(services.Record{ID: services.IDMySQL, State: services.StateActive, Active: true})
	prep := NewPhpMyAdminPreparer(root, store)
	svc, _ := services.DefaultCatalog().Get(services.IDPHPMyAdmin)

	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	path := filepath.Join(root, "secrets", "phpmyadmin", "root_password")
	if got := strings.TrimSpace(readTestFile(t, path)); got != secret {
		t.Fatalf("root password file = %q, want the active engine secret", got)
	}
	requireTestMode(t, path, 0o644)
	requireTestMode(t, filepath.Dir(path), 0o700)
	agentPath := filepath.Join(root, "services", "phpmyadmin", "secrets", "root_password")
	if got := strings.TrimSpace(readTestFile(t, agentPath)); got != secret {
		t.Fatalf("agent root password file = %q, want the active engine secret", got)
	}
	requireTestMode(t, agentPath, 0o644)
	requireTestMode(t, filepath.Dir(agentPath), 0o700)
}

func TestPhpMyAdminPreparerSyncsActiveMariaDBRootPassword(t *testing.T) {
	root := t.TempDir()
	const secret = "mariadb-root-secret"
	writeSecret(t, root, "mariadb", secret)
	store := newFakeStore(services.Record{ID: services.IDMariaDB, State: services.StateActive, Active: true})
	prep := NewPhpMyAdminPreparer(root, store)
	svc, _ := services.DefaultCatalog().Get(services.IDPHPMyAdmin)

	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	if got := strings.TrimSpace(readTestFile(t, filepath.Join(root, "secrets", "phpmyadmin", "root_password"))); got != secret {
		t.Fatalf("root password file = %q, want the MariaDB secret", got)
	}
}

func TestPhpMyAdminPreparerLeavesSecretWhenNoDatabaseIsActive(t *testing.T) {
	root := t.TempDir()
	const previous = "previous-root-secret"
	writeSecret(t, root, "phpmyadmin", previous)
	store := newFakeStore()
	prep := NewPhpMyAdminPreparer(root, store)
	svc, _ := services.DefaultCatalog().Get(services.IDPHPMyAdmin)

	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	if got := strings.TrimSpace(readTestFile(t, filepath.Join(root, "secrets", "phpmyadmin", "root_password"))); got != previous {
		t.Fatalf("root password file = %q, want the previous secret", got)
	}
}

func TestPhpMyAdminPreparerMigratesLegacyGeneratedConfig(t *testing.T) {
	for name, legacy := range map[string]string{
		"cookie":      services.PhpMyAdminLegacyUserConfig,
		"config-auth": services.PhpMyAdminConfigAuthUserConfig,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "services", "phpmyadmin", "config", "config.user.inc.php")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
				t.Fatal(err)
			}
			prep := NewPhpMyAdminPreparer(root)
			svc, _ := services.DefaultCatalog().Get(services.IDPHPMyAdmin)

			if err := prep.PrepareActivate(context.Background(), svc); err != nil {
				t.Fatalf("PrepareActivate() = %v", err)
			}
			content := readTestFile(t, path)
			for _, forbidden := range []string{"auth_type'] = 'cookie'", "auth_type'] = 'config'"} {
				if strings.Contains(content, forbidden) {
					t.Fatalf("legacy generated config was not migrated to signon:\n%s", content)
				}
			}
			if !strings.Contains(content, "auth_type'] = 'signon'") {
				t.Fatalf("migrated config missing signon auth:\n%s", content)
			}
		})
	}
}

func writeSecret(t *testing.T, root, engine, value string) {
	t.Helper()
	dir := filepath.Join(root, "secrets", engine)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root_password"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPhpMyAdminPreparerHasNoCredentials(t *testing.T) {
	prep := NewPhpMyAdminPreparer(t.TempDir())
	if _, err := prep.Credentials(context.Background(), services.IDPHPMyAdmin); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("Credentials() error = %v, want ErrCredentialsUnsupported", err)
	}
	if _, err := prep.RotateCredentials(context.Background(), services.IDPHPMyAdmin); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("RotateCredentials() error = %v, want ErrCredentialsUnsupported", err)
	}
}

func TestNewPhpMyAdminPreparers(t *testing.T) {
	preparers := NewPhpMyAdminPreparers(t.TempDir())
	if prep, ok := preparers[services.IDPHPMyAdmin]; !ok || prep == nil {
		t.Fatalf("phpmyadmin preparer = %v, present = %t", prep, ok)
	}
	if len(preparers) != 1 {
		t.Fatalf("preparers = %d, want 1", len(preparers))
	}
}
