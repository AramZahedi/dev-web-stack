package agentdocker

import (
	"context"
	"fmt"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ListDatabases returns the engine inventory. It requires an active service
// because the inventory is read through the running container.
func (d *Docker) ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() == model.DatabaseRedis {
		return nil, databases.ErrUnsupported
	}
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return nil, err
	}
	command, err := engine.ListDatabasesCommand()
	if err != nil {
		return nil, mapEngineError(err)
	}
	output, err := d.runDatabaseSQL(ctx, serviceID, command, nil)
	if err != nil {
		return nil, err
	}
	databasesList, err := engine.ParseDatabases(output)
	if err != nil {
		return nil, err
	}
	for i := range databasesList {
		databasesList[i].ServiceID = string(service.ID)
		databasesList[i].Engine = engine.ID()
	}
	return databasesList, nil
}

// CreateDatabase creates a database after validating the identifier and the
// protected-name list. Validation happens before any Docker command.
func (d *Docker) CreateDatabase(ctx context.Context, serviceID string, req databases.CreateDatabaseRequest) (model.Database, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.Database{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return model.Database{}, databases.ErrUnsupported
	}
	if err := engine.ValidateDatabaseName(req.Name); err != nil {
		return model.Database{}, mapEngineError(err)
	}
	if engine.ProtectedDatabase(req.Name) {
		return model.Database{}, databases.ErrProtected
	}
	statement, err := engine.CreateDatabaseSQL(req.Name, req.Charset, req.Collation)
	if err != nil {
		return model.Database{}, mapEngineError(err)
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return model.Database{}, err
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.Database{}, err
	}
	if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
		return model.Database{}, err
	}
	return model.Database{
		Name:      req.Name,
		ServiceID: string(service.ID),
		Engine:    engine.ID(),
		Managed:   true,
	}, nil
}

// RenameDatabase renames a PostgreSQL database. MySQL/MariaDB reject the
// operation before any status check or Docker command.
func (d *Docker) RenameDatabase(ctx context.Context, serviceID, name string, req databases.RenameDatabaseRequest) (model.Database, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.Database{}, err
	}
	if !engine.RenameSupported() {
		return model.Database{}, dbengine.ErrUnsupported
	}
	if err := engine.ValidateDatabaseName(name); err != nil {
		return model.Database{}, mapEngineError(err)
	}
	if err := engine.ValidateDatabaseName(req.Name); err != nil {
		return model.Database{}, mapEngineError(err)
	}
	if engine.ProtectedDatabase(name) || engine.ProtectedDatabase(req.Name) {
		return model.Database{}, databases.ErrProtected
	}
	statement, err := engine.RenameDatabaseSQL(name, req.Name)
	if err != nil {
		return model.Database{}, mapEngineError(err)
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return model.Database{}, err
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.Database{}, err
	}
	if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
		return model.Database{}, err
	}
	return model.Database{
		Name:      req.Name,
		ServiceID: string(service.ID),
		Engine:    engine.ID(),
		Managed:   true,
	}, nil
}

// DropDatabase terminates target sessions and drops the database. The daemon
// orchestrates a pre-drop backup; the agent never creates one implicitly.
func (d *Docker) DropDatabase(ctx context.Context, serviceID, name string, req databases.DropDatabaseRequest) error {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	if engine.ID() == model.DatabaseRedis {
		return databases.ErrUnsupported
	}
	if err := engine.ValidateDatabaseName(name); err != nil {
		return mapEngineError(err)
	}
	if engine.ProtectedDatabase(name) {
		return databases.ErrProtected
	}
	if req.Confirm != name {
		return fmt.Errorf("%w: confirmation does not match %q", databases.ErrInvalidRequest, name)
	}
	statement, err := engine.DropDatabaseSQL(name)
	if err != nil {
		return mapEngineError(err)
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return err
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return err
	}
	if err := d.terminateDatabaseSessions(ctx, serviceID, engine, name); err != nil {
		return err
	}
	if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
		return err
	}
	return nil
}
