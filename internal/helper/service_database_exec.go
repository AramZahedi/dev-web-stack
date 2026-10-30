//go:build !windows

package helper

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// engineForService maps a catalog service ID to its database engine. Only the
// four database services are valid; everything else is not a database service.
func engineForService(id services.ID) model.DatabaseEngine {
	switch id {
	case services.IDMySQL:
		return model.DatabaseMySQL
	case services.IDMariaDB:
		return model.DatabaseMariaDB
	case services.IDPostgreSQL:
		return model.DatabasePostgreSQL
	case services.IDRedis:
		return model.DatabaseRedis
	default:
		return ""
	}
}

// databaseEngine resolves the SQL engine for a service. Redis returns a nil
// engine because its user operations are implemented directly by the helper;
// the frozen RedisACL interface cannot be implemented by a value that also
// implements Engine (the ListUsersCommand signatures collide).
func (c *DockerServiceController) databaseEngine(serviceID string) (dbengine.Engine, model.DatabaseEngine, error) {
	svc, err := c.service(services.ID(serviceID))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	engineID := engineForService(svc.ID)
	if engineID == "" {
		return nil, "", fmt.Errorf("%w: service %q is not a database service", databases.ErrUnsupported, serviceID)
	}
	if engineID == model.DatabaseRedis {
		return nil, engineID, nil
	}
	registry := c.databaseRegistry
	if registry == nil && dbengine.DefaultRegistry != nil {
		registry = dbengine.DefaultRegistry()
	}
	if registry == nil {
		return nil, engineID, fmt.Errorf("%w: no database engine registry is configured", databases.ErrUnsupported)
	}
	engine, err := registry.For(engineID)
	if err != nil {
		return nil, engineID, fmt.Errorf("%w: %v", databases.ErrUnsupported, err)
	}
	return engine, engineID, nil
}

// databasePort returns the loopback host port published for a service.
func (c *DockerServiceController) databasePort(serviceID string) (int, error) {
	svc, err := c.service(services.ID(serviceID))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	for _, port := range svc.Ports {
		if port.Host > 0 {
			return port.Host, nil
		}
	}
	return 0, fmt.Errorf("%w: service %q has no published port", databases.ErrUnsupported, serviceID)
}

