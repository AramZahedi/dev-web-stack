package servicecontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

var dbSecretPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func testDatabaseService(t *testing.T, id services.ID) services.Service {
	t.Helper()
	svc, ok := services.DefaultCatalog().Get(id)
	if !ok {
		t.Fatalf("catalog missing %q", id)
	}
	return svc
}

func testDatabasePreparer(t *testing.T) (*DatabasePreparer, string) {
	t.Helper()
	root := t.TempDir()
	return NewDatabasePreparer(root), root
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireTestMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}

func TestDatabasePreparerRegistration(t *testing.T) {
	preparers := NewDatabasePreparers(t.TempDir())
	for _, id := range []services.ID{services.IDMySQL, services.IDMariaDB, services.IDPostgreSQL, services.IDRedis} {
		prep, ok := preparers[id]
		if !ok || prep == nil {
			t.Fatalf("missing preparer for %q", id)
		}
		if !IsDatabaseService(id) {
			t.Errorf("IsDatabaseService(%q) = false", id)
		}
	}
	if len(preparers) != 4 {
		t.Errorf("preparers = %d, want 4", len(preparers))
	}
	if IsDatabaseService(services.IDNginx) {
		t.Error("IsDatabaseService(nginx) = true")
	}
	ids := DatabasePreparerIDs()
	if len(ids) != 4 {
		t.Errorf("DatabasePreparerIDs() = %v", ids)
	}
}

func TestPrepareInstallMySQLCreatesSecureScaffold(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDMySQL)

	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatalf("PrepareInstall() = %v", err)
	}

	serviceDir := filepath.Join(root, "services", "mysql")
	for _, dir := range []string{"config", "data", "logs"} {
		requireTestMode(t, filepath.Join(serviceDir, dir), 0o755)
	}

	configPath := filepath.Join(serviceDir, "config", "devstack.cnf")
	content := readTestFile(t, configPath)
	for _, want := range []string{
		"skip-name-resolve",
		"max_connections=256",
		"character-set-server=utf8mb4",
		"collation-server=utf8mb4_unicode_ci",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("devstack.cnf missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "bind-address") {
		t.Errorf("devstack.cnf contains bind-address inside the container:\n%s", content)
	}
	requireTestMode(t, configPath, 0o644)

	initDir := filepath.Join(serviceDir, "config", "init")
	requireTestMode(t, initDir, 0o755)
	initPath := filepath.Join(initDir, "10-devstack-admin.sql")
	initSQL := readTestFile(t, initPath)
	for _, want := range []string{
		"GRANT ALL PRIVILEGES ON *.* TO 'devstack'@'%' WITH GRANT OPTION",
		"FLUSH PRIVILEGES",
	} {
		if !strings.Contains(initSQL, want) {
			t.Errorf("admin init SQL missing %q:\n%s", want, initSQL)
		}
	}
	requireTestMode(t, initPath, 0o644)

	for _, name := range []string{"root_password", "devstack_password"} {
		path := filepath.Join(root, "secrets", "mysql", name)
		requireTestMode(t, path, 0o600)
		value := strings.TrimSpace(readTestFile(t, path))
		if !dbSecretPattern.MatchString(value) {
			t.Errorf("%s = %q, want 64 lowercase hex characters", name, value)
		}
	}
	requireTestMode(t, filepath.Join(root, "secrets", "mysql"), 0o700)
}

func TestPrepareActivateDoesNotOverwriteEditedConfig(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDMariaDB)

	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "services", "mariadb", "config", "devstack.cnf")
	edited := readTestFile(t, configPath) + "\n# local operator edit\n"
	if err := os.WriteFile(configPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	secretBefore := readTestFile(t, filepath.Join(root, "secrets", "mariadb", "root_password"))

	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	if got := readTestFile(t, configPath); got != edited {
		t.Fatalf("PrepareActivate overwrote an edited config:\n%s", got)
	}
	if got := readTestFile(t, filepath.Join(root, "secrets", "mariadb", "root_password")); got != secretBefore {
		t.Fatal("PrepareActivate replaced an existing secret")
	}
}

func TestPrepareMySQLFamilySeedsAdminGrant(t *testing.T) {
	for _, id := range []services.ID{services.IDMySQL, services.IDMariaDB} {
		t.Run(string(id), func(t *testing.T) {
			prep, root := testDatabasePreparer(t)
			if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, id)); err != nil {
				t.Fatalf("PrepareInstall() = %v", err)
			}
			path := filepath.Join(root, "services", string(id), "config", "init", "10-devstack-admin.sql")
			requireTestMode(t, filepath.Dir(path), 0o755)
			requireTestMode(t, path, 0o644)
			sql := readTestFile(t, path)
			if !strings.Contains(sql, "GRANT ALL PRIVILEGES ON *.* TO 'devstack'@'%' WITH GRANT OPTION") {
				t.Errorf("admin grant SQL = %s", sql)
			}
			if strings.Contains(sql, "IDENTIFIED BY") {
				t.Errorf("admin grant SQL must not embed a password: %s", sql)
			}
		})
	}
}

