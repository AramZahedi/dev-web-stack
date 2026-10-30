package dbengine

import (
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// redisEngine implements the parts of Engine that make sense for Redis. Redis
// has no logical database management in devstack, so the SQL surface returns
// ErrUnsupported; ACL operations live on RedisACL.
type redisEngine struct{}

// NewRedis returns the Redis Engine used for validation and protection.
func NewRedis() Engine { return &redisEngine{} }

func (*redisEngine) ID() model.DatabaseEngine { return model.DatabaseRedis }

func (*redisEngine) ValidateDatabaseName(name string) error { return ValidateIdentifier(name) }
func (*redisEngine) ValidateUsername(name string) error     { return ValidateIdentifier(name) }

func (*redisEngine) ValidateHost(host string) error {
	if strings.TrimSpace(host) != "" {
		return ErrInvalidIdentifier
	}
	return nil
}

func (*redisEngine) ProtectedDatabase(string) bool { return false }

func (*redisEngine) ProtectedUser(username, _ string) bool {
	return SystemUsers[model.DatabaseRedis][username]
}

func (*redisEngine) RenameSupported() bool { return false }

func (*redisEngine) ListDatabasesCommand() ([]string, error) { return nil, ErrUnsupported }
func (*redisEngine) ParseDatabases([]byte) ([]model.Database, error) {
	return nil, ErrUnsupported
}
func (*redisEngine) CreateDatabaseSQL(string, string, string) (string, error) {
	return "", ErrUnsupported
}
func (*redisEngine) DropDatabaseSQL(string) (string, error)     { return "", ErrUnsupported }
func (*redisEngine) RenameDatabaseSQL(string, string) (string, error) {
	return "", ErrUnsupported
}
func (*redisEngine) ListUsersCommand() ([]string, error) { return nil, ErrUnsupported }
func (*redisEngine) ParseUsers([]byte) ([]model.DatabaseUser, error) {
	return nil, ErrUnsupported
}
func (*redisEngine) ListGrantsCommand(string, string) ([]string, error) {
	return nil, ErrUnsupported
}
func (*redisEngine) ParseGrants([]byte) ([]model.DatabaseGrant, error) {
	return nil, ErrUnsupported
}
func (*redisEngine) CreateUserSQL(string, string, string) (string, error) {
	return "", ErrUnsupported
}
func (*redisEngine) SetPasswordSQL(string, string, string) (string, error) {
	return "", ErrUnsupported
}
func (*redisEngine) DeleteUserSQL(string, string) (string, error) { return "", ErrUnsupported }
func (*redisEngine) GrantSQL(string, string, string, model.DatabaseGrant) (string, error) {
	return "", ErrUnsupported
}
func (*redisEngine) RevokeSQL(string, string, string, model.DatabaseGrant) (string, error) {
	return "", ErrUnsupported
}
func (*redisEngine) TerminateSessionsSQL(string) (string, error) { return "", ErrUnsupported }
func (*redisEngine) DumpCommand(string) ([]string, error)        { return nil, ErrUnsupported }
func (*redisEngine) RestoreCommand(string) ([]string, error)     { return nil, ErrUnsupported }

type redisACL struct{}

// NewRedisACL returns the Redis ACL command builder. It is a separate type
// because the frozen Engine and RedisACL interfaces both declare
// ListUsersCommand with different signatures; one Go method set cannot satisfy
// both.
func NewRedisACL() RedisACL { return &redisACL{} }

func (*redisACL) ListUsersCommand() []string {
	return []string{"redis-cli", "ACL", "LIST"}
}

func (*redisACL) SetUserCommand(username, password, accessLevel string, enabled bool) []string {
	rules := []string{"reset"}
	if enabled {
		rules = append(rules, "on")
	} else {
		rules = append(rules, "off")
	}
	rules = append(rules, "~*")
	rules = append(rules, redisAccessRules(username, accessLevel)...)
	// The password is deliberately last so the agent can deliver it through
	// stdin with `redis-cli -x` instead of argv.
	rules = append(rules, ">"+password)
	args := []string{"redis-cli", "ACL", "SETUSER", username}
	return append(args, rules...)
}

// SetUserRulesCommand changes on/off state and access level without resetting
// the password. It is used by updates that must preserve the existing secret.
func SetUserRulesCommand(username, accessLevel string, enabled bool) []string {
	args := []string{"redis-cli", "ACL", "SETUSER", username}
	if enabled {
		args = append(args, "on")
	} else {
		args = append(args, "off")
	}
	args = append(args, "-@all")
	args = append(args, redisAccessRules(username, accessLevel)...)
	return args
}

func redisAccessRules(username, accessLevel string) []string {
	if username == "default" {
		// The default identity is the Redis administrator. It must retain ACL
		// (and therefore ACL SAVE/LIST/SETUSER) or the next rotation locks the
		// agent out of the engine. Application users get the restricted rules.
		return []string{"+@all", "+acl"}
	}
	rules := make([]string, 0, 4+len(redisForbiddenCommands))
	switch normalizeRedisAccessLevel(accessLevel) {
	case "full":
		rules = append(rules, "+@all")
	case "read-only":
		rules = append(rules, "+@read", "+@connection")
	default:
		rules = append(rules, "+@read", "+@connection")
	}
	rules = append(rules, "-@admin", "-@dangerous")
	for _, command := range redisForbiddenCommands {
		rules = append(rules, "-"+command)
	}
	return rules
}

func (*redisACL) DeleteUserCommand(username string) []string {
	return []string{"redis-cli", "ACL", "DELUSER", username}
}

func (*redisACL) SaveCommand() []string {
	return []string{"redis-cli", "ACL", "SAVE"}
}

func (*redisACL) ACLFile() string { return "/data/users.acl" }

var redisForbiddenCommands = []string{
	"config", "debug", "shutdown", "flushall", "flushdb", "acl", "module",
	"replicaof", "slaveof", "migrate", "save", "bgsave", "bgrewriteaof",
	"swapdb", "cluster", "failover",
}

func normalizeRedisAccessLevel(accessLevel string) string {
	switch strings.ToLower(strings.TrimSpace(accessLevel)) {
	case "full":
		return "full"
	case "read-only", "read_only", "readonly", "":
		return "read-only"
	default:
		return "read-only"
	}
}
