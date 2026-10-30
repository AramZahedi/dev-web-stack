//go:build !windows

package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func (c *DockerServiceController) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// databaseLock returns the per-service mutation lock. Database streams never
// hold the global operation lock.
func (c *DockerServiceController) databaseLock(serviceID string) *sync.Mutex {
	value, _ := c.databaseLocks.LoadOrStore(serviceID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// ListDatabases returns the engine inventory for an active SQL service.
func (c *DockerServiceController) ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, fmt.Errorf("%w: %s does not expose logical databases", databases.ErrUnsupported, engineID)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	command, err := engine.ListDatabasesCommand()
	if err != nil {
		return nil, engineError(err)
	}
	out, err := c.runEngineCommand(ctx, serviceID, command)
	if err != nil {
		return nil, err
	}
	list, err := engine.ParseDatabases(out)
	if err != nil {
		return nil, engineError(err)
	}
	if list == nil {
		list = []model.Database{}
	}
	for i := range list {
		list[i].ServiceID = serviceID
		list[i].Engine = engineID
	}
	return list, nil
}

// CreateDatabase creates a database on an active SQL service.
func (c *DockerServiceController) CreateDatabase(ctx context.Context, serviceID string, req databases.CreateDatabaseRequest) (model.Database, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return model.Database{}, err
	}
	if engine == nil {
		return model.Database{}, fmt.Errorf("%w: %s does not support databases", databases.ErrUnsupported, engineID)
	}
	if err := engine.ValidateDatabaseName(req.Name); err != nil {
		return model.Database{}, invalidIdentifier(err)
	}
	if engine.ProtectedDatabase(req.Name) {
		return model.Database{}, fmt.Errorf("%w: database %q", databases.ErrProtected, req.Name)
	}
	for _, value := range []string{req.Charset, req.Collation} {
		if value != "" {
			if err := dbengine.ValidateIdentifier(value); err != nil {
				return model.Database{}, invalidIdentifier(err)
			}
		}
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.Database{}, err
	}
	sql, err := engine.CreateDatabaseSQL(req.Name, req.Charset, req.Collation)
	if err != nil {
		return model.Database{}, engineError(err)
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
		return model.Database{}, err
	}
	return model.Database{
		Name:      req.Name,
		ServiceID: serviceID,
		Engine:    engineID,
		Managed:   true,
		CreatedAt: c.now(),
	}, nil
}

// RenameDatabase renames a PostgreSQL database. MySQL/MariaDB fail before any
// command is built.
func (c *DockerServiceController) RenameDatabase(ctx context.Context, serviceID, name string, req databases.RenameDatabaseRequest) (model.Database, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return model.Database{}, err
	}
	if engine == nil {
		return model.Database{}, fmt.Errorf("%w: %s does not support rename", databases.ErrUnsupported, engineID)
	}
	if !engine.RenameSupported() {
		return model.Database{}, fmt.Errorf("%w: %w: %s does not support database rename", databases.ErrUnsupported, dbengine.ErrUnsupported, engineID)
	}
	if err := engine.ValidateDatabaseName(name); err != nil {
		return model.Database{}, invalidIdentifier(err)
	}
	if err := engine.ValidateDatabaseName(req.Name); err != nil {
		return model.Database{}, invalidIdentifier(err)
	}
	if engine.ProtectedDatabase(name) || engine.ProtectedDatabase(req.Name) {
		return model.Database{}, fmt.Errorf("%w: database rename", databases.ErrProtected)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.Database{}, err
	}
	sql, err := engine.RenameDatabaseSQL(name, req.Name)
	if err != nil {
		return model.Database{}, engineError(err)
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
		return model.Database{}, err
	}
	return model.Database{
		Name:      req.Name,
		ServiceID: serviceID,
		Engine:    engineID,
		Managed:   true,
		CreatedAt: c.now(),
	}, nil
}

