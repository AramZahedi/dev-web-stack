package dbengine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// mysqlEngine implements the shared MySQL/MariaDB SQL surface. The two engines
// differ only in the client and dump tool names and in the identity of the
// catalog service.
type mysqlEngine struct {
	id     model.DatabaseEngine
	client string
	dump   string
}

// NewMySQL returns the MySQL engine.
func NewMySQL() Engine {
	return &mysqlEngine{id: model.DatabaseMySQL, client: "mysql", dump: "mysqldump"}
}

func (e *mysqlEngine) ID() model.DatabaseEngine { return e.id }

func (e *mysqlEngine) ValidateDatabaseName(name string) error { return ValidateIdentifier(name) }
func (e *mysqlEngine) ValidateUsername(name string) error     { return ValidateIdentifier(name) }
func (e *mysqlEngine) ValidateHost(host string) error         { return validateMySQLHost(host) }

func (e *mysqlEngine) ProtectedDatabase(name string) bool {
	return SystemDatabases[e.id][name]
}

func (e *mysqlEngine) ProtectedUser(username, _ string) bool {
	return SystemUsers[e.id][username]
}

func (*mysqlEngine) RenameSupported() bool { return false }

func (e *mysqlEngine) ListDatabasesCommand() ([]string, error) {
	const query = `SELECT s.SCHEMA_NAME,
       COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0),
       COUNT(t.TABLE_NAME)
FROM information_schema.SCHEMATA s
LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
GROUP BY s.SCHEMA_NAME
ORDER BY s.SCHEMA_NAME;`
	return []string{e.client, "--batch", "--skip-column-names", "--execute", query}, nil
}

func (e *mysqlEngine) ParseDatabases(output []byte) ([]model.Database, error) {
	rows, err := parseTabRows(output, 3)
	if err != nil {
		return nil, err
	}
	databases := make([]model.Database, 0, len(rows))
	for _, row := range rows {
		size, err := strconv.ParseInt(row[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("dbengine: parse database size %q: %w", row[1], err)
		}
		tables, err := strconv.Atoi(row[2])
		if err != nil {
			return nil, fmt.Errorf("dbengine: parse table count %q: %w", row[2], err)
		}
		databases = append(databases, model.Database{
			Name:       row[0],
			Engine:     e.id,
			SizeBytes:  size,
			TableCount: tables,
		})
	}
	return databases, nil
}

func (e *mysqlEngine) CreateDatabaseSQL(name, charset, collation string) (string, error) {
	quoted, err := QuoteIdentifier(e.id, name)
	if err != nil {
		return "", err
	}
	charset = strings.TrimSpace(charset)
	collation = strings.TrimSpace(collation)
	if charset == "" {
		charset = "utf8mb4"
	}
	if collation == "" {
		collation = "utf8mb4_unicode_ci"
	}
	if err := ValidateIdentifier(charset); err != nil {
		return "", fmt.Errorf("%w: charset %q", ErrInvalidIdentifier, charset)
	}
	if err := ValidateIdentifier(collation); err != nil {
		return "", fmt.Errorf("%w: collation %q", ErrInvalidIdentifier, collation)
	}
	return fmt.Sprintf("CREATE DATABASE %s CHARACTER SET %s COLLATE %s;", quoted, charset, collation), nil
}

func (e *mysqlEngine) DropDatabaseSQL(name string) (string, error) {
	quoted, err := QuoteIdentifier(e.id, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP DATABASE %s;", quoted), nil
}

func (*mysqlEngine) RenameDatabaseSQL(string, string) (string, error) {
	return "", ErrUnsupported
}

func (e *mysqlEngine) ListUsersCommand() ([]string, error) {
	// MariaDB's mysql.user has no account_locked column; MySQL 8 does.
	query := `SELECT User, Host, Super_priv FROM mysql.user ORDER BY User, Host;`
	if e.id == model.DatabaseMySQL {
		query = `SELECT User, Host, Super_priv, account_locked FROM mysql.user ORDER BY User, Host;`
	}
	return []string{e.client, "--batch", "--skip-column-names", "--execute", query}, nil
}

func (e *mysqlEngine) ParseUsers(output []byte) ([]model.DatabaseUser, error) {
	rows, err := parseTabRowsAtLeast(output, 3)
	if err != nil {
		return nil, err
	}
	users := make([]model.DatabaseUser, 0, len(rows))
	for _, row := range rows {
		username, host := row[0], row[1]
		if e.ProtectedUser(username, host) {
			continue
		}
		enabled := true
		if len(row) >= 4 {
			enabled = !strings.EqualFold(strings.TrimSpace(row[3]), "Y")
		}
		users = append(users, model.DatabaseUser{
			ServiceID: string(e.id),
			Engine:    e.id,
			Username:  username,
			Host:      host,
			Enabled:   enabled,
		})
	}
	return users, nil
}

func (e *mysqlEngine) ListGrantsCommand(username, host string) ([]string, error) {
	user, err := e.quoteUser(username, host)
	if err != nil {
		return nil, err
	}
	return []string{e.client, "--batch", "--skip-column-names", "--execute", "SHOW GRANTS FOR " + user + ";"}, nil
}

func (e *mysqlEngine) ParseGrants(output []byte) ([]model.DatabaseGrant, error) {
	type accumulator struct {
		privileges []string
		seen       map[string]bool
	}
	order := make([]string, 0)
	byDatabase := make(map[string]*accumulator)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";"))
		if !strings.HasPrefix(strings.ToUpper(line), "GRANT ") {
			continue
		}
		rest := line[len("GRANT "):]
		onIndex := strings.Index(strings.ToUpper(rest), " ON ")
		if onIndex < 0 {
			continue
		}
		privileges := splitPrivileges(rest[:onIndex])
		afterOn := rest[onIndex+len(" ON "):]
		toIndex := strings.Index(strings.ToUpper(afterOn), " TO ")
		if toIndex < 0 {
			continue
		}
		object := strings.TrimSpace(afterOn[:toIndex])
		if object == "*.*" || !strings.HasSuffix(object, ".*") {
			continue
		}
		database := strings.Trim(strings.TrimSuffix(object, ".*"), "`\"")
		if database == "" || database == "*" {
			continue
		}
		entry := byDatabase[database]
		if entry == nil {
			entry = &accumulator{seen: make(map[string]bool)}
			byDatabase[database] = entry
			order = append(order, database)
		}
		for _, privilege := range privileges {
			if !entry.seen[privilege] {
				entry.seen[privilege] = true
				entry.privileges = append(entry.privileges, privilege)
			}
		}
	}
	grants := make([]model.DatabaseGrant, 0, len(order))
	for _, database := range order {
		entry := byDatabase[database]
		grants = append(grants, model.DatabaseGrant{
			Database:   database,
			Preset:     mysqlGrantPreset(entry.privileges),
			Privileges: entry.privileges,
		})
	}
	return grants, nil
}

