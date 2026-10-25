package services

import "time"

// Pinned database images. The tag is kept alongside the exact digest from the
// Wave 1 plan; the digest is authoritative.
const (
	mysqlImage      = "mysql:8.4.13@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69"
	mariadbImage    = "mariadb:11.8.9@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa"
	postgresqlImage = "postgres:18.6@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6"
	redisImage      = "redis:8.8.3@sha256:418e397fd48d66e450af13baab123397f2d2580b07ef310c3a5dbe96c3d731f1"

	noNewPrivileges = "no-new-privileges:true"
)

// databaseServices returns the MySQL, MariaDB, PostgreSQL, and Redis catalog
// entries. Database ports are published on loopback only and every bind mount
// requests an SELinux :z relabel.
func databaseServices() []Service {
	return []Service{
		mysqlFamilyService(
			IDMySQL,
			"MySQL 8.4",
			mysqlImage,
			map[string]string{
				"MYSQL_ROOT_PASSWORD_FILE": "/run/secrets/mysql_root_password",
				"MYSQL_DATABASE":           "devstack",
				"MYSQL_USER":               "devstack",
				"MYSQL_PASSWORD_FILE":      "/run/secrets/mysql_devstack_password",
			},
			[]Mount{
				secretMount("mysql/root_password", "/run/secrets/mysql_root_password"),
				secretMount("mysql/devstack_password", "/run/secrets/mysql_devstack_password"),
			},
			mysqlHealthcheck(),
		),
		mysqlFamilyService(
			IDMariaDB,
			"MariaDB 11.8",
			mariadbImage,
			map[string]string{
				"MARIADB_ROOT_PASSWORD_FILE": "/run/secrets/mariadb_root_password",
				"MARIADB_DATABASE":           "devstack",
				"MARIADB_USER":               "devstack",
				"MARIADB_PASSWORD_FILE":      "/run/secrets/mariadb_devstack_password",
			},
			[]Mount{
				secretMount("mariadb/root_password", "/run/secrets/mariadb_root_password"),
				secretMount("mariadb/devstack_password", "/run/secrets/mariadb_devstack_password"),
			},
			mariadbHealthcheck(),
		),
		{
			ID:          IDPostgreSQL,
			DisplayName: "PostgreSQL 18",
			Group:       GroupStandalone,
			Image:       postgresqlImage,
			NetworkMode: "bridge",
			Ports: []Port{
				{Host: 5432, Container: 5432, Protocol: "tcp", Loopback: true},
			},
			Config: []Mount{
				{
					HostSubdir:    "config/postgresql.conf",
					ContainerPath: "/etc/postgresql/postgresql.conf",
					ReadOnly:      true,
					Area:          MountService,
					SELinux:       true,
				},
				{
					HostSubdir:    "config/pg_hba.conf",
					ContainerPath: "/etc/postgresql/pg_hba.conf",
					ReadOnly:      true,
					Area:          MountService,
					SELinux:       true,
				},
				{
					HostSubdir:    "config/init",
					ContainerPath: "/docker-entrypoint-initdb.d",
					ReadOnly:      true,
					Area:          MountService,
					SELinux:       true,
				},
				secretMount("postgresql/postgres_password", "/run/secrets/postgresql_postgres_password"),
				secretMount("postgresql/devstack_password", "/run/secrets/postgresql_devstack_password"),
			},
			Data: []Mount{
				serviceMount("data", "/var/lib/postgresql/data", false),
			},
			Environment: map[string]string{
				"POSTGRES_PASSWORD_FILE": "/run/secrets/postgresql_postgres_password",
				"POSTGRES_USER":          "postgres",
				"PGDATA":                 "/var/lib/postgresql/data",
			},
			Entrypoint:  []string{"/bin/sh", "-c", postgresEntrypoint},
			Healthcheck: postgresHealthcheck(),
			SecurityOpts: []string{
				noNewPrivileges,
			},
		},
		{
			ID:          IDRedis,
			DisplayName: "Redis 8.8",
			Group:       GroupStandalone,
			Image:       redisImage,
			NetworkMode: "bridge",
			Ports: []Port{
				{Host: 6379, Container: 6379, Protocol: "tcp", Loopback: true},
			},
			Config: []Mount{
				{
					HostSubdir:    "config/redis.conf",
					ContainerPath: "/usr/local/etc/redis/redis.conf",
					ReadOnly:      true,
					Area:          MountService,
					SELinux:       true,
				},
			},
			Data: []Mount{
				serviceMount("data", "/data", false),
			},
			Command:     []string{"redis-server", "/usr/local/etc/redis/redis.conf"},
			Healthcheck: redisHealthcheck(),
			SecurityOpts: []string{
				noNewPrivileges,
			},
		},
	}
}