// DropDatabase terminates target sessions and drops a database after typed
// confirmation. The daemon orchestrates a pre-drop backup when requested.
func (c *DockerServiceController) DropDatabase(ctx context.Context, serviceID, name string, req databases.DropDatabaseRequest) error {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	if engine == nil {
		return fmt.Errorf("%w: %s does not support databases", databases.ErrUnsupported, engineID)
	}
	if req.Confirm != name {
		return fmt.Errorf("%w: confirmation does not match database %q", databases.ErrInvalidRequest, name)
	}
	if err := engine.ValidateDatabaseName(name); err != nil {
		return invalidIdentifier(err)
	}
	if engine.ProtectedDatabase(name) {
		return fmt.Errorf("%w: database %q", databases.ErrProtected, name)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if err := c.terminateSessions(ctx, serviceID, engine, name); err != nil {
		return err
	}
	sql, err := engine.DropDatabaseSQL(name)
	if err != nil {
		return engineError(err)
	}
	_, err = c.runDatabaseSQL(ctx, serviceID, sql)
	return err
}

func (c *DockerServiceController) terminateSessions(ctx context.Context, serviceID string, engine dbengine.Engine, database string) error {
	sql, err := engine.TerminateSessionsSQL(database)
	if err != nil {
		return engineError(err)
	}
	if strings.TrimSpace(sql) == "" {
		return nil
	}
	out, err := c.runDatabaseSQLIn(ctx, serviceID, database, sql)
	if err != nil {
		return err
	}
	engineID := engine.ID()
	if engineID != model.DatabaseMySQL && engineID != model.DatabaseMariaDB {
		return nil
	}
	// MySQL/MariaDB return SELECT rows containing the KILL statements. The
	// helper must execute each one, exactly like the agent adapter.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(line), "KILL ") {
			return fmt.Errorf("%w: unexpected session termination output %q", databases.ErrInvalidRequest, line)
		}
		statement := line
		if !strings.HasSuffix(statement, ";") {
			statement += ";"
		}
		if _, err := c.runDatabaseSQL(ctx, serviceID, statement); err != nil {
			return err
		}
	}
	return nil
}

// ListUsers returns the engine user inventory. Redis users are read directly
// from ACL LIST because the frozen Redis engine contract cannot expose them.
func (c *DockerServiceController) ListUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engineID == model.DatabaseRedis {
		return c.listRedisUsers(ctx, serviceID)
	}
	if engine == nil {
		return nil, fmt.Errorf("%w: %s does not expose users", databases.ErrUnsupported, engineID)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	command, err := engine.ListUsersCommand()
	if err != nil {
		return nil, err
	}
	out, err := c.runEngineCommand(ctx, serviceID, command)
	if err != nil {
		return nil, err
	}
	users, err := engine.ParseUsers(out)
	if err != nil {
		return nil, err
	}
	if users == nil {
		users = []model.DatabaseUser{}
	}
	for i := range users {
		host := users[i].Host
		if host == "" && (engineID == model.DatabaseMySQL || engineID == model.DatabaseMariaDB) {
			host = "%"
		}
		users[i].Host = host
		if users[i].ID == "" {
			users[i].ID = databaseUserID(serviceID, engineID, users[i].Username, host)
		}
		users[i].ServiceID = serviceID
		users[i].Engine = engineID
		users[i].PasswordManaged = c.databaseSecretExists(serviceID, users[i].ID)
		if users[i].PasswordManaged {
			users[i].Managed = true
		}
		// Grants are a best-effort enrichment; a failure for one system user
		// must not make the whole inventory unavailable.
		if command, grantErr := engine.ListGrantsCommand(users[i].Username, users[i].Host); grantErr == nil {
			if grantOut, runErr := c.runEngineCommand(ctx, serviceID, command); runErr == nil {
				if grants, parseErr := engine.ParseGrants(grantOut); parseErr == nil {
					users[i].Grants = grants
				}
			}
		}
	}
	return users, nil
}

