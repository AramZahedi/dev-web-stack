package dbengine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type postgresEngine struct{}

// NewPostgreSQL returns the PostgreSQL engine.
func NewPostgreSQL() Engine { return &postgresEngine{} }

func (*postgresEngine) ID() model.DatabaseEngine { return model.DatabasePostgreSQL }

func (*postgresEngine) ValidateDatabaseName(name string) error { return ValidateIdentifier(name) }
func (*postgresEngine) ValidateUsername(name string) error     { return ValidateIdentifier(name) }

func (*postgresEngine) ValidateHost(host string) error {
	if strings.TrimSpace(host) != "" {
		return fmt.Errorf("%w: PostgreSQL users have no host scope", ErrInvalidIdentifier)
	}
	return nil
}

func (*postgresEngine) ProtectedDatabase(name string) bool {
	return SystemDatabases[model.DatabasePostgreSQL][name]
}

func (*postgresEngine) ProtectedUser(username, _ string) bool {
	return SystemUsers[model.DatabasePostgreSQL][username]
}

func (*postgresEngine) RenameSupported() bool { return true }

func (*postgresEngine) ListDatabasesCommand() ([]string, error) {
	const query = `SELECT datname, pg_database_size(datname), 0 FROM pg_database ORDER BY datname;`
	return []string{
		"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1",
		"--username=postgres", "--dbname=postgres",
		"--tuples-only", "--no-align", "--field-separator", "\t",
		"--command", query,
	}, nil
}

func (*postgresEngine) ParseDatabases(output []byte) ([]model.Database, error) {
	rows, err := parseTabRows(output, 3)
	if err != nil {
		return nil, err
	}
	databases := make([]model.Database, 0, len(rows))
	for _, row := range rows {
		size, err := parseInt64(row[1])
		if err != nil {
			return nil, fmt.Errorf("dbengine: parse database size %q: %w", row[1], err)
		}
		tables, err := parseInt(row[2])
		if err != nil {
			return nil, fmt.Errorf("dbengine: parse table count %q: %w", row[2], err)
		}
		databases = append(databases, model.Database{
			Name:       row[0],
			Engine:     model.DatabasePostgreSQL,
			SizeBytes:  size,
			TableCount: tables,
		})
	}
	return databases, nil
}

func (*postgresEngine) CreateDatabaseSQL(name, charset, collation string) (string, error) {
	quoted, err := QuoteIdentifier(model.DatabasePostgreSQL, name)
	if err != nil {
		return "", err
	}
	charset = strings.TrimSpace(charset)
	collation = strings.TrimSpace(collation)
	if charset == "" && collation == "" {
		return fmt.Sprintf("CREATE DATABASE %s;", quoted), nil
	}
	parts := []string{fmt.Sprintf("CREATE DATABASE %s WITH TEMPLATE template0", quoted)}
	if charset != "" {
		if err := validateSQLLiteral(charset); err != nil {
			return "", err
		}
		parts = append(parts, "ENCODING "+QuoteLiteral(charset))
	}
	if collation != "" {
		if err := validateSQLLiteral(collation); err != nil {
			return "", err
		}
		parts = append(parts, "LC_COLLATE "+QuoteLiteral(collation))
	}
	return strings.Join(parts, " ") + ";", nil
}