func mysqlFamilyService(
	id ID,
	displayName string,
	image string,
	environment map[string]string,
	secrets []Mount,
	healthcheck *Healthcheck,
) Service {
	config := []Mount{
		serviceMount("config", "/etc/mysql/conf.d", true),
		serviceMount("config/init", "/docker-entrypoint-initdb.d", true),
		serviceMount("logs", "/var/log/mysql", false),
	}
	config = append(config, secrets...)
	return Service{
		ID:          id,
		DisplayName: displayName,
		Group:       GroupMySQLFamily,
		Image:       image,
		NetworkMode: "bridge",
		Ports: []Port{
			{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true},
		},
		Config:       config,
		Data:         []Mount{serviceMount("data", "/var/lib/mysql", false)},
		Environment:  environment,
		Healthcheck:  healthcheck,
		SecurityOpts: []string{noNewPrivileges},
	}
}

func serviceMount(hostSubdir, containerPath string, readOnly bool) Mount {
	return Mount{
		HostSubdir:    hostSubdir,
		ContainerPath: containerPath,
		ReadOnly:      readOnly,
		Area:          MountService,
		SELinux:       true,
	}
}

func secretMount(hostSubdir, containerPath string) Mount {
	return Mount{
		HostSubdir:    hostSubdir,
		ContainerPath: containerPath,
		ReadOnly:      true,
		Area:          MountSecrets,
		SELinux:       true,
	}
}

func mysqlHealthcheck() *Healthcheck {
	return &Healthcheck{
		Test:        []string{"CMD-SHELL", "mysqladmin ping -h 127.0.0.1 --silent"},
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		StartPeriod: 30 * time.Second,
		Retries:     10,
	}
}

func mariadbHealthcheck() *Healthcheck {
	return &Healthcheck{
		Test:        []string{"CMD", "healthcheck.sh", "--connect", "--innodb_initialized"},
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		StartPeriod: 30 * time.Second,
		Retries:     10,
	}
}

// postgresEntrypoint runs as the image's root user, reads the 0600 host
// secrets, and exports them before the official entrypoint drops privileges.
// Init scripts run as the postgres user, so they consume the exported
// POSTGRES_DEVSTACK_PASSWORD instead of trying to read a root-only file mount.
const postgresEntrypoint = `set -eu
POSTGRES_PASSWORD="$(cat /run/secrets/postgresql_postgres_password)"
POSTGRES_DEVSTACK_PASSWORD="$(cat /run/secrets/postgresql_devstack_password)"
export POSTGRES_PASSWORD POSTGRES_DEVSTACK_PASSWORD
unset POSTGRES_PASSWORD_FILE POSTGRES_DEVSTACK_PASSWORD_FILE
exec /usr/local/bin/docker-entrypoint.sh postgres -c config_file=/etc/postgresql/postgresql.conf`

func postgresHealthcheck() *Healthcheck {
	return &Healthcheck{
		Test:        []string{"CMD-SHELL", "pg_isready -U postgres -d postgres -h 127.0.0.1"},
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		StartPeriod: 30 * time.Second,
		Retries:     10,
	}
}

func redisHealthcheck() *Healthcheck {
	return &Healthcheck{
		Test:        []string{"CMD", "redis-cli", "ping"},
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		StartPeriod: 5 * time.Second,
		Retries:     10,
	}
}
