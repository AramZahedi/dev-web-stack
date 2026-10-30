package agentdocker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// databaseUserID derives a stable opaque ID from the engine identity. The same
// user discovered later must map to the same devstack record.
func databaseUserID(serviceID, username, host string) string {
	sum := sha256.Sum256([]byte(serviceID + "\x00" + username + "\x00" + host))
	return "usr_" + hex.EncodeToString(sum[:8])
}

func normalizeDatabaseHost(engine model.DatabaseEngine, host string) string {
	host = strings.TrimSpace(host)
	if host == "" && (engine == model.DatabaseMySQL || engine == model.DatabaseMariaDB) {
		return "%"
	}
	return host
}

func (d *Docker) ListUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() == model.DatabaseRedis {
		return d.ListRedisUsers(ctx, serviceID)
	}
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return nil, err
	}
	command, err := engine.ListUsersCommand()
	if err != nil {
		return nil, mapEngineError(err)
	}
	output, err := d.runDatabaseSQL(ctx, serviceID, command, nil)
	if err != nil {
		return nil, err
	}
	users, err := engine.ParseUsers(output)
	if err != nil {
		return nil, err
	}
	for i := range users {
		users[i].ServiceID = string(service.ID)
		users[i].Engine = engine.ID()
		users[i].Host = normalizeDatabaseHost(engine.ID(), users[i].Host)
		users[i].ID = databaseUserID(string(service.ID), users[i].Username, users[i].Host)
		managed, err := d.databaseSecretExists(serviceID, users[i].ID)
		if err != nil {
			return nil, err
		}
		users[i].PasswordManaged = managed
		grants, err := d.listUserGrants(ctx, serviceID, engine, users[i].Username, users[i].Host)
		if err != nil {
			return nil, err
		}
		users[i].Grants = grants
	}
	return users, nil
}

func (d *Docker) listUserGrants(ctx context.Context, serviceID string, engine dbengine.Engine, username, host string) ([]model.DatabaseGrant, error) {
	command, err := engine.ListGrantsCommand(username, host)
	if err != nil {
		return nil, err
	}
	output, err := d.runDatabaseSQL(ctx, serviceID, command, nil)
	if err != nil {
		return nil, err
	}
	return engine.ParseGrants(output)
}

func (d *Docker) CreateUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return d.SetRedisUser(ctx, serviceID, req)
	}
	if err := engine.ValidateUsername(req.Username); err != nil {
		return model.DatabaseUser{}, mapEngineError(err)
	}
	host := normalizeDatabaseHost(engine.ID(), req.Host)
	if err := engine.ValidateHost(host); err != nil {
		return model.DatabaseUser{}, mapEngineError(err)
	}
	if engine.ProtectedUser(req.Username, host) {
		return model.DatabaseUser{}, databases.ErrProtected
	}
	for _, grant := range req.Grants {
		if err := engine.ValidateDatabaseName(grant.Database); err != nil {
			return model.DatabaseUser{}, mapEngineError(err)
		}
		if engine.ProtectedDatabase(grant.Database) {
			return model.DatabaseUser{}, databases.ErrProtected
		}
	}
	password, err := credentials.NewSecret()
	if err != nil {
		return model.DatabaseUser{}, fmt.Errorf("agentdocker: generate database password: %w", err)
	}
	statement, err := engine.CreateUserSQL(req.Username, host, password)
	if err != nil {
		return model.DatabaseUser{}, mapEngineError(err)
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return model.DatabaseUser{}, err
	}
	userID := databaseUserID(string(service.ID), req.Username, host)
	user := model.DatabaseUser{
		ID:              userID,
		ServiceID:       string(service.ID),
		Engine:          engine.ID(),
		Username:        req.Username,
		Host:            host,
		Enabled:         true,
		Managed:         true,
		PasswordManaged: true,
		Grants:          req.Grants,
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
		return model.DatabaseUser{}, err
	}
	if err := d.storeDatabaseSecret(serviceID, userID, password); err != nil {
		d.rollbackCreatedUser(ctx, serviceID, engine, command, req.Username, host, userID)
		return model.DatabaseUser{}, fmt.Errorf("agentdocker: database user created but its password could not be stored: %w", err)
	}
	for _, grant := range req.Grants {
		grantStatement, err := engine.GrantSQL(req.Username, host, grant.Database, grant)
		if err != nil {
			d.rollbackCreatedUser(ctx, serviceID, engine, command, req.Username, host, userID)
			return model.DatabaseUser{}, mapEngineError(err)
		}
		grantCommand, err := databaseSQLCommandFor(engine.ID(), grant.Database)
		if err != nil {
			d.rollbackCreatedUser(ctx, serviceID, engine, command, req.Username, host, userID)
			return model.DatabaseUser{}, mapEngineError(err)
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, grantCommand, []byte(grantStatement)); err != nil {
			d.rollbackCreatedUser(ctx, serviceID, engine, command, req.Username, host, userID)
			return model.DatabaseUser{}, err
		}
	}
	return user, nil
}

