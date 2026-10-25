package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	mysqlDigest      = "76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69"
	mariadbDigest    = "f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa"
	postgresqlDigest = "edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6"
	redisDigest      = "418e397fd48d66e450af13baab123397f2d2580b07ef310c3a5dbe96c3d731f1"
)

func databaseCatalog(t *testing.T) Catalog {
	t.Helper()
	c := DefaultCatalog()
	if err := c.Validate(); err != nil {
		t.Fatalf("DefaultCatalog().Validate() = %v", err)
	}
	return c
}

func requireDatabaseService(t *testing.T, c Catalog, id ID) Service {
	t.Helper()
	svc, ok := c.Get(id)
	if !ok {
		t.Fatalf("catalog missing database service %q", id)
	}
	return svc
}

func requireMount(t *testing.T, svc Service, containerPath string) Mount {
	t.Helper()
	for _, mounts := range [][]Mount{svc.Config, svc.Data} {
		for _, mount := range mounts {
			if mount.ContainerPath == containerPath {
				return mount
			}
		}
	}
	t.Fatalf("service %q has no mount for %q", svc.ID, containerPath)
	return Mount{}
}

func TestDatabaseCatalogPinnedImages(t *testing.T) {
	c := databaseCatalog(t)
	cases := []struct {
		id     ID
		image  string
		group  Group
		port   int
		health string
	}{
		{IDMySQL, "docker.io/library/mysql@sha256:" + mysqlDigest, GroupMySQLFamily, 3306, "mysqladmin"},
		{IDMariaDB, "docker.io/library/mariadb@sha256:" + mariadbDigest, GroupMySQLFamily, 3306, "healthcheck.sh"},
		{IDPostgreSQL, "docker.io/library/postgres@sha256:" + postgresqlDigest, GroupStandalone, 5432, "pg_isready"},
		{IDRedis, "docker.io/library/redis@sha256:" + redisDigest, GroupStandalone, 6379, "redis-cli"},
	}

	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			svc := requireDatabaseService(t, c, tc.id)
			if svc.Image != tc.image {
				t.Errorf("image = %q, want %q", svc.Image, tc.image)
			}
			if svc.Group != tc.group {
				t.Errorf("group = %q, want %q", svc.Group, tc.group)
			}
			if svc.NetworkMode != "bridge" {
				t.Errorf("network mode = %q, want bridge", svc.NetworkMode)
			}
			if len(svc.Ports) != 1 {
				t.Fatalf("ports = %+v, want exactly one", svc.Ports)
			}
			port := svc.Ports[0]
			if port.Host != tc.port || port.Container != tc.port || !port.Loopback {
				t.Errorf("port = %+v, want loopback host/container %d", port, tc.port)
			}
			if !containsString(svc.SecurityOpts, "no-new-privileges:true") {
				t.Errorf("security opts = %v, want no-new-privileges:true", svc.SecurityOpts)
			}
			if svc.Healthcheck == nil {
				t.Fatal("healthcheck is nil")
			}
			if got := strings.Join(svc.Healthcheck.Test, " "); !strings.Contains(got, tc.health) {
				t.Errorf("healthcheck = %q, want it to contain %q", got, tc.health)
			}
			if len(svc.ValidateCommand) != 0 || len(svc.ReloadCommand) != 0 {
				t.Errorf("validate/reload = %v/%v, want empty for a reliable-only wave 1 catalog",
					svc.ValidateCommand, svc.ReloadCommand)
			}
		})
	}
}

func TestDatabaseCatalogSELinuxMounts(t *testing.T) {
	c := databaseCatalog(t)
	cases := []struct {
		id       ID
		required []string
	}{
		{
			IDMySQL,
			[]string{"/etc/mysql/conf.d", "/var/lib/mysql", "/var/log/mysql",
				"/run/secrets/mysql_root_password", "/run/secrets/mysql_devstack_password"},
		},
		{
			IDMariaDB,
			[]string{"/etc/mysql/conf.d", "/var/lib/mysql", "/var/log/mysql",
				"/run/secrets/mariadb_root_password", "/run/secrets/mariadb_devstack_password"},
		},
		{
			IDPostgreSQL,
			[]string{"/etc/postgresql/postgresql.conf", "/etc/postgresql/pg_hba.conf",
				"/docker-entrypoint-initdb.d", "/var/lib/postgresql/data",
				"/run/secrets/postgresql_postgres_password", "/run/secrets/postgresql_devstack_password"},
		},
		{
			IDRedis,
			[]string{"/usr/local/etc/redis/redis.conf", "/data"},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			svc := requireDatabaseService(t, c, tc.id)
			for _, path := range tc.required {
				mount := requireMount(t, svc, path)
				if !mount.SELinux {
					t.Errorf("mount %q does not request SELinux :z", path)
				}
			}
		})
	}
}