func (e *mysqlEngine) CreateUserSQL(username, host, password string) (string, error) {
	user, err := e.quoteUser(username, host)
	if err != nil {
		return "", err
	}
	if err := validatePasswordLiteral(password); err != nil {
		return "", err
	}
	return fmt.Sprintf("CREATE USER %s IDENTIFIED BY %s;", user, QuoteLiteral(password)), nil
}

func (e *mysqlEngine) SetPasswordSQL(username, host, password string) (string, error) {
	user, err := e.quoteUser(username, host)
	if err != nil {
		return "", err
	}
	if err := validatePasswordLiteral(password); err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s;", user, QuoteLiteral(password)), nil
}

func (e *mysqlEngine) DeleteUserSQL(username, host string) (string, error) {
	user, err := e.quoteUser(username, host)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP USER %s;", user), nil
}

func (e *mysqlEngine) GrantSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	return e.grantRevokeSQL("GRANT", "TO", username, host, database, grant)
}

func (e *mysqlEngine) RevokeSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	return e.grantRevokeSQL("REVOKE", "FROM", username, host, database, grant)
}

func (e *mysqlEngine) grantRevokeSQL(verb, target, username, host, database string, grant model.DatabaseGrant) (string, error) {
	user, err := e.quoteUser(username, host)
	if err != nil {
		return "", err
	}
	quoted, err := QuoteIdentifier(e.id, database)
	if err != nil {
		return "", err
	}
	privileges, err := mysqlPrivileges(grant)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s ON %s.* %s %s;", verb, strings.Join(privileges, ", "), quoted, target, user), nil
}

func (e *mysqlEngine) TerminateSessionsSQL(database string) (string, error) {
	if err := e.ValidateDatabaseName(database); err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT CONCAT('KILL ', ID, ';') FROM information_schema.PROCESSLIST WHERE DB = %s AND ID <> CONNECTION_ID();", QuoteLiteral(database)), nil
}

func (e *mysqlEngine) DumpCommand(database string) ([]string, error) {
	if err := e.ValidateDatabaseName(database); err != nil {
		return nil, err
	}
	// --databases is intentionally omitted: the dump must be restorable into a
	// different target database without a USE statement redirecting it back.
	return []string{e.dump, "--single-transaction", "--routines", "--triggers", "--events", "--skip-comments", database}, nil
}

func (e *mysqlEngine) RestoreCommand(database string) ([]string, error) {
	if err := e.ValidateDatabaseName(database); err != nil {
		return nil, err
	}
	return []string{e.client, "--batch", "--database", database}, nil
}