func (d *Docker) rollbackCreatedUser(ctx context.Context, serviceID string, engine dbengine.Engine, command []string, username, host, userID string) {
	statement, err := engine.DeleteUserSQL(username, host)
	if err != nil {
		return
	}
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = d.runDatabaseSQL(rollbackCtx, serviceID, command, []byte(statement))
	_ = d.deleteDatabaseSecret(serviceID, userID)
}

func (d *Docker) UpdateUser(ctx context.Context, serviceID, userID string, req databases.UpdateUserRequest) (model.DatabaseUser, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return d.UpdateRedisUser(ctx, serviceID, userID, req)
	}
	for _, grant := range req.Grants {
		if err := engine.ValidateDatabaseName(grant.Database); err != nil {
			return model.DatabaseUser{}, mapEngineError(err)
		}
		if engine.ProtectedDatabase(grant.Database) {
			return model.DatabaseUser{}, databases.ErrProtected
		}
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return model.DatabaseUser{}, err
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	user, err := d.findDatabaseUser(ctx, serviceID, userID)
	if err != nil {
		if errors.Is(err, databases.ErrNotFound) && d.isProtectedUserID(engine, string(service.ID), userID) {
			return model.DatabaseUser{}, databases.ErrProtected
		}
		return model.DatabaseUser{}, err
	}
	if engine.ProtectedUser(user.Username, user.Host) {
		if req.Enabled != nil || req.Grants != nil {
			return model.DatabaseUser{}, databases.ErrProtected
		}
	}
	if req.Enabled != nil {
		statement, err := databaseSetEnabledSQL(engine, user.Username, user.Host, *req.Enabled)
		if err != nil {
			return model.DatabaseUser{}, mapEngineError(err)
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
			return model.DatabaseUser{}, err
		}
	}
	if req.Grants != nil {
		existing := user.Grants
		for _, existingGrant := range existing {
			statement, err := engine.RevokeSQL(user.Username, user.Host, existingGrant.Database, model.DatabaseGrant{
				Database: existingGrant.Database,
				Preset:   model.GrantAdmin,
			})
			if err != nil {
				d.restoreUserGrants(ctx, serviceID, engine, user.Username, user.Host, existing)
				return model.DatabaseUser{}, mapEngineError(err)
			}
			grantCommand, err := databaseSQLCommandFor(engine.ID(), existingGrant.Database)
			if err != nil {
				d.restoreUserGrants(ctx, serviceID, engine, user.Username, user.Host, existing)
				return model.DatabaseUser{}, mapEngineError(err)
			}
			if _, err := d.runDatabaseSQL(ctx, serviceID, grantCommand, []byte(statement)); err != nil {
				d.restoreUserGrants(ctx, serviceID, engine, user.Username, user.Host, existing)
				return model.DatabaseUser{}, err
			}
		}
		applied := make([]model.DatabaseGrant, 0, len(req.Grants))
		for _, grant := range req.Grants {
			statement, err := engine.GrantSQL(user.Username, user.Host, grant.Database, grant)
			if err != nil {
				d.rollbackGrantUpdate(ctx, serviceID, engine, user.Username, user.Host, existing, applied)
				return model.DatabaseUser{}, mapEngineError(err)
			}
			grantCommand, err := databaseSQLCommandFor(engine.ID(), grant.Database)
			if err != nil {
				d.rollbackGrantUpdate(ctx, serviceID, engine, user.Username, user.Host, existing, applied)
				return model.DatabaseUser{}, mapEngineError(err)
			}
			if _, err := d.runDatabaseSQL(ctx, serviceID, grantCommand, []byte(statement)); err != nil {
				d.rollbackGrantUpdate(ctx, serviceID, engine, user.Username, user.Host, existing, applied)
				return model.DatabaseUser{}, err
			}
			applied = append(applied, grant)
		}
	}
	return d.findDatabaseUser(ctx, serviceID, userID)
}