func TestDatabaseCatalogMySQLFamilyLayout(t *testing.T) {
	c := databaseCatalog(t)
	for _, tc := range []struct {
		id      ID
		secrets []string
	}{
		{IDMySQL, []string{"mysql/root_password", "mysql/devstack_password"}},
		{IDMariaDB, []string{"mariadb/root_password", "mariadb/devstack_password"}},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			svc := requireDatabaseService(t, c, tc.id)

			config := requireMount(t, svc, "/etc/mysql/conf.d")
			if config.Area != MountService || config.HostSubdir != "config" || !config.ReadOnly {
				t.Errorf("config mount = %+v, want service config read-only", config)
			}
			data := requireMount(t, svc, "/var/lib/mysql")
			if data.Area != MountService || data.HostSubdir != "data" || data.ReadOnly {
				t.Errorf("data mount = %+v, want service data writable", data)
			}

			var secretMounts []Mount
			for _, mount := range svc.Config {
				if mount.Area == MountSecrets {
					secretMounts = append(secretMounts, mount)
				}
			}
			if len(secretMounts) != 2 {
				t.Fatalf("secret mounts = %+v, want two", secretMounts)
			}
			for i, want := range tc.secrets {
				if secretMounts[i].HostSubdir != want || !secretMounts[i].ReadOnly {
					t.Errorf("secret mount[%d] = %+v, want %q read-only", i, secretMounts[i], want)
				}
			}

			if got := svc.Environment["MYSQL_ROOT_PASSWORD_FILE"]; tc.id == IDMySQL && got != "/run/secrets/mysql_root_password" {
				t.Errorf("MYSQL_ROOT_PASSWORD_FILE = %q", got)
			}
			if got := svc.Environment["MARIADB_ROOT_PASSWORD_FILE"]; tc.id == IDMariaDB && got != "/run/secrets/mariadb_root_password" {
				t.Errorf("MARIADB_ROOT_PASSWORD_FILE = %q", got)
			}
			if value := svc.Environment["MYSQL_PASSWORD_FILE"]; tc.id == IDMySQL && value != "/run/secrets/mysql_devstack_password" {
				t.Errorf("MYSQL_PASSWORD_FILE = %q", value)
			}
			if value := svc.Environment["MARIADB_PASSWORD_FILE"]; tc.id == IDMariaDB && value != "/run/secrets/mariadb_devstack_password" {
				t.Errorf("MARIADB_PASSWORD_FILE = %q", value)
			}
		})
	}
}

func TestDatabaseCatalogPostgreSQLayout(t *testing.T) {
	c := databaseCatalog(t)
	svc := requireDatabaseService(t, c, IDPostgreSQL)

	for _, tc := range []struct {
		path       string
		hostSubdir string
		readOnly   bool
	}{
		{"/etc/postgresql/postgresql.conf", "config/postgresql.conf", true},
		{"/etc/postgresql/pg_hba.conf", "config/pg_hba.conf", true},
		{"/docker-entrypoint-initdb.d", "config/init", true},
		{"/var/lib/postgresql/data", "data", false},
	} {
		mount := requireMount(t, svc, tc.path)
		if mount.HostSubdir != tc.hostSubdir || mount.ReadOnly != tc.readOnly {
			t.Errorf("mount %q = %+v", tc.path, mount)
		}
	}

	for _, want := range []string{
		"/run/secrets/postgresql_postgres_password",
		"/run/secrets/postgresql_devstack_password",
	} {
		mount := requireMount(t, svc, want)
		if mount.Area != MountSecrets {
			t.Errorf("mount %q area = %q, want secrets", want, mount.Area)
		}
	}

	if got := svc.Environment["POSTGRES_PASSWORD_FILE"]; got != "/run/secrets/postgresql_postgres_password" {
		t.Errorf("POSTGRES_PASSWORD_FILE = %q", got)
	}
	if got := svc.Environment["PGDATA"]; got != "/var/lib/postgresql/data" {
		t.Errorf("PGDATA = %q", got)
	}
	if len(svc.Entrypoint) != 3 || svc.Entrypoint[0] != "/bin/sh" || svc.Entrypoint[1] != "-c" {
		t.Fatalf("entrypoint = %v, want a root wrapper shell", svc.Entrypoint)
	}
	for _, want := range []string{
		`POSTGRES_PASSWORD="$(cat /run/secrets/postgresql_postgres_password)"`,
		`POSTGRES_DEVSTACK_PASSWORD="$(cat /run/secrets/postgresql_devstack_password)"`,
		"export POSTGRES_PASSWORD POSTGRES_DEVSTACK_PASSWORD",
		"unset POSTGRES_PASSWORD_FILE POSTGRES_DEVSTACK_PASSWORD_FILE",
		"exec /usr/local/bin/docker-entrypoint.sh postgres",
		"config_file=/etc/postgresql/postgresql.conf",
	} {
		if !strings.Contains(svc.Entrypoint[2], want) {
			t.Errorf("postgres entrypoint missing %q:\n%s", want, svc.Entrypoint[2])
		}
	}
	if len(svc.Command) != 0 {
		t.Errorf("postgres command = %v, want the entrypoint to supply argv", svc.Command)
	}
}