func (*postgresEngine) DropDatabaseSQL(name string) (string, error) {
	quoted, err := QuoteIdentifier(model.DatabasePostgreSQL, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP DATABASE %s;", quoted), nil
}

func (*postgresEngine) RenameDatabaseSQL(name, newName string) (string, error) {
	quoted, err := QuoteIdentifier(model.DatabasePostgreSQL, name)
	if err != nil {
		return "", err
	}
	quotedNew, err := QuoteIdentifier(model.DatabasePostgreSQL, newName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER DATABASE %s RENAME TO %s;", quoted, quotedNew), nil
}

func (*postgresEngine) ListUsersCommand() ([]string, error) {
	const query = `SELECT rolname, rolcanlogin, rolsuper FROM pg_roles ORDER BY rolname;`
	return []string{
		"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1",
		"--username=postgres", "--dbname=postgres",
		"--tuples-only", "--no-align", "--field-separator", "\t",
		"--command", query,
	}, nil
}

func (*postgresEngine) ParseUsers(output []byte) ([]model.DatabaseUser, error) {
	rows, err := parseTabRows(output, 3)
	if err != nil {
		return nil, err
	}
	users := make([]model.DatabaseUser, 0, len(rows))
	for _, row := range rows {
		username := row[0]
		if SystemUsers[model.DatabasePostgreSQL][username] {
			continue
		}
		users = append(users, model.DatabaseUser{
			ServiceID: string(model.DatabasePostgreSQL),
			Engine:    model.DatabasePostgreSQL,
			Username:  username,
			Enabled:   row[1] == "t",
		})
	}
	return users, nil
}

func (*postgresEngine) ListGrantsCommand(username, host string) ([]string, error) {
	if err := ValidateIdentifier(username); err != nil {
		return nil, err
	}
	if err := (&postgresEngine{}).ValidateHost(host); err != nil {
		return nil, err
	}
	// A role's table privileges live in each database, so the fixed script
	// loops over connectable databases. The role name travels as a psql
	// variable; it is never interpolated into shell syntax.
	script := `set -eu
for db in $(psql --no-psqlrc --set=ON_ERROR_STOP=1 --username=postgres --dbname=postgres --tuples-only --no-align --command="SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname"); do
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --username=postgres --dbname="$db" --tuples-only --no-align --field-separator="$(printf '\t')" --set=role=` + username + ` --command="SELECT current_database(), privilege_type FROM information_schema.role_table_grants WHERE grantee = :'role' UNION SELECT current_database(), privilege_type FROM information_schema.role_usage_grants WHERE grantee = :'role' UNION SELECT current_database(), 'CONNECT' WHERE has_database_privilege(:'role', current_database(), 'CONNECT')"
done`
	return []string{"sh", "-c", script}, nil
}

func (*postgresEngine) ParseGrants(output []byte) ([]model.DatabaseGrant, error) {
	rows, err := parseTabRows(output, 2)
	if err != nil {
		return nil, err
	}
	order := make([]string, 0)
	privileges := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, row := range rows {
		database, privilege := row[0], strings.ToUpper(strings.TrimSpace(row[1]))
		if database == "" || privilege == "" {
			continue
		}
		if seen[database] == nil {
			seen[database] = make(map[string]bool)
			order = append(order, database)
		}
		if !seen[database][privilege] {
			seen[database][privilege] = true
			privileges[database] = append(privileges[database], privilege)
		}
	}
	grants := make([]model.DatabaseGrant, 0, len(order))
	for _, database := range order {
		grants = append(grants, model.DatabaseGrant{
			Database:   database,
			Preset:     postgresGrantPreset(privileges[database]),
			Privileges: privileges[database],
		})
	}
	return grants, nil
}

func (*postgresEngine) CreateUserSQL(username, host, password string) (string, error) {
	if err := ValidateIdentifier(username); err != nil {
		return "", err
	}
	if err := (&postgresEngine{}).ValidateHost(host); err != nil {
		return "", err
	}
	if err := validatePasswordLiteral(password); err != nil {
		return "", err
	}
	quoted, err := QuoteIdentifier(model.DatabasePostgreSQL, username)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s;", quoted, QuoteLiteral(password)), nil
}

func (*postgresEngine) SetPasswordSQL(username, host, password string) (string, error) {
	if err := ValidateIdentifier(username); err != nil {
		return "", err
	}
	if err := (&postgresEngine{}).ValidateHost(host); err != nil {
		return "", err
	}
	if err := validatePasswordLiteral(password); err != nil {
		return "", err
	}
	quoted, err := QuoteIdentifier(model.DatabasePostgreSQL, username)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER ROLE %s WITH PASSWORD %s;", quoted, QuoteLiteral(password)), nil
}

func (*postgresEngine) DeleteUserSQL(username, host string) (string, error) {
	if err := ValidateIdentifier(username); err != nil {
		return "", err
	}
	if err := (&postgresEngine{}).ValidateHost(host); err != nil {
		return "", err
	}
	quoted, err := QuoteIdentifier(model.DatabasePostgreSQL, username)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP ROLE %s;", quoted), nil
}

func (e *postgresEngine) GrantSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	return e.grantRevokeSQL("GRANT", "TO", username, host, database, grant)
}

func (e *postgresEngine) RevokeSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	return e.grantRevokeSQL("REVOKE", "FROM", username, host, database, grant)
}