func TestPrepareCreatesDataBeforeSecretsAndRetries(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	// Block credential creation with a regular file where the secrets root
	// belongs. Data scaffolding must already have happened, so a retry after
	// removing the block works.
	if err := os.WriteFile(filepath.Join(root, "secrets"), []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDMySQL)); err == nil {
		t.Fatal("PrepareInstall() with blocked secrets root = nil, want an error")
	}
	dataDir := filepath.Join(root, "services", "mysql", "data")
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("data directory was not created before secret generation: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dataDir)
	}
	if err := os.Remove(filepath.Join(root, "secrets")); err != nil {
		t.Fatal(err)
	}
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDMySQL)); err != nil {
		t.Fatalf("retry after data scaffold = %v", err)
	}
	for _, name := range []string{"root_password", "devstack_password"} {
		requireTestMode(t, filepath.Join(root, "secrets", "mysql", name), 0o600)
	}
}

func TestPrepareMySQLFamilyUsesIndependentSecrets(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	for _, id := range []services.ID{services.IDMySQL, services.IDMariaDB} {
		if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, id)); err != nil {
			t.Fatalf("PrepareInstall(%s) = %v", id, err)
		}
	}
	mysql := readTestFile(t, filepath.Join(root, "secrets", "mysql", "root_password"))
	mariadb := readTestFile(t, filepath.Join(root, "secrets", "mariadb", "root_password"))
	if mysql == mariadb {
		t.Fatal("MySQL and MariaDB root passwords are shared")
	}
}

func TestPreparePostgreSQLSeedsConfig(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDPostgreSQL)
	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatalf("PrepareInstall() = %v", err)
	}

	configDir := filepath.Join(root, "services", "postgresql", "config")
	postgresConf := readTestFile(t, filepath.Join(configDir, "postgresql.conf"))
	for _, want := range []string{
		"listen_addresses = '*'",
		"data_directory = '/var/lib/postgresql/data'",
		"hba_file = '/etc/postgresql/pg_hba.conf'",
	} {
		if !strings.Contains(postgresConf, want) {
			t.Errorf("postgresql.conf missing %q:\n%s", want, postgresConf)
		}
	}
	requireTestMode(t, filepath.Join(configDir, "postgresql.conf"), 0o644)

	pgHBA := readTestFile(t, filepath.Join(configDir, "pg_hba.conf"))
	if !strings.Contains(pgHBA, "scram-sha-256") {
		t.Errorf("pg_hba.conf has no scram-sha-256 rule:\n%s", pgHBA)
	}
	if !strings.Contains(pgHBA, "127.0.0.1/32") {
		t.Errorf("pg_hba.conf has no loopback rule:\n%s", pgHBA)
	}
	requireTestMode(t, filepath.Join(configDir, "pg_hba.conf"), 0o644)

	initPath := filepath.Join(configDir, "init", "10-devstack.sh")
	initScript := readTestFile(t, initPath)
	requireTestMode(t, initPath, 0o755)
	for _, want := range []string{
		"POSTGRES_DEVSTACK_PASSWORD",
		"pg_roles",
		"CREATE ROLE",
		"CREATEDB",
		"CREATEROLE",
		"pg_database",
		"CREATE DATABASE",
	} {
		if !strings.Contains(initScript, want) {
			t.Errorf("init script missing %q:\n%s", want, initScript)
		}
	}
	if strings.Contains(initScript, "cat /run/secrets") {
		t.Errorf("init script must not cat a root-only secret mount:\n%s", initScript)
	}

	for _, name := range []string{"postgres_password", "devstack_password"} {
		requireTestMode(t, filepath.Join(root, "secrets", "postgresql", name), 0o600)
	}
}