func (e *mysqlEngine) quoteUser(username, host string) (string, error) {
	if err := e.ValidateUsername(username); err != nil {
		return "", err
	}
	host = normalizeMySQLHost(host)
	if err := e.ValidateHost(host); err != nil {
		return "", err
	}
	return QuoteLiteral(username) + "@" + QuoteLiteral(host), nil
}

func normalizeMySQLHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "%"
	}
	return host
}

func validateMySQLHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	if len(host) > 255 {
		return fmt.Errorf("%w: host is too long", ErrInvalidIdentifier)
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '%', r == '_', r == '.', r == '-', r == ':':
		default:
			return fmt.Errorf("%w: invalid host %q", ErrInvalidIdentifier, host)
		}
	}
	return nil
}

var mysqlPrivilegeWhitelist = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"CREATE": true, "ALTER": true, "DROP": true, "INDEX": true,
	"REFERENCES": true, "CREATE TEMPORARY TABLES": true, "LOCK TABLES": true,
	"EXECUTE": true, "CREATE VIEW": true, "SHOW VIEW": true, "TRIGGER": true,
	"CREATE ROUTINE": true, "ALTER ROUTINE": true, "EVENT": true,
}

func mysqlPrivileges(grant model.DatabaseGrant) ([]string, error) {
	switch grant.Preset {
	case model.GrantReadOnly:
		return []string{"SELECT"}, nil
	case model.GrantReadWrite:
		return []string{"SELECT", "INSERT", "UPDATE", "DELETE"}, nil
	case model.GrantReadWriteSchema:
		return []string{
			"SELECT", "INSERT", "UPDATE", "DELETE",
			"CREATE", "ALTER", "DROP", "INDEX", "REFERENCES",
			"CREATE TEMPORARY TABLES", "LOCK TABLES", "EXECUTE",
			"CREATE VIEW", "SHOW VIEW", "TRIGGER",
		}, nil
	case model.GrantAdmin:
		return []string{"ALL PRIVILEGES"}, nil
	case model.GrantCustom:
		if len(grant.Privileges) == 0 {
			return nil, fmt.Errorf("%w: custom grant has no privileges", ErrInvalidIdentifier)
		}
		privileges := make([]string, 0, len(grant.Privileges))
		seen := make(map[string]bool, len(grant.Privileges))
		for _, raw := range grant.Privileges {
			privilege := strings.ToUpper(strings.TrimSpace(raw))
			if !mysqlPrivilegeWhitelist[privilege] {
				return nil, fmt.Errorf("%w: privilege %q", ErrInvalidIdentifier, raw)
			}
			if !seen[privilege] {
				seen[privilege] = true
				privileges = append(privileges, privilege)
			}
		}
		return privileges, nil
	default:
		return nil, ErrUnsupported
	}
}

func mysqlGrantPreset(privileges []string) model.DatabaseGrantPreset {
	hasSchema := false
	hasWrite := false
	hasRead := false
	for _, raw := range privileges {
		switch strings.ToUpper(strings.TrimSpace(raw)) {
		case "ALL", "ALL PRIVILEGES":
			return model.GrantAdmin
		case "CREATE", "ALTER", "DROP", "INDEX", "REFERENCES", "TRIGGER",
			"CREATE TEMPORARY TABLES", "LOCK TABLES", "EXECUTE",
			"CREATE VIEW", "SHOW VIEW", "CREATE ROUTINE", "ALTER ROUTINE", "EVENT":
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

func splitPrivileges(value string) []string {
	parts := strings.Split(value, ",")
	privileges := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			privileges = append(privileges, part)
		}
	}
	return privileges
}

func parseTabRows(output []byte, columns int) ([][]string, error) {
	rows, err := parseTabRowsAtLeast(output, columns)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i] = rows[i][:columns]
	}
	return rows, nil
}

func parseTabRowsAtLeast(output []byte, columns int) ([][]string, error) {
	var rows [][]string
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < columns {
			return nil, fmt.Errorf("dbengine: expected at least %d columns, got %d in %q", columns, len(fields), line)
		}
		rows = append(rows, fields)
	}
	return rows, nil
}

func validatePasswordLiteral(password string) error {
	if password == "" {
		return fmt.Errorf("%w: empty password", ErrInvalidIdentifier)
	}
	if len(password) > 512 {
		return fmt.Errorf("%w: password is too long", ErrInvalidIdentifier)
	}
	for _, r := range password {
		if r == 0 || r == '\n' || r == '\r' {
			return fmt.Errorf("%w: password contains control characters", ErrInvalidIdentifier)
		}
	}
	return nil
}

func validateSQLLiteral(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: empty literal", ErrInvalidIdentifier)
	}
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: literal contains control characters", ErrInvalidIdentifier)
	}
	return nil
}