// CreateUser creates an engine user with a generated password. The password is
// sent on stdin and then stored atomically under the helper secret tree.
func (c *DockerServiceController) CreateUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if engineID == model.DatabaseRedis {
		return c.setRedisUser(ctx, serviceID, req)
	}
	if engine == nil {
		return model.DatabaseUser{}, fmt.Errorf("%w: %s does not support users", databases.ErrUnsupported, engineID)
	}
	if req.AccessLevel != "" {
		return model.DatabaseUser{}, fmt.Errorf("%w: SQL users do not have a Redis access level", databases.ErrUnsupported)
	}
	host := req.Host
	if host == "" && (engineID == model.DatabaseMySQL || engineID == model.DatabaseMariaDB) {
		host = "%"
	}
	if err := engine.ValidateUsername(req.Username); err != nil {
		return model.DatabaseUser{}, invalidIdentifier(err)
	}
	if err := engine.ValidateHost(host); err != nil {
		return model.DatabaseUser{}, invalidIdentifier(err)
	}
	if engine.ProtectedUser(req.Username, host) {
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q", databases.ErrProtected, req.Username)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	password, err := credentials.NewSecret()
	if err != nil {
		return model.DatabaseUser{}, err
	}
	sql, err := engine.CreateUserSQL(req.Username, host, password)
	if err != nil {
		return model.DatabaseUser{}, engineError(err)
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
		lock.Unlock()
		return model.DatabaseUser{}, fmt.Errorf("create user: %s", scrubSecretText(err.Error(), password))
	}
	lock.Unlock()

	user, err := c.findUser(ctx, serviceID, req.Username, host)
	if err != nil {
		rollbackErr := c.rollbackCreatedUser(ctx, serviceID, engine, req.Username, host)
		if rollbackErr != nil {
			return model.DatabaseUser{
				ID:              databaseUserID(serviceID, engineID, req.Username, host),
				ServiceID:       serviceID,
				Engine:          engineID,
				Username:        req.Username,
				Host:            host,
				Enabled:         true,
				Managed:         false,
				PasswordManaged: false,
			}, fmt.Errorf("locate created user: %v; rollback failed: %w", err, rollbackErr)
		}
		return model.DatabaseUser{}, fmt.Errorf("locate created user (rolled back): %w", err)
	}
	if err := c.writeDatabaseSecret(serviceID, user.ID, password); err != nil {
		user.PasswordManaged = false
		return user, fmt.Errorf("store generated password: %w", err)
	}
	user.PasswordManaged = true
	user.Managed = true
	if len(req.Grants) > 0 {
		if err := c.applyUserGrants(ctx, serviceID, engine, user, req.Grants); err != nil {
			return user, err
		}
	}
	return user, nil
}

// UpdateUser changes enabled state or grants. SQL user locking is not part of
// the frozen engine contract, so an enabled change on SQL returns an explicit
// unsupported error rather than silently diverging.
func (c *DockerServiceController) UpdateUser(ctx context.Context, serviceID, userID string, req databases.UpdateUserRequest) (model.DatabaseUser, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if engineID == model.DatabaseRedis {
		return c.updateRedisUser(ctx, serviceID, userID, req)
	}
	if engine == nil {
		return model.DatabaseUser{}, fmt.Errorf("%w: %s does not support users", databases.ErrUnsupported, engineID)
	}
	if req.AccessLevel != "" {
		return model.DatabaseUser{}, fmt.Errorf("%w: SQL users do not have a Redis access level", databases.ErrUnsupported)
	}
	user, err := c.findUserByID(ctx, serviceID, userID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if engine.ProtectedUser(user.Username, user.Host) {
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q", databases.ErrProtected, user.Username)
	}
	if req.Enabled != nil {
		return model.DatabaseUser{}, fmt.Errorf("%w: SQL user enable/disable is not supported by the engine contract", databases.ErrUnsupported)
	}
	if req.Grants != nil {
		if err := c.applyUserGrants(ctx, serviceID, engine, user, req.Grants); err != nil {
			return model.DatabaseUser{}, err
		}
	}
	return c.findUserByID(ctx, serviceID, userID)
}

// DeleteUser deletes an engine user after typed confirmation. PostgreSQL
// object reassignment happens only when explicitly requested.
func (c *DockerServiceController) DeleteUser(ctx context.Context, serviceID, userID string, req databases.DeleteUserRequest) error {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	if engineID == model.DatabaseRedis {
		return c.deleteRedisUser(ctx, serviceID, userID, req)
	}
	if engine == nil {
		return fmt.Errorf("%w: %s does not support users", databases.ErrUnsupported, engineID)
	}
	if req.ReassignOwned && engineID != model.DatabasePostgreSQL {
		return fmt.Errorf("%w: owned-object reassignment is PostgreSQL-only", databases.ErrUnsupported)
	}
	user, err := c.findUserByID(ctx, serviceID, userID)
	if err != nil {
		return err
	}
	if req.Confirm != user.Username {
		return fmt.Errorf("%w: confirmation does not match user %q", databases.ErrInvalidRequest, user.Username)
	}
	if engine.ProtectedUser(user.Username, user.Host) {
		return fmt.Errorf("%w: user %q", databases.ErrProtected, user.Username)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if req.ReassignOwned {
		if err := c.reassignOwned(ctx, serviceID, user); err != nil {
			return err
		}
	}
	sql, err := engine.DeleteUserSQL(user.Username, user.Host)
	if err != nil {
		return engineError(err)
	}
	if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
		return err
	}
	return c.deleteDatabaseSecret(serviceID, user.ID)
}

// SetUserPassword generates and applies a new password, then stores it. If the
// secret cannot be stored the old (now invalid) secret is removed so a reveal
// never returns a stale password.
func (c *DockerServiceController) SetUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return databases.Secret{}, err
	}
	if engineID == model.DatabaseRedis {
		return c.setRedisPassword(ctx, serviceID, userID)
	}
	if engine == nil {
		return databases.Secret{}, fmt.Errorf("%w: %s does not support users", databases.ErrUnsupported, engineID)
	}
	user, err := c.findUserByID(ctx, serviceID, userID)
	if err != nil {
		return databases.Secret{}, err
	}
	if engine.ProtectedUser(user.Username, user.Host) {
		return databases.Secret{}, fmt.Errorf("%w: user %q", databases.ErrProtected, user.Username)
	}
	password, err := credentials.NewSecret()
	if err != nil {
		return databases.Secret{}, err
	}
	sql, err := engine.SetPasswordSQL(user.Username, user.Host, password)
	if err != nil {
		return databases.Secret{}, engineError(err)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return databases.Secret{}, err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
		return databases.Secret{}, fmt.Errorf("set password: %s", scrubSecretText(err.Error(), password))
	}
	if err := c.writeDatabaseSecret(serviceID, user.ID, password); err != nil {
		_ = c.deleteDatabaseSecret(serviceID, user.ID)
		return databases.Secret{}, fmt.Errorf("store generated password: %w", err)
	}
	return databases.Secret{Value: password}, nil
}

