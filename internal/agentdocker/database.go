package agentdocker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// databaseLock serializes mutations for one service. Long-running database
// streams deliberately never take the global opMu.
func (d *Docker) databaseLock(serviceID string) *sync.Mutex {
	value, _ := d.databaseLocks.LoadOrStore(serviceID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// databaseEngine resolves a fixed database service and its engine.
func (d *Docker) databaseEngine(serviceID string) (dbengine.Engine, services.Service, error) {
	service, err := d.service(serviceID)
	if err != nil {
		return nil, services.Service{}, err
	}
	engineID, ok := databaseEngineID(service.ID)
	if !ok {
		return nil, service, fmt.Errorf("agentdocker: %s is not a database service", service.ID)
	}
	registry := d.databaseRegistry
	if registry == nil && dbengine.DefaultRegistry != nil {
		registry = dbengine.DefaultRegistry()
	}
	if registry == nil {
		return nil, service, fmt.Errorf("%w: no database engine registry", dbengine.ErrUnsupported)
	}
	engine, err := registry.For(engineID)
	if err != nil {
		return nil, service, err
	}
	return engine, service, nil
}

func databaseEngineID(id services.ID) (model.DatabaseEngine, bool) {
	switch id {
	case services.IDMySQL:
		return model.DatabaseMySQL, true
	case services.IDMariaDB:
		return model.DatabaseMariaDB, true
	case services.IDPostgreSQL, "postgres":
		return model.DatabasePostgreSQL, true
	case services.IDRedis:
		return model.DatabaseRedis, true
	default:
		return "", false
	}
}

// requireDatabaseActive rejects engine mutations while the service container
// is stopped. Backup listing/download/deletion intentionally do not call this.
func (d *Docker) requireDatabaseActive(ctx context.Context, serviceID string) error {
	status, err := d.Status(ctx, serviceID)
	if err != nil {
		return fmt.Errorf("agentdocker: check %s status: %w", serviceID, err)
	}
	if status.State != "active" {
		return fmt.Errorf("%w: service %s is %s", databases.ErrServiceInactive, serviceID, status.State)
	}
	return nil
}

func (d *Docker) databaseComposeArgs(service services.Service, engine dbengine.Engine, command []string) []string {
	args := []string{
		"compose", "--project-name", projectName, "--file", d.composePath,
		"exec", "-T", string(service.ID), "sh", "-c", databaseShellWrapper(engine.ID()), "sh",
	}
	return append(args, command...)
}

// databaseShellWrapper reads the service's fixed root credential from the
// container secret mount into the client's environment. Secrets never appear in
// argv and user input is always passed as an argument, never interpolated into
// this fixed script.
func databaseShellWrapper(engine model.DatabaseEngine) string {
	switch engine {
	case model.DatabaseMySQL:
		return `set -eu; MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"; export MYSQL_PWD; exec "$@"`
	case model.DatabaseMariaDB:
		return `set -eu; MYSQL_PWD="$(cat /run/secrets/mariadb_root_password)"; export MYSQL_PWD; exec "$@"`
	case model.DatabasePostgreSQL:
		return `set -eu; PGPASSWORD="$(cat /run/secrets/postgresql_postgres_password)"; export PGPASSWORD; exec "$@"`
	default:
		return `set -eu; exec "$@"`
	}
}

func (d *Docker) composeExecStream(ctx context.Context, serviceID string, command []string) (io.ReadCloser, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	runner, ok := d.runner.(StreamRunner)
	if !ok {
		return nil, errors.New("agentdocker: runner does not support streamed output")
	}
	return runner.RunStream(ctx, "docker", d.databaseComposeArgs(service, engine, command)...)
}

func (d *Docker) composeExecInput(ctx context.Context, serviceID string, input io.Reader, command []string) error {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	runner, ok := d.runner.(InputStreamRunner)
	if !ok {
		return errors.New("agentdocker: runner does not support streamed input")
	}
	return runner.RunInputStream(ctx, input, "docker", d.databaseComposeArgs(service, engine, command)...)
}

func (d *Docker) composeExecSQL(ctx context.Context, serviceID string, input []byte, command []string) ([]byte, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	runner, ok := d.runner.(InputRunner)
	if !ok {
		return nil, errors.New("agentdocker: runner does not support stdin")
	}
	return runner.RunInput(ctx, input, "docker", d.databaseComposeArgs(service, engine, command)...)
}
