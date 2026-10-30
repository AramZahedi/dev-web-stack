package helperproto

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
)

func TestServiceBackupsEscapesServiceID(t *testing.T) {
	if got := ServiceBackups("mysql db"); got != "/v1/services/mysql%20db/backups" {
		t.Fatalf("path = %q", got)
	}
}

func TestDatabaseRoutePatternsCarryWildcards(t *testing.T) {
	for name, pattern := range map[string]string{
		"databases":       PathServiceDatabases,
		"database":        PathServiceDatabase,
		"users":           PathServiceUsers,
		"user":            PathServiceUser,
		"user password":   PathServiceUserPassword,
		"user reveal":     PathServiceUserReveal,
		"backups":         PathServiceBackups,
		"database backup": PathServiceDatabaseBackup,
		"backup":          PathServiceBackup,
		"backup download": PathServiceBackupDownload,
		"backup restore":  PathServiceBackupRestore,
		"import":          PathServiceDatabaseImport,
		"export":          PathServiceDatabaseExport,
		"job":             PathServiceJob,
		"database env":    PathServiceDatabaseEnv,
	} {
		if !strings.Contains(pattern, "{id}") {
			t.Errorf("%s pattern %q has no service wildcard", name, pattern)
		}
	}
}

func TestDatabaseWireAliasesMatchSharedProtocol(t *testing.T) {
	var list DatabaseListResponse = dbproto.DatabaseListResponse{}
	var user UserResponse = dbproto.UserResponse{}
	var job JobResponse = dbproto.JobResponse{}
	var env SiteEnvResponse = dbproto.SiteEnvResponse{}
	_ = list
	_ = user
	_ = job
	_ = env
}