func (d *Docker) rollbackGrantUpdate(ctx context.Context, serviceID string, engine dbengine.Engine, username, host string, existing, applied []model.DatabaseGrant) {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, grant := range applied {
		statement, err := engine.RevokeSQL(username, host, grant.Database, model.DatabaseGrant{
			Database: grant.Database,
			Preset:   model.GrantAdmin,
		})
		if err != nil {
			continue
		}
		command, err := databaseSQLCommandFor(engine.ID(), grant.Database)
		if err != nil {
			continue
		}
		_, _ = d.runDatabaseSQL(rollbackCtx, serviceID, command, []byte(statement))
	}
	d.restoreUserGrants(rollbackCtx, serviceID, engine, username, host, existing)
}

func (d *Docker) restoreUserGrants(ctx context.Context, serviceID string, engine dbengine.Engine, username, host string, grants []model.DatabaseGrant) {
	for _, grant := range grants {
		statement, err := engine.GrantSQL(username, host, grant.Database, grant)
		if err != nil {
			continue
		}
		command, err := databaseSQLCommandFor(engine.ID(), grant.Database)
		if err != nil {
			continue
		}
		_, _ = d.runDatabaseSQL(ctx, serviceID, command, []byte(statement))
	}
}

func (d *Docker) DeleteUser(ctx context.Context, serviceID, userID string, req databases.DeleteUserRequest) error {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	if engine.ID() == model.DatabaseRedis {
		return d.DeleteRedisUser(ctx, serviceID, userID, req.Confirm)
	}
	user, err := d.findDatabaseUser(ctx, serviceID, userID)
	if err != nil {
		if errors.Is(err, databases.ErrNotFound) && d.isProtectedUserID(engine, string(service.ID), userID) {
			return databases.ErrProtected
		}
		return err
	}
	if engine.ProtectedUser(user.Username, user.Host) {
		return databases.ErrProtected
	}
	if req.Confirm != user.Username {
		return fmt.Errorf("%w: confirmation does not match %q", databases.ErrInvalidRequest, user.Username)
	}
	statement, err := engine.DeleteUserSQL(user.Username, user.Host)
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
	if req.ReassignOwned && engine.ID() == model.DatabasePostgreSQL {
		if err := d.reassignOwnedObjects(ctx, serviceID, user.Username); err != nil {
			return err
		}
	}
	if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
		return err
	}
	if err := d.deleteDatabaseSecret(serviceID, userID); err != nil {
		return fmt.Errorf("agentdocker: user deleted but its secret could not be removed: %w", err)
	}
	return nil
}