// requireActive verifies that the service container is running. Database
// mutations and inventory reads require an active service.
func (c *DockerServiceController) requireActive(ctx context.Context, serviceID string) error {
	svc, err := c.service(services.ID(serviceID))
	if err != nil {
		return fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	out, err := c.runCompose(ctx, "ps", "-a", "--format", "json")
	if err != nil {
		return fmt.Errorf("inspect service %q: %w", serviceID, err)
	}
	containers, err := containersByService(out)
	if err != nil {
		return fmt.Errorf("inspect service %q: %w", serviceID, err)
	}
	cont, ok := containers[string(svc.ID)]
	if !ok || !strings.EqualFold(cont.State, "running") {
		return fmt.Errorf("%w: service %q is not active", databases.ErrServiceInactive, serviceID)
	}
	health := strings.ToLower(cont.Health)
	if health == "unhealthy" || health == "starting" || (health == "" && svc.Healthcheck != nil) {
		return fmt.Errorf("%w: service %q is not healthy", databases.ErrServiceInactive, serviceID)
	}
	return nil
}

// composeDatabase prefixes a fixed docker compose exec command for a database
// service. The helper's project name is devstack-<root-hash>; it never targets
// the agent's fixed project.
func (c *DockerServiceController) composeDatabase(ctx context.Context, serviceID string, args ...string) ([]byte, error) {
	if _, err := c.service(services.ID(serviceID)); err != nil {
		return nil, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	base := []string{"exec", "-T", serviceID}
	return c.runCompose(ctx, append(base, args...)...)
}

func (c *DockerServiceController) composeDatabaseInput(ctx context.Context, serviceID string, input io.Reader, args ...string) error {
	if _, err := c.service(services.ID(serviceID)); err != nil {
		return fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	runner, ok := c.runner.(InputStreamCommandRunner)
	if !ok {
		return fmt.Errorf("%w: command runner does not support stdin streams", databases.ErrUnsupported)
	}
	base := []string{"compose", "-p", c.projectName, "-f", c.composePath, "exec", "-T", serviceID}
	return runner.RunInputStream(ctx, input, "docker", append(base, args...)...)
}

func (c *DockerServiceController) composeDatabaseStream(ctx context.Context, serviceID string, args ...string) (io.ReadCloser, error) {
	if _, err := c.service(services.ID(serviceID)); err != nil {
		return nil, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	runner, ok := c.runner.(StreamCommandRunner)
	if !ok {
		return nil, fmt.Errorf("%w: command runner does not support output streams", databases.ErrUnsupported)
	}
	base := []string{"compose", "-p", c.projectName, "-f", c.composePath, "exec", "-T", serviceID}
	return runner.RunStream(ctx, "docker", append(base, args...)...)
}

// sqlClientCommand returns the fixed SQL client for a service. The client reads
// SQL from stdin so passwords and identifiers never appear in argv.
func (c *DockerServiceController) sqlClientCommand(serviceID string) ([]string, error) {
	return c.sqlClientCommandForDatabase(serviceID, "")
}

func (c *DockerServiceController) sqlClientCommandForDatabase(serviceID, database string) ([]string, error) {
	switch services.ID(serviceID) {
	case services.IDMySQL:
		return []string{"mysql", "--user=root", "--batch", "--skip-column-names"}, nil
	case services.IDMariaDB:
		return []string{"mariadb", "--user=root", "--batch", "--skip-column-names"}, nil
	case services.IDPostgreSQL:
		args := []string{"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1", "--username=postgres"}
		if database == "" {
			args = append(args, "--dbname=postgres")
		} else {
			args = append(args, "--dbname="+database)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("%w: service %q is not a SQL service", databases.ErrUnsupported, serviceID)
	}
}

// sqlCommandWithEnv wraps a SQL command so the root password is read from the
// container's mounted secret into the client's environment. The fixed shell
// script interpolates nothing; the client argv is passed as positional
// parameters.
func (c *DockerServiceController) sqlCommandWithEnv(serviceID string, argv []string) ([]string, error) {
	var secretPath string
	switch services.ID(serviceID) {
	case services.IDMySQL:
		secretPath = "/run/secrets/mysql_root_password"
	case services.IDMariaDB:
		secretPath = "/run/secrets/mariadb_root_password"
	case services.IDPostgreSQL:
		return argv, nil
	default:
		return nil, fmt.Errorf("%w: service %q is not a SQL service", databases.ErrUnsupported, serviceID)
	}
	script := fmt.Sprintf(`set -eu; MYSQL_PWD="$(cat %s)"; export MYSQL_PWD; exec "$@"`, secretPath)
	return append([]string{"sh", "-c", script, "sh"}, argv...), nil
}

// runDatabaseSQL executes SQL through the service's fixed client with the SQL
// text on stdin.
func (c *DockerServiceController) runDatabaseSQL(ctx context.Context, serviceID, sql string) ([]byte, error) {
	client, err := c.sqlClientCommand(serviceID)
	if err != nil {
		return nil, err
	}
	wrapped, err := c.sqlCommandWithEnv(serviceID, client)
	if err != nil {
		return nil, err
	}
	args := append([]string{"exec", "-T", serviceID}, wrapped...)
	return c.runComposeInput(ctx, []byte(sql), args...)
}

// runDatabaseSQLIn executes SQL against a specific database (PostgreSQL needs
// a database connection for REASSIGN OWNED).
func (c *DockerServiceController) runDatabaseSQLIn(ctx context.Context, serviceID, database, sql string) ([]byte, error) {
	client, err := c.sqlClientCommandForDatabase(serviceID, database)
	if err != nil {
		return nil, err
	}
	wrapped, err := c.sqlCommandWithEnv(serviceID, client)
	if err != nil {
		return nil, err
	}
	args := append([]string{"exec", "-T", serviceID}, wrapped...)
	return c.runComposeInput(ctx, []byte(sql), args...)
}

// runEngineCommand executes an engine-provided fixed command with no stdin.
func (c *DockerServiceController) runEngineCommand(ctx context.Context, serviceID string, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("%w: engine returned an empty command", databases.ErrUnsupported)
	}
	wrapped, err := c.sqlCommandWithEnv(serviceID, argv)
	if err != nil {
		return nil, err
	}
	return c.composeDatabase(ctx, serviceID, wrapped...)
}

// runEngineInput executes an engine-provided fixed command with a stream on
// stdin (restores and imports).
func (c *DockerServiceController) runEngineInput(ctx context.Context, serviceID string, input io.Reader, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("%w: engine returned an empty command", databases.ErrUnsupported)
	}
	wrapped, err := c.sqlCommandWithEnv(serviceID, argv)
	if err != nil {
		return err
	}
	return c.composeDatabaseInput(ctx, serviceID, input, wrapped...)
}

// runEngineStream executes an engine-provided fixed command and streams stdout
// (dumps and exports).
func (c *DockerServiceController) runEngineStream(ctx context.Context, serviceID string, argv []string) (io.ReadCloser, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("%w: engine returned an empty command", databases.ErrUnsupported)
	}
	wrapped, err := c.sqlCommandWithEnv(serviceID, argv)
	if err != nil {
		return nil, err
	}
	return c.composeDatabaseStream(ctx, serviceID, wrapped...)
}

// invalidIdentifier wraps a dbengine validation error with the domain error so
// callers can match either sentinel.
func invalidIdentifier(err error) error {
	return fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err)
}

// notImplementedJob reports that the helper runs database jobs synchronously
// and does not keep a job registry.
func notImplementedJob(jobID string) error {
	return fmt.Errorf("%w: job %q", databases.ErrJobNotFound, jobID)
}