// RevealUserPassword returns the stored password for an explicit audited reveal.
func (c *DockerServiceController) RevealUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	engineID, err := c.engineID(serviceID)
	if err != nil {
		return databases.Secret{}, err
	}
	if engineID == model.DatabaseRedis {
		if _, err := c.findRedisUser(ctx, serviceID, userID); err != nil {
			return databases.Secret{}, err
		}
	} else {
		if _, err := c.findUserByID(ctx, serviceID, userID); err != nil {
			return databases.Secret{}, err
		}
	}
	value, err := c.readDatabaseSecret(serviceID, userID)
	if err != nil {
		return databases.Secret{}, err
	}
	return databases.Secret{Value: value}, nil
}

func (c *DockerServiceController) engineID(serviceID string) (model.DatabaseEngine, error) {
	_, engineID, err := c.databaseEngine(serviceID)
	return engineID, err
}

// databaseUserID matches Lane A's opaque user ID scheme: usr_ plus the first
// eight bytes of SHA-256 over serviceID, username, and normalized host.
func databaseUserID(serviceID string, engine model.DatabaseEngine, username, host string) string {
	if (engine == model.DatabaseMySQL || engine == model.DatabaseMariaDB) && host == "" {
		host = "%"
	}
	sum := sha256.Sum256([]byte(serviceID + "\x00" + username + "\x00" + host))
	return "usr_" + hex.EncodeToString(sum[:8])
}