func TestPrepareRedisSeedsConfigWithoutPassword(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDRedis)
	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatalf("PrepareInstall() = %v", err)
	}

	configPath := filepath.Join(root, "services", "redis", "config", "redis.conf")
	content := readTestFile(t, configPath)
	for _, want := range []string{"appendonly yes", "dir /data", "protected-mode no"} {
		if !strings.Contains(content, want) {
			t.Errorf("redis.conf missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "requirepass") {
		t.Errorf("redis.conf enables a password by default:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(root, "secrets", "redis", "password")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("redis password exists by default: %v", err)
	}
}

func TestPrepareRejectsUnknownService(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	if err := prep.PrepareInstall(context.Background(), services.Service{ID: services.IDNginx}); err == nil {
		t.Fatal("PrepareInstall(nginx) = nil, want an error")
	}
	if _, err := prep.Credentials(context.Background(), services.IDNginx); err == nil {
		t.Fatal("Credentials(nginx) = nil, want an error")
	}
}

func TestPrepareRejectsEmptyRoot(t *testing.T) {
	prep := NewDatabasePreparer("")
	svc := testDatabaseService(t, services.IDMySQL)
	if err := prep.PrepareInstall(context.Background(), svc); err == nil {
		t.Fatal("PrepareInstall with an empty root = nil, want an error")
	}
}

func TestPrepareHonorsContextCancellation(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := prep.PrepareActivate(ctx, testDatabaseService(t, services.IDMySQL))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareActivate() error = %v, want context.Canceled", err)
	}
}

func TestPrepareReportsDataWithoutSecret(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDPostgreSQL)
	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "services", "postgresql", "data", "PG_VERSION"), []byte("18\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(root, "secrets", "postgresql", "devstack_password")
	secret := strings.TrimSpace(readTestFile(t, secretPath))
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}

	err := prep.PrepareActivate(context.Background(), svc)
	if !errors.Is(err, credentials.ErrDataWithoutSecret) {
		t.Fatalf("PrepareActivate() error = %v, want ErrDataWithoutSecret", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error message leaks a secret")
	}
}

func TestPrepareReportsSecretWithoutData(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDMySQL)
	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "services", "mysql", "data")); err != nil {
		t.Fatal(err)
	}
	err := prep.PrepareActivate(context.Background(), svc)
	if !errors.Is(err, credentials.ErrSecretWithoutData) {
		t.Fatalf("PrepareActivate() error = %v, want ErrSecretWithoutData", err)
	}
}

func TestDatabaseCredentials(t *testing.T) {
	cases := []struct {
		id   services.ID
		port int
	}{
		{services.IDMySQL, 3306},
		{services.IDMariaDB, 3306},
		{services.IDPostgreSQL, 5432},
	}
	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			prep, root := testDatabasePreparer(t)
			if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, tc.id)); err != nil {
				t.Fatal(err)
			}
			got, err := prep.Credentials(context.Background(), tc.id)
			if err != nil {
				t.Fatalf("Credentials() = %v", err)
			}
			want, err := os.ReadFile(filepath.Join(root, "secrets", string(tc.id), "devstack_password"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Engine != string(tc.id) {
				t.Errorf("Engine = %q", got.Engine)
			}
			if got.Username != "devstack" || got.Database != "devstack" {
				t.Errorf("credentials = %+v", got)
			}
			if got.Host != "127.0.0.1" || got.Port != tc.port {
				t.Errorf("host/port = %s/%d, want 127.0.0.1/%d", got.Host, got.Port, tc.port)
			}
			if got.Password != strings.TrimSpace(string(want)) {
				t.Error("Credentials() password does not match the stored devstack secret")
			}
			if !dbSecretPattern.MatchString(got.Password) {
				t.Errorf("Password = %q, want 64 lowercase hex characters", got.Password)
			}
		})
	}
}

func TestDatabaseCredentialsRequiresPreparedSecret(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	_, err := prep.Credentials(context.Background(), services.IDMySQL)
	if !errors.Is(err, credentials.ErrSecretMissing) {
		t.Fatalf("Credentials() error = %v, want ErrSecretMissing", err)
	}
}

func TestDatabaseCredentialsRedisUnsupported(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDRedis)); err != nil {
		t.Fatal(err)
	}
	_, err := prep.Credentials(context.Background(), services.IDRedis)
	if !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("Credentials(redis) error = %v, want ErrCredentialsUnsupported", err)
	}
}

func TestRotateCredentialsUnavailable(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDMySQL)); err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(readTestFile(t, filepath.Join(root, "secrets", "mysql", "devstack_password")))
	_, err := prep.RotateCredentials(context.Background(), services.IDMySQL)
	if !errors.Is(err, ErrCredentialsRotationUnavailable) {
		t.Fatalf("RotateCredentials() error = %v, want ErrCredentialsRotationUnavailable", err)
	}
	if !strings.Contains(err.Error(), "Wave 2") {
		t.Errorf("error = %v, want a clear Wave 2 explanation", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("rotation error leaks the secret")
	}
}

func TestDatabasePreparerNoopLifecycleHooks(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	svc := testDatabaseService(t, services.IDMySQL)
	if err := prep.CommitActivate(context.Background(), svc); err != nil {
		t.Errorf("CommitActivate() = %v", err)
	}
	if err := prep.CleanupActivate(context.Background(), svc, errors.New("boom")); err != nil {
		t.Errorf("CleanupActivate() = %v", err)
	}
}