func TestDatabaseCatalogRedisLayout(t *testing.T) {
	c := databaseCatalog(t)
	svc := requireDatabaseService(t, c, IDRedis)

	config := requireMount(t, svc, "/usr/local/etc/redis/redis.conf")
	if config.Area != MountService || config.HostSubdir != "config/redis.conf" || !config.ReadOnly {
		t.Errorf("redis config mount = %+v", config)
	}
	data := requireMount(t, svc, "/data")
	if data.Area != MountService || data.HostSubdir != "data" || data.ReadOnly {
		t.Errorf("redis data mount = %+v", data)
	}
	if len(svc.Config) != 1 {
		t.Errorf("redis config mounts = %+v, want only the config file", svc.Config)
	}
	if len(svc.Command) != 2 || svc.Command[0] != "redis-server" || svc.Command[1] != "/usr/local/etc/redis/redis.conf" {
		t.Errorf("command = %v, want redis-server with the mounted config", svc.Command)
	}
}

func TestDatabaseCatalogEnvironmentHasNoLiteralSecrets(t *testing.T) {
	c := databaseCatalog(t)
	for _, id := range []ID{IDMySQL, IDMariaDB, IDPostgreSQL, IDRedis} {
		svc := requireDatabaseService(t, c, id)
		for key, value := range svc.Environment {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") {
				if !strings.HasSuffix(key, "_FILE") {
					t.Errorf("%s: %s must use a _FILE indirection", id, key)
				}
				if !strings.HasPrefix(value, "/run/secrets/") {
					t.Errorf("%s: %s = %q, want a /run/secrets path", id, key, value)
				}
			}
		}
		if strings.Contains(strings.ToLower(strings.Join(svc.Command, " ")), "password=") {
			t.Errorf("%s: command contains a literal password", id)
		}
	}
}

func TestRenderComposeDatabaseServices(t *testing.T) {
	root := t.TempDir()
	sentinel := "s3cr3t-sentinel-value"
	for _, rel := range []string{
		"secrets/mysql/root_password",
		"secrets/mysql/devstack_password",
		"secrets/mariadb/root_password",
		"secrets/mariadb/devstack_password",
		"secrets/postgresql/postgres_password",
		"secrets/postgresql/devstack_password",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	data, err := RenderCompose(databaseCatalog(t), root)
	if err != nil {
		t.Fatalf("RenderCompose() = %v", err)
	}

	var doc composeFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal compose: %v", err)
	}

	mysql, ok := doc.Services["mysql"]
	if !ok {
		t.Fatal("compose missing mysql service")
	}
	if len(mysql.Ports) != 1 || mysql.Ports[0] != "127.0.0.1:3306:3306" {
		t.Errorf("mysql ports = %v", mysql.Ports)
	}
	if mysql.Environment["MYSQL_ROOT_PASSWORD_FILE"] != "/run/secrets/mysql_root_password" {
		t.Errorf("mysql env = %v", mysql.Environment)
	}
	if !containsString(mysql.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("mysql security_opt = %v", mysql.SecurityOpt)
	}
	if mysql.Healthcheck == nil {
		t.Fatal("mysql compose healthcheck is nil")
	}

	var foundConfig, foundSecret bool
	for _, volume := range mysql.Volumes {
		if strings.HasSuffix(volume, ":/etc/mysql/conf.d:ro,z") {
			foundConfig = true
		}
		if strings.HasSuffix(volume, ":/run/secrets/mysql_root_password:ro,z") {
			foundSecret = true
		}
	}
	if !foundConfig {
		t.Errorf("mysql volumes = %v, want config mount with :ro,z", mysql.Volumes)
	}
	if !foundSecret {
		t.Errorf("mysql volumes = %v, want secret mount with :ro,z", mysql.Volumes)
	}

	redis := doc.Services["redis"]
	if len(redis.Command) != 2 || redis.Command[0] != "redis-server" {
		t.Errorf("redis command = %v", redis.Command)
	}
	postgres := doc.Services["postgresql"]
	if len(postgres.Entrypoint) != 3 || !strings.Contains(postgres.Entrypoint[2], "docker-entrypoint.sh") {
		t.Errorf("postgres entrypoint = %v", postgres.Entrypoint)
	}
	if postgres.Environment["PGDATA"] != "/var/lib/postgresql/data" {
		t.Errorf("postgres env = %v", postgres.Environment)
	}

	rendered := string(data)
	if strings.Contains(rendered, sentinel) {
		t.Fatal("compose output contains a literal secret value")
	}
	for _, secret := range []string{"root_password: ", "POSTGRES_PASSWORD: "} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("compose output contains a literal secret marker %q", secret)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