// rollbackCreatedUser deletes a just-created engine user when its parser ID
// cannot be resolved, so a failed create never leaves an orphan account with
// an unknown password.
func (c *DockerServiceController) rollbackCreatedUser(ctx context.Context, serviceID string, engine dbengine.Engine, username, host string) error {
	sql, err := engine.DeleteUserSQL(username, host)
	if err != nil {
		return engineError(err)
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	_, err = c.runDatabaseSQL(ctx, serviceID, sql)
	return err
}

func (c *DockerServiceController) findUser(ctx context.Context, serviceID, username, host string) (model.DatabaseUser, error) {
	users, err := c.ListUsers(ctx, serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	for _, user := range users {
		if user.Username == username && strings.EqualFold(user.Host, host) {
			return user, nil
		}
	}
	return model.DatabaseUser{}, fmt.Errorf("%w: user %q@%q", databases.ErrNotFound, username, host)
}

func (c *DockerServiceController) findUserByID(ctx context.Context, serviceID, userID string) (model.DatabaseUser, error) {
	users, err := c.ListUsers(ctx, serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	for _, user := range users {
		if user.ID == userID {
			return user, nil
		}
	}
	return model.DatabaseUser{}, fmt.Errorf("%w: user %q", databases.ErrNotFound, userID)
}

func (c *DockerServiceController) applyUserGrants(ctx context.Context, serviceID string, engine dbengine.Engine, user model.DatabaseUser, desired []model.DatabaseGrant) error {
	// The current grant set and the diff both run under the service lock so a
	// concurrent update cannot interleave a stale read with new grants.
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	current, err := c.userGrants(ctx, serviceID, engine, user)
	if err != nil {
		return err
	}
	currentByDB := make(map[string]model.DatabaseGrant, len(current))
	for _, grant := range current {
		if !isGrantableScope(engine, grant.Database) || engine.ProtectedDatabase(grant.Database) {
			continue
		}
		currentByDB[grant.Database] = grant
	}
	desiredByDB := make(map[string]model.DatabaseGrant, len(desired))
	for _, grant := range desired {
		if isGlobalGrantScope(grant.Database) {
			return fmt.Errorf("%w: grant scope %q is not database-scoped", databases.ErrInvalidRequest, grant.Database)
		}
		if strings.Contains(grant.Database, "%") {
			return invalidIdentifier(dbengine.ErrInvalidIdentifier)
		}
		if err := engine.ValidateDatabaseName(grant.Database); err != nil {
			return invalidIdentifier(err)
		}
		if engine.ProtectedDatabase(grant.Database) {
			return fmt.Errorf("%w: database %q", databases.ErrProtected, grant.Database)
		}
		desiredByDB[grant.Database] = grant
	}
	for database, want := range desiredByDB {
		have, ok := currentByDB[database]
		if ok && grantsEqual(have, want) {
			continue
		}
		if ok {
			sql, err := engine.RevokeSQL(user.Username, user.Host, database, have)
			if err != nil {
				return engineError(err)
			}
			if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
				return err
			}
		}
		sql, err := engine.GrantSQL(user.Username, user.Host, database, want)
		if err != nil {
			return engineError(err)
		}
		if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
			return err
		}
	}
	for database, have := range currentByDB {
		if _, ok := desiredByDB[database]; ok {
			continue
		}
		if engine.ProtectedDatabase(database) {
			continue
		}
		sql, err := engine.RevokeSQL(user.Username, user.Host, database, have)
		if err != nil {
			return engineError(err)
		}
		if _, err := c.runDatabaseSQL(ctx, serviceID, sql); err != nil {
			return err
		}
	}
	return nil
}

func (c *DockerServiceController) userGrants(ctx context.Context, serviceID string, engine dbengine.Engine, user model.DatabaseUser) ([]model.DatabaseGrant, error) {
	command, err := engine.ListGrantsCommand(user.Username, user.Host)
	if err != nil {
		return nil, err
	}
	out, err := c.runEngineCommand(ctx, serviceID, command)
	if err != nil {
		return nil, err
	}
	return engine.ParseGrants(out)
}

// isGlobalGrantScope reports whether a grant scope is not a single database.
// Global and wildcard scopes are never diffed or revoked by a database-scoped
// update.
func isGlobalGrantScope(database string) bool {
	switch strings.ToLower(strings.TrimSpace(database)) {
	case "", "*", "%", "*.*":
		return true
	default:
		return false
	}
}

// engineError maps engine contract validation/unsupported errors to domain
// errors so the RPC layer never returns a 500 for a rejected identifier.
func engineError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dbengine.ErrUnsupported):
		return fmt.Errorf("%w: %v", databases.ErrUnsupported, err)
	case errors.Is(err, dbengine.ErrInvalidIdentifier):
		return fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	default:
		return err
	}
}

func isGrantableScope(engine dbengine.Engine, database string) bool {
	if isGlobalGrantScope(database) || strings.Contains(database, "%") {
		return false
	}
	return engine.ValidateDatabaseName(database) == nil
}

func grantsEqual(a, b model.DatabaseGrant) bool {
	if a.Preset != b.Preset {
		return false
	}
	left := append([]string(nil), a.Privileges...)
	right := append([]string(nil), b.Privileges...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !strings.EqualFold(left[i], right[i]) {
			return false
		}
	}
	return true
}