func (e *postgresEngine) grantRevokeSQL(verb, target, username, host, database string, grant model.DatabaseGrant) (string, error) {
	if err := e.ValidateUsername(username); err != nil {
		return "", err
	}
	if err := e.ValidateHost(host); err != nil {
		return "", err
	}
	if err := e.ValidateDatabaseName(database); err != nil {
		return "", err
	}
	user, err := QuoteIdentifier(model.DatabasePostgreSQL, username)
	if err != nil {
		return "", err
	}
	quotedDatabase, err := QuoteIdentifier(model.DatabasePostgreSQL, database)
	if err != nil {
		return "", err
	}
	statements := make([]string, 0, 8)
	seen := make(map[string]bool)
	add := func(privileges, object string) {
		statement := fmt.Sprintf("%s %s ON %s %s %s;", verb, privileges, object, target, user)
		if !seen[statement] {
			seen[statement] = true
			statements = append(statements, statement)
		}
	}
	addDefault := func(privileges, object string) {
		statement := fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public %s %s ON %s %s %s;", verb, privileges, object, target, user)
		if !seen[statement] {
			seen[statement] = true
			statements = append(statements, statement)
		}
	}
	addDatabase := func(privileges string) { add(privileges, "DATABASE "+quotedDatabase) }

	switch grant.Preset {
	case model.GrantReadOnly:
		addDatabase("CONNECT")
		add("USAGE", "SCHEMA public")
		add("SELECT", "ALL TABLES IN SCHEMA public")
		add("SELECT", "ALL SEQUENCES IN SCHEMA public")
		addDefault("SELECT", "TABLES")
		addDefault("SELECT", "SEQUENCES")
	case model.GrantReadWrite:
		addDatabase("CONNECT")
		add("USAGE", "SCHEMA public")
		add("SELECT, INSERT, UPDATE, DELETE", "ALL TABLES IN SCHEMA public")
		add("USAGE, SELECT, UPDATE", "ALL SEQUENCES IN SCHEMA public")
		addDefault("SELECT, INSERT, UPDATE, DELETE", "TABLES")
		addDefault("USAGE, SELECT, UPDATE", "SEQUENCES")
	case model.GrantReadWriteSchema:
		addDatabase("CONNECT")
		add("USAGE", "SCHEMA public")
		add("CREATE", "SCHEMA public")
		add("SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER", "ALL TABLES IN SCHEMA public")
		add("USAGE, SELECT, UPDATE", "ALL SEQUENCES IN SCHEMA public")
		addDefault("SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER", "TABLES")
		addDefault("USAGE, SELECT, UPDATE", "SEQUENCES")
	case model.GrantAdmin:
		addDatabase("ALL PRIVILEGES")
		add("ALL", "SCHEMA public")
		add("ALL", "ALL TABLES IN SCHEMA public")
		add("ALL", "ALL SEQUENCES IN SCHEMA public")
		addDefault("ALL", "TABLES")
		addDefault("ALL", "SEQUENCES")
	case model.GrantCustom:
		if len(grant.Privileges) == 0 {
			return "", fmt.Errorf("%w: custom grant has no privileges", ErrInvalidIdentifier)
		}
		for _, raw := range grant.Privileges {
			privilege := strings.ToUpper(strings.TrimSpace(raw))
			switch privilege {
			case "CONNECT", "TEMPORARY":
				addDatabase(privilege)
			case "CREATE", "USAGE":
				add(privilege, "SCHEMA public")
			case "SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER":
				add(privilege, "ALL TABLES IN SCHEMA public")
			default:
				return "", fmt.Errorf("%w: privilege %q", ErrInvalidIdentifier, raw)
			}
		}
	default:
		return "", ErrUnsupported
	}
	return strings.Join(statements, "\n"), nil
}

func (*postgresEngine) TerminateSessionsSQL(database string) (string, error) {
	if err := ValidateIdentifier(database); err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid();", QuoteLiteral(database)), nil
}

func (*postgresEngine) DumpCommand(database string) ([]string, error) {
	if err := ValidateIdentifier(database); err != nil {
		return nil, err
	}
	return []string{"pg_dump", "--no-owner", "--no-acl", "--format=plain", "--username=postgres", database}, nil
}

func (*postgresEngine) RestoreCommand(database string) ([]string, error) {
	if err := ValidateIdentifier(database); err != nil {
		return nil, err
	}
	return []string{"psql", "--no-psqlrc", "--set=ON_ERROR_STOP=1", "--username=postgres", "--dbname", database}, nil
}

func postgresGrantPreset(privileges []string) model.DatabaseGrantPreset {
	hasSchema := false
	hasWrite := false
	hasRead := false
	for _, raw := range privileges {
		switch strings.ToUpper(strings.TrimSpace(raw)) {
		case "ALL", "ALL PRIVILEGES":
			return model.GrantAdmin
		case "CREATE", "ALTER", "DROP", "TRUNCATE", "REFERENCES", "TRIGGER":
			hasSchema = true
		case "INSERT", "UPDATE", "DELETE":
			hasWrite = true
		case "SELECT":
			hasRead = true
		}
	}
	switch {
	case hasSchema:
		return model.GrantReadWriteSchema
	case hasWrite:
		return model.GrantReadWrite
	case hasRead:
		return model.GrantReadOnly
	default:
		return model.GrantCustom
	}
}

func parseInt64(value string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
}

func parseInt(value string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(value))
}
