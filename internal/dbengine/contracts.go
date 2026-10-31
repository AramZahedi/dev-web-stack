// Package dbengine defines the pure engine contract shared by the Windows
// agent and the Linux helper. Concrete engines live in the agent lane.
package dbengine

import (
	"errors"
	"regexp"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var (
	ErrUnsupported       = errors.New("dbengine: unsupported operation")
	ErrInvalidIdentifier = errors.New("dbengine: invalid identifier")
	ErrProtected         = errors.New("dbengine: protected object")
)

var identifierRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Engine is the pure SQL/command contract for one database engine.
type Engine interface {
	ID() model.DatabaseEngine
	ValidateDatabaseName(string) error
	ValidateUsername(string) error
	ValidateHost(string) error
	ProtectedDatabase(string) bool
	ProtectedUser(string, string) bool
	RenameSupported() bool
	ListDatabasesCommand() ([]string, error)
	ParseDatabases([]byte) ([]model.Database, error)
	CreateDatabaseSQL(name, charset, collation string) (string, error)
	DropDatabaseSQL(name string) (string, error)
	RenameDatabaseSQL(name, newName string) (string, error)
	ListUsersCommand() ([]string, error)
	ParseUsers([]byte) ([]model.DatabaseUser, error)
	ListGrantsCommand(username, host string) ([]string, error)
	ParseGrants([]byte) ([]model.DatabaseGrant, error)
	CreateUserSQL(username, host, password string) (string, error)
	SetPasswordSQL(username, host, password string) (string, error)
	DeleteUserSQL(username, host string) (string, error)
	GrantSQL(username, host, database string, grant model.DatabaseGrant) (string, error)
	RevokeSQL(username, host, database string, grant model.DatabaseGrant) (string, error)
	TerminateSessionsSQL(database string) (string, error)
	DumpCommand(database string) ([]string, error)
	RestoreCommand(database string) ([]string, error)
}

// RedisACL is implemented by the Redis engine.
type RedisACL interface {
	ListUsersCommand() []string
	SetUserCommand(username, password, accessLevel string, enabled bool) []string
	DeleteUserCommand(username string) []string
	SaveCommand() []string
	ACLFile() string
}

// Registry resolves an engine by ID.
type Registry interface {
	For(engine model.DatabaseEngine) (Engine, error)
}

// DefaultRegistry is set by the concrete engine package at init. The helper
// lane calls it; if nil, database operations return ErrUnsupported.
var DefaultRegistry func() Registry

// SystemDatabases lists databases that may never be dropped.
var SystemDatabases = map[model.DatabaseEngine]map[string]bool{
	model.DatabaseMySQL: {
		"mysql": true, "information_schema": true, "performance_schema": true,
		"sys": true, "devstack": true,
	},
	model.DatabaseMariaDB: {
		"mysql": true, "information_schema": true, "performance_schema": true,
		"sys": true, "devstack": true,
	},
	model.DatabasePostgreSQL: {
		"postgres": true, "template0": true, "template1": true, "devstack": true,
	},
}

// SystemUsers lists users that may never be dropped.
var SystemUsers = map[model.DatabaseEngine]map[string]bool{
	model.DatabaseMySQL: {
		"root": true, "mysql.sys": true, "mysql.session": true,
		"mysql.infoschema": true, "devstack": true,
	},
	model.DatabaseMariaDB: {
		"root": true, "mysql.sys": true, "mysql.session": true,
		"mysql.infoschema": true, "devstack": true,
	},
	model.DatabasePostgreSQL: {
		"postgres": true, "devstack": true,
	},
	model.DatabaseRedis: {
		"default": true,
	},
}

// ValidateIdentifier accepts the conservative identifier subset used for
// databases, users, and hosts before any engine quoting.
func ValidateIdentifier(name string) error {
	if len(name) == 0 || len(name) > 63 || !identifierRegex.MatchString(name) {
		return ErrInvalidIdentifier
	}
	return nil
}

// privilegeSetEqual reports whether the observed privilege list is exactly the
// expected set (case-insensitive, order-independent). Preset inference uses it
// so a partial custom grant is never promoted to a broader preset.
func privilegeSetEqual(privileges []string, want ...string) bool {
	seen := make(map[string]bool, len(privileges))
	for _, raw := range privileges {
		privilege := strings.ToUpper(strings.TrimSpace(raw))
		if privilege == "" {
			continue
		}
		seen[privilege] = true
	}
	if len(seen) != len(want) {
		return false
	}
	for _, raw := range want {
		if !seen[strings.ToUpper(strings.TrimSpace(raw))] {
			return false
		}
	}
	return true
}

// QuoteIdentifier validates and quotes an identifier for the engine.
func QuoteIdentifier(engine model.DatabaseEngine, name string) (string, error) {
	if err := ValidateIdentifier(name); err != nil {
		return "", err
	}
	switch engine {
	case model.DatabaseMySQL, model.DatabaseMariaDB:
		return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
	case model.DatabasePostgreSQL, model.DatabaseRedis:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
	default:
		return "", ErrUnsupported
	}
}

// QuoteLiteral quotes a SQL string literal. Callers must reject control
// characters before using this for a password.
func QuoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