// reassignOwned moves objects owned by a PostgreSQL role to the devstack admin
// in every connectable database. It never drops objects.
func (c *DockerServiceController) reassignOwned(ctx context.Context, serviceID string, user model.DatabaseUser) error {
	databasesList, err := c.ListDatabases(ctx, serviceID)
	if err != nil {
		return err
	}
	role, err := dbengine.QuoteIdentifier(model.DatabasePostgreSQL, user.Username)
	if err != nil {
		return invalidIdentifier(err)
	}
	admin, err := dbengine.QuoteIdentifier(model.DatabasePostgreSQL, "devstack")
	if err != nil {
		return err
	}
	for _, database := range databasesList {
		switch database.Name {
		case "template0", "template1":
			continue
		}
		if err := dbengine.ValidateIdentifier(database.Name); err != nil {
			return invalidIdentifier(err)
		}
		sql := fmt.Sprintf("REASSIGN OWNED BY %s TO %s;", role, admin)
		if _, err := c.runDatabaseSQLIn(ctx, serviceID, database.Name, sql); err != nil {
			return fmt.Errorf("reassign owned objects in %q: %w", database.Name, err)
		}
	}
	return nil
}

// ResolveSiteEnv reads linked secrets and returns the DB_* environment for a
// site process. Metadata-only links omit DB_PASSWORD and DATABASE_URL.
func (c *DockerServiceController) ResolveSiteEnv(ctx context.Context, serviceID string, links []model.DatabaseLink) (map[string]string, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engineID == model.DatabaseRedis {
		return nil, fmt.Errorf("%w: Redis users are not site-linkable", databases.ErrUnsupported)
	}
	if engine == nil {
		return nil, fmt.Errorf("%w: %s does not support site links", databases.ErrUnsupported, engineID)
	}
	port, err := c.databasePort(serviceID)
	if err != nil {
		return nil, err
	}
	primaryIndex := -1
	for i, link := range links {
		if link.Primary {
			primaryIndex = i
			break
		}
	}
	if primaryIndex == -1 && len(links) > 0 {
		primaryIndex = 0
	}
	env := make(map[string]string, len(links)*8)
	usedSlugs := make(map[string]bool, len(links))
	for i, link := range links {
		if link.ServiceID != "" && link.ServiceID != serviceID {
			return nil, fmt.Errorf("%w: link service %q does not match %q", databases.ErrInvalidRequest, link.ServiceID, serviceID)
		}
		if err := engine.ValidateDatabaseName(link.Database); err != nil {
			return nil, invalidIdentifier(err)
		}
		if err := engine.ValidateUsername(link.Username); err != nil {
			return nil, invalidIdentifier(err)
		}
		prefix := "DB_"
		if i != primaryIndex {
			slug := envSlug(link.Database)
			for usedSlugs[slug] {
				slug += "_"
			}
			usedSlugs[slug] = true
			prefix = "DB_" + slug + "_"
		}
		connection := connectionValue(engineID)
		env[prefix+"CONNECTION"] = connection
		env[prefix+"HOST"] = "127.0.0.1"
		env[prefix+"PORT"] = fmt.Sprintf("%d", port)
		env[prefix+"DATABASE"] = link.Database
		env[prefix+"USERNAME"] = link.Username
		if !link.PasswordManaged {
			continue
		}
		secret, err := c.readDatabaseSecret(serviceID, link.UserID)
		if err != nil {
			return nil, fmt.Errorf("resolve password for user %q: %w", link.UserID, err)
		}
		env[prefix+"PASSWORD"] = secret
		if i == primaryIndex {
			env["DATABASE_URL"] = databaseURL(engineID, link.Username, secret, port, link.Database)
		} else {
			env[prefix+"URL"] = databaseURL(engineID, link.Username, secret, port, link.Database)
		}
	}
	return env, nil
}

func connectionValue(engine model.DatabaseEngine) string {
	switch engine {
	case model.DatabasePostgreSQL:
		return "pgsql"
	case model.DatabaseRedis:
		return "redis"
	default:
		return "mysql"
	}
}

func databaseURL(engine model.DatabaseEngine, username, password string, port int, database string) string {
	scheme := "mysql"
	if engine == model.DatabasePostgreSQL {
		scheme = "postgres"
	}
	user := url.UserPassword(username, password)
	return fmt.Sprintf("%s://%s@127.0.0.1:%d/%s", scheme, user.String(), port, url.PathEscape(database))
}

func envSlug(database string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(database) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		slug = "DB"
	}
	return slug
}

// GetJob and CancelJob exist for the Platform contract. Helper database jobs
// run synchronously, so there is never a job to look up or cancel.
func (c *DockerServiceController) GetJob(ctx context.Context, jobID string) (model.DatabaseJob, error) {
	return model.DatabaseJob{}, notImplementedJob(jobID)
}

func (c *DockerServiceController) CancelJob(ctx context.Context, jobID string) error {
	return notImplementedJob(jobID)
}