func (d *Docker) SetUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return databases.Secret{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		defaultID := databaseUserID(string(service.ID), "default", "")
		if userID == defaultID {
			return d.SetRedisPassword(ctx, serviceID)
		}
		return d.SetRedisUserPassword(ctx, serviceID, userID)
	}
	user, err := d.findDatabaseUser(ctx, serviceID, userID)
	if err != nil {
		if errors.Is(err, databases.ErrNotFound) && d.isProtectedUserID(engine, string(service.ID), userID) {
			return databases.Secret{}, databases.ErrProtected
		}
		return databases.Secret{}, err
	}
	if engine.ProtectedUser(user.Username, user.Host) {
		return databases.Secret{}, databases.ErrProtected
	}
	password, err := credentials.NewSecret()
	if err != nil {
		return databases.Secret{}, fmt.Errorf("agentdocker: generate database password: %w", err)
	}
	statement, err := engine.SetPasswordSQL(user.Username, user.Host, password)
	if err != nil {
		return databases.Secret{}, mapEngineError(err)
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return databases.Secret{}, err
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return databases.Secret{}, err
	}
	if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
		return databases.Secret{}, err
	}
	if err := d.storeDatabaseSecret(serviceID, userID, password); err != nil {
		// The engine now has a password we could not persist. Remove the old
		// secret so reveal fails closed instead of returning a stale password.
		_ = d.deleteDatabaseSecret(serviceID, userID)
		return databases.Secret{}, fmt.Errorf("agentdocker: database password changed but the new secret could not be stored: %w", err)
	}
	return databases.Secret{Value: password}, nil
}

func (d *Docker) RevealUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	engine, _, err := d.databaseEngine(serviceID)
	if err != nil {
		return databases.Secret{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return d.RevealRedisPassword(ctx, serviceID, userID)
	}
	value, err := d.readDatabaseSecret(serviceID, userID)
	if err != nil {
		return databases.Secret{}, err
	}
	return databases.Secret{Value: value}, nil
}

func (d *Docker) findDatabaseUser(ctx context.Context, serviceID, userID string) (model.DatabaseUser, error) {
	users, err := d.ListUsers(ctx, serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	for _, user := range users {
		if user.ID == userID {
			return user, nil
		}
	}
	return model.DatabaseUser{}, databases.ErrNotFound
}

func (d *Docker) isProtectedUserID(engine dbengine.Engine, serviceID, userID string) bool {
	hosts := []string{"", "%", "localhost", "127.0.0.1", "::1"}
	for username := range dbengine.SystemUsers[engine.ID()] {
		for _, host := range hosts {
			if databaseUserID(serviceID, username, host) == userID {
				return true
			}
		}
	}
	return false
}

func databaseSetEnabledSQL(engine dbengine.Engine, username, host string, enabled bool) (string, error) {
	if err := dbengine.ValidateExistingUsername(engine.ID(), username); err != nil {
		return "", err
	}
	if err := engine.ValidateHost(host); err != nil {
		return "", err
	}
	switch engine.ID() {
	case model.DatabaseMySQL, model.DatabaseMariaDB:
		user := dbengine.QuoteLiteral(username) + "@" + dbengine.QuoteLiteral(host)
		if enabled {
			return fmt.Sprintf("ALTER USER %s ACCOUNT UNLOCK;", user), nil
		}
		return fmt.Sprintf("ALTER USER %s ACCOUNT LOCK;", user), nil
	case model.DatabasePostgreSQL:
		quoted, err := dbengine.QuoteIdentifier(engine.ID(), username)
		if err != nil {
			return "", err
		}
		if enabled {
			return fmt.Sprintf("ALTER ROLE %s WITH LOGIN;", quoted), nil
		}
		return fmt.Sprintf("ALTER ROLE %s WITH NOLOGIN;", quoted), nil
	default:
		return "", dbengine.ErrUnsupported
	}
}

func (d *Docker) reassignOwnedObjects(ctx context.Context, serviceID, username string) error {
	user, err := dbengine.QuoteExistingIdentifier(model.DatabasePostgreSQL, username)
	if err != nil {
		return err
	}
	owner, err := dbengine.QuoteExistingIdentifier(model.DatabasePostgreSQL, "devstack")
	if err != nil {
		return err
	}
	databasesList, err := d.ListDatabases(ctx, serviceID)
	if err != nil {
		return err
	}
	statement := fmt.Sprintf("REASSIGN OWNED BY %s TO %s;", user, owner)
	for _, database := range databasesList {
		if database.Name == "template0" || database.Name == "template1" {
			continue
		}
		command := []string{
			"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1",
			"--username=postgres", "--dbname", database.Name,
			"--tuples-only", "--no-align",
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(statement)); err != nil {
			return fmt.Errorf("agentdocker: reassign objects in %s: %w", database.Name, err)
		}
	}
	return nil
}
