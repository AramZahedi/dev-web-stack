package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestDBListJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseList: []model.Database{{Name: "app", Engine: model.DatabaseMySQL, SizeBytes: 12 << 20}}}
	out := runCLI(t, fake, "db", "list", "--service", "mysql", "--json")
	var got struct {
		Databases []model.Database `json:"databases"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Databases) != 1 || got.Databases[0].Name != "app" {
		t.Fatalf("databases=%+v", got.Databases)
	}
	if strings.Contains(strings.ToLower(out), "password") {
		t.Fatalf("list leaked a password field: %s", out)
	}
}

func TestDBRenameRejectsMySQLBeforeAPICall(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "rename", "--service", "mysql", "--name", "app", "--to", "app2")
	if code == 0 || !strings.Contains(stderr, "engine") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseRenamed {
		t.Fatal("rename API called for mysql")
	}
}

func TestDBRenameRejectsMariaDBBeforeAPICall(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "rename", "--service", "mariadb", "--name", "app", "--to", "app2")
	if code == 0 || !strings.Contains(stderr, "engine") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseRenamed {
		t.Fatal("rename API called for mariadb")
	}
}

func TestDBDropRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "drop", "--service", "mysql", "--name", "app")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseDropped {
		t.Fatal("drop API called without --yes")
	}
}

func TestDBCreateAndUserCreatePayloads(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		databaseCreated:     model.Database{Name: "app"},
		databaseUserCreated: model.DatabaseUser{ID: "usr_app", Username: "app_user"},
	}
	runCLI(t, fake, "db", "create", "--service", "mysql", "--name", "app", "--charset", "utf8mb4", "--collation", "utf8mb4_unicode_ci")
	if fake.databaseCreateReq.Name != "app" || fake.databaseCreateReq.Charset != "utf8mb4" || fake.databaseCreateReq.Collation != "utf8mb4_unicode_ci" {
		t.Fatalf("create req=%+v", fake.databaseCreateReq)
	}
	runCLI(t, fake, "db", "user", "create", "--service", "mysql", "--name", "app_user", "--host", "%", "--grant", "app:read_write_schema")
	if fake.databaseUserCreateReq.Username != "app_user" || fake.databaseUserCreateReq.Host != "%" {
		t.Fatalf("user create req=%+v", fake.databaseUserCreateReq)
	}
	if len(fake.databaseUserCreateReq.Grants) != 1 {
		t.Fatalf("grants=%+v", fake.databaseUserCreateReq.Grants)
	}
	grant := fake.databaseUserCreateReq.Grants[0]
	if grant.Database != "app" || grant.Preset != model.GrantReadWriteSchema {
		t.Fatalf("grant=%+v", grant)
	}
}

func TestDBUserCreateRejectsPrivilegeWithoutGrant(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "user", "create", "--service", "mysql", "--name", "app_user", "--privilege", "SELECT")
	if code == 0 || !strings.Contains(stderr, "grant") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseUserCreateReq.Username != "" {
		t.Fatal("user create API called with --privilege but no --grant")
	}
}

func TestDBUserCreateRejectsPrivilegesForNonCustomPreset(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "user", "create", "--service", "mysql", "--name", "app_user", "--grant", "app:read_only", "--privilege", "SELECT")
	if code == 0 || !strings.Contains(stderr, "privilege") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseUserCreateReq.Username != "" {
		t.Fatal("user create API called with invalid privilege flags")
	}
}

func TestDBUserGrantMergesExistingGrants(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		databaseUser: model.DatabaseUser{
			ID:       "usr_app",
			Username: "app",
			Grants: []model.DatabaseGrant{
				{Database: "app", Preset: model.GrantReadOnly},
				{Database: "other", Preset: model.GrantReadWrite},
			},
		},
	}
	runCLI(t, fake, "db", "user", "grant", "--service", "mysql", "--user-id", "usr_app", "--database", "app", "--preset", "read_write_schema")
	if fake.databaseUserUpdateID != "usr_app" {
		t.Fatalf("updated user=%q", fake.databaseUserUpdateID)
	}
	grants := fake.databaseUserUpdateReq.Grants
	if len(grants) != 2 {
		t.Fatalf("grants=%+v", grants)
	}
	seen := map[string]model.DatabaseGrantPreset{}
	for _, grant := range grants {
		seen[grant.Database] = grant.Preset
	}
	if seen["app"] != model.GrantReadWriteSchema || seen["other"] != model.GrantReadWrite {
		t.Fatalf("grants=%+v", grants)
	}
}

func TestDBUserPasswordRevealWarnsAndPrintsSecret(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "s3cret"}}
	out := runCLI(t, fake, "db", "user", "password", "--service", "mysql", "--user-id", "usr_app", "--reveal")
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "s3cret") {
		t.Fatalf("output=%q", out)
	}
	if !fake.databaseUserRevealed {
		t.Fatal("reveal API not called")
	}
}

func TestReadPasswordLineRejectsOversizedInput(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(strings.Repeat("x", maxPasswordLineBytes+1) + "\n"))
	if _, err := readPasswordLine(cmd); err == nil {
		t.Fatal("oversized password input was accepted")
	}
}

func TestDBUserPasswordSetFailsWithoutAPICallAndDoesNotLeakInput(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "generated-secret"}}
	code, stdout, stderr := runCLIExitReader(t, fake, errorReader{},
		"db", "user", "password", "--service", "mysql", "--user-id", "usr_app", "--set", "--yes")
	if code == 0 || !strings.Contains(stderr, "DATABASE_PASSWORD_SET_UNSUPPORTED") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "s3cret-from-stdin") || strings.Contains(stderr, "s3cret-from-stdin") {
		t.Fatalf("piped password leaked: stdout=%s stderr=%s", stdout, stderr)
	}
	if fake.databaseUserRotated || fake.databaseUserRevealed {
		t.Fatal("--set made an API call")
	}
	if fake.lastHome != "" {
		t.Fatal("--set called clientReady/client factory")
	}
}

func TestDBUserPasswordRotateFlagRotatesGeneratedPassword(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "generated-secret"}}
	runCLI(t, fake, "db", "user", "password", "--service", "mysql", "--user-id", "usr_app", "--rotate", "--yes", "--json")
	if !fake.databaseUserRotated {
		t.Fatal("--rotate did not call the rotate API")
	}
}

func TestDBUserPasswordRotateJSONNeverLeaksSecret(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "s3cret"}}
	out := runCLI(t, fake, "db", "user", "password", "--service", "mysql", "--user-id", "usr_app", "--yes", "--json")
	if strings.Contains(out, "s3cret") {
		t.Fatalf("rotation leaked a password: %s", out)
	}
	if !fake.databaseUserRotated {
		t.Fatal("rotate API not called")
	}
	var got struct {
		UserID  string `json:"user_id"`
		Rotated bool   `json:"rotated"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.UserID != "usr_app" || !got.Rotated {
		t.Fatalf("output=%+v", got)
	}
}

func TestDBBackupDownloadWritesFile(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseBackupData: "backup-data"}
	output := filepath.Join(t.TempDir(), "app.sql.gz")
	runCLI(t, fake, "db", "backup", "download", "--service", "mysql", "--backup-id", "bkp_1", "--output", output)
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "backup-data" {
		t.Fatalf("download=%q", data)
	}
}

func TestDBBackupListRedactsError(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseBackups: []model.DatabaseBackup{{
		ID: "bkp_1", ServiceID: "mysql", Database: "app", Status: model.BackupFailed,
		Error: "dump failed DB_PASSWORD=s3cret-backup",
	}}}
	jsonOut := runCLI(t, fake, "db", "backup", "list", "--service", "mysql", "--json")
	humanOut := runCLI(t, fake, "db", "backup", "list", "--service", "mysql")
	for name, out := range map[string]string{"json": jsonOut, "human": humanOut} {
		if strings.Contains(out, "s3cret-backup") {
			t.Fatalf("%s backup output leaked an error secret: %s", name, out)
		}
	}
}

func TestDBBackupJobRedactsMessage(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseBackupJob: model.DatabaseJob{
		ID: "job_1", ServiceID: "mysql", Database: "app", Kind: model.JobBackup, Status: model.JobFailed,
		Message: "restore failed password=s3cret-job",
	}}
	jsonOut := runCLI(t, fake, "db", "backup", "create", "--service", "mysql", "--database", "app", "--json")
	humanOut := runCLI(t, fake, "db", "backup", "create", "--service", "mysql", "--database", "app")
	for name, out := range map[string]string{"json": jsonOut, "human": humanOut} {
		if strings.Contains(out, "s3cret-job") {
			t.Fatalf("%s job output leaked a message secret: %s", name, out)
		}
	}
}

func TestDBBackupDownloadStdoutKeepsBinaryClean(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseBackupData: "backup-data"}
	code, stdout, stderr := runCLIExit(t, fake, "db", "backup", "download", "--service", "mysql", "--backup-id", "bkp_1", "--output", "-")
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
	if stdout != "backup-data" {
		t.Fatalf("stdout corrupted: %q", stdout)
	}
	if !strings.Contains(stderr, "Downloaded backup") {
		t.Fatalf("summary missing from stderr: %q", stderr)
	}
	if !fake.databaseBackupDownloaded {
		t.Fatal("download API not called")
	}
}

func TestDBExportStdoutKeepsBinaryClean(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseExportData: "dump"}
	code, stdout, stderr := runCLIExit(t, fake, "db", "export", "--service", "mysql", "--database", "app", "--output", "-")
	if code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
	if stdout != "dump" {
		t.Fatalf("stdout corrupted: %q", stdout)
	}
	if !strings.Contains(stderr, "Exported") {
		t.Fatalf("summary missing from stderr: %q", stderr)
	}
	if !fake.databaseExported {
		t.Fatal("export API not called")
	}
}

func TestDBBackupDownloadJSONStdoutRejectedBeforeAPI(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "db", "backup", "download", "--service", "mysql", "--backup-id", "bkp_1", "--output", "-", "--json")
	if code == 0 || !strings.Contains(stderr, "INVALID_FLAGS") {
		t.Fatalf("code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
	if fake.databaseBackupDownloaded {
		t.Fatal("download API called before --output - + --json validation")
	}
	if stdout != "" {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestDBExportJSONStdoutRejectedBeforeAPI(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, stdout, stderr := runCLIExit(t, fake, "db", "export", "--service", "mysql", "--database", "app", "--output", "-", "--json")
	if code == 0 || !strings.Contains(stderr, "INVALID_FLAGS") {
		t.Fatalf("code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
	if fake.databaseExported {
		t.Fatal("export API called before --output - + --json validation")
	}
	if stdout != "" {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestDBImportReplaceRequiresYes(t *testing.T) {
	useTestSeams(t)
	input := filepath.Join(t.TempDir(), "dump.sql")
	if err := os.WriteFile(input, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "import", "--service", "mysql", "--database", "app", "--file", input, "--replace")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseImportFile != "" {
		t.Fatal("import API called without --yes")
	}

	runCLI(t, fake, "db", "import", "--service", "mysql", "--database", "app", "--file", input, "--replace", "--yes")
	if fake.databaseImportReq.Mode != databases.ImportReplace || fake.databaseImportReq.Confirm != "app" || !fake.databaseImportReq.PreImportBackup {
		t.Fatalf("import req=%+v", fake.databaseImportReq)
	}
	if fake.databaseImportFile != "SELECT 1;\n" {
		t.Fatalf("import file=%q", fake.databaseImportFile)
	}
}

func TestDBLinkAndUnlink(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinked: model.DatabaseLink{ID: "lnk_1", Database: "app"}}
	runCLI(t, fake, "db", "link", "--site", "blog", "--service", "mysql", "--database", "app", "--user-id", "usr_app", "--primary")
	if fake.databaseLinkReq.ServiceID != "mysql" || fake.databaseLinkReq.Database != "app" || fake.databaseLinkReq.UserID != "usr_app" || !fake.databaseLinkReq.Primary {
		t.Fatalf("link req=%+v", fake.databaseLinkReq)
	}

	code, _, stderr := runCLIExit(t, fake, "db", "unlink", "--site", "blog", "--link-id", "lnk_1")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.siteDatabaseUnlinked {
		t.Fatal("unlink API called without --yes")
	}
	runCLI(t, fake, "db", "unlink", "--site", "blog", "--link-id", "lnk_1", "--yes")
	if !fake.siteDatabaseUnlinked || fake.siteDatabaseUnlinkRef != "blog" || fake.siteDatabaseUnlinkID != "lnk_1" {
		t.Fatalf("unlink=%v ref=%q id=%q", fake.siteDatabaseUnlinked, fake.siteDatabaseUnlinkRef, fake.siteDatabaseUnlinkID)
	}
}

func TestDBLinkJSONRedactsConnectionSecrets(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinked: model.DatabaseLink{
		ID: "lnk_1", Database: "app", Username: "app",
		Error: "failed mysql://app:s3cret@127.0.0.1:3306/app?password=s3cret",
		Connection: model.DatabaseConnection{
			Host: "127.0.0.1", Port: 3306, Database: "app", Username: "app",
			URL: "mysql://app:s3cret@127.0.0.1:3306/app?password=s3cret&sslmode=require",
			Env: map[string]string{"DB_PASSWORD": "s3cret", "DB_BLOG_PASSWORD": "s3cret", "DB_BLOG_URL": "mysql://blog:s3cret@127.0.0.1:3306/blog", "DB_HOST": "127.0.0.1"},
		},
	}}
	out := runCLI(t, fake, "db", "link", "--site", "blog", "--service", "mysql", "--database", "app", "--user-id", "usr_app", "--json")
	if strings.Contains(out, "s3cret") {
		t.Fatalf("db link JSON leaked a secret: %s", out)
	}
	if strings.Contains(out, "DB_PASSWORD") || strings.Contains(out, "DB_BLOG_PASSWORD") {
		t.Fatalf("db link JSON leaked a password key: %s", out)
	}
	if !strings.Contains(out, "sslmode=require") {
		t.Fatalf("db link JSON lost a non-secret query parameter: %s", out)
	}
}

func TestDBPasswordRejectsNonRedisBeforeAPICall(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "s3cret"}}
	code, stdout, stderr := runCLIExit(t, fake, "db", "password", "--service", "mysql", "--reveal")
	if code == 0 || !strings.Contains(stderr, "DATABASE_UNSUPPORTED") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if fake.databaseUserRevealed || fake.databaseUserRotated || fake.servicePasswordSet {
		t.Fatal("db password made an API call for a non-Redis service")
	}
}

func TestDBPasswordRedisRevealUsesDefaultUser(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		databaseUserList:   []model.DatabaseUser{{ID: "usr_default", Username: "default", Engine: model.DatabaseRedis}},
		databaseUserSecret: databases.Secret{Value: "redis-secret"},
	}
	out := runCLI(t, fake, "db", "password", "--service", "redis", "--reveal")
	if !strings.Contains(out, "redis-secret") {
		t.Fatalf("output=%q", out)
	}
	if fake.databaseUserRevealID != "usr_default" {
		t.Fatalf("revealed user=%q", fake.databaseUserRevealID)
	}
}

func TestDBPasswordRedisRotateJSONNeverLeaksSecret(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "redis-secret"}}
	out := runCLI(t, fake, "db", "password", "--service", "redis", "--yes", "--json")
	if strings.Contains(out, "redis-secret") {
		t.Fatalf("rotation leaked a password: %s", out)
	}
	if !fake.servicePasswordSet {
		t.Fatal("service password API not called")
	}
}

func TestDBDropYesCallsAPIWithConfirmation(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	runCLI(t, fake, "db", "drop", "--service", "mysql", "--name", "app", "--backup-first", "--yes")
	if !fake.databaseDropped {
		t.Fatal("drop API not called")
	}
	if fake.databaseDropReq.Confirm != "app" || !fake.databaseDropReq.BackupFirst {
		t.Fatalf("drop req=%+v", fake.databaseDropReq)
	}
}

func TestDBUserDeleteRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "user", "delete", "--service", "mysql", "--user-id", "usr_app")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseUserDeleted || fake.lastHome != "" {
		t.Fatal("user delete called the daemon without confirmation")
	}
}

func TestDBBackupDeleteRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "backup", "delete", "--service", "mysql", "--backup-id", "bkp_1")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseBackupDelete || fake.lastHome != "" {
		t.Fatal("backup delete called the daemon without confirmation")
	}
}

func TestDBPasswordRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "password", "--service", "redis")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.servicePasswordSet || fake.lastHome != "" {
		t.Fatal("db password called the daemon without confirmation")
	}
}

func TestDBUserPasswordRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "user", "password", "--service", "mysql", "--user-id", "usr_app")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseUserRotated || fake.lastHome != "" {
		t.Fatal("user password rotation called the daemon without confirmation")
	}
}

func TestDBUserDeleteConfirmsWithUsername(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUser: model.DatabaseUser{ID: "usr_app", Username: "app"}}
	runCLI(t, fake, "db", "user", "delete", "--service", "mysql", "--user-id", "usr_app", "--yes")
	if !fake.databaseUserDeleted {
		t.Fatal("delete API not called")
	}
	if fake.databaseUserDeleteReq.Confirm != "app" {
		t.Fatalf("confirm=%q", fake.databaseUserDeleteReq.Confirm)
	}
}

func TestDBBackupRestoreOverwriteRequiresYesAndTarget(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "db", "backup", "restore", "--service", "mysql", "--backup-id", "bkp_1", "--overwrite")
	if code == 0 || !strings.Contains(stderr, "target") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	code, _, stderr = runCLIExit(t, fake, "db", "backup", "restore", "--service", "mysql", "--backup-id", "bkp_1", "--target", "app", "--overwrite")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.databaseRestoreReq.Overwrite {
		t.Fatal("restore API called without --yes")
	}
	runCLI(t, fake, "db", "backup", "restore", "--service", "mysql", "--backup-id", "bkp_1", "--target", "app", "--overwrite", "--yes")
	if !fake.databaseRestoreReq.Overwrite || fake.databaseRestoreReq.Confirm != "app" || !fake.databaseRestoreReq.PreRestoreBackup {
		t.Fatalf("restore req=%+v", fake.databaseRestoreReq)
	}
}

func TestDBExportDoesNotTruncateDestinationOnCopyFailure(t *testing.T) {
	useTestSeams(t)
	output := filepath.Join(t.TempDir(), "app.sql")
	if err := os.WriteFile(output, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeClient{databaseExportReadErr: errors.New("stream failed")}
	code, _, stderr := runCLIExit(t, fake, "db", "export", "--service", "mysql", "--database", "app", "--output", output)
	if code == 0 {
		t.Fatalf("export unexpectedly succeeded: %s", stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("destination was truncated before a successful copy: %q", data)
	}
}

func TestDBExportReplacesExistingFileOnSuccess(t *testing.T) {
	useTestSeams(t)
	output := filepath.Join(t.TempDir(), "app.sql")
	if err := os.WriteFile(output, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeClient{databaseExportData: "new dump"}
	runCLI(t, fake, "db", "export", "--service", "mysql", "--database", "app", "--output", output)
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new dump" {
		t.Fatalf("export did not replace the destination: %q", data)
	}
}

func TestDBExportWritesFile(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseExportData: "dump"}
	output := filepath.Join(t.TempDir(), "app.sql")
	runCLI(t, fake, "db", "export", "--service", "mysql", "--database", "app", "--output", output)
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "dump" {
		t.Fatalf("export=%q", data)
	}
}

func TestDBLinkExternalPasswordStdinUnsupported(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	reader := &trackingReader{data: "s3cret\n"}
	code, stdout, stderr := runCLIExitReader(t, fake, reader, "db", "link", "--site", "blog", "--service", "mysql", "--database", "app", "--user-id", "usr_app", "--password-stdin")
	if code == 0 || !strings.Contains(stderr, "DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if reader.read {
		t.Fatal("external password stdin was read")
	}
	if strings.Contains(stdout, "s3cret") || strings.Contains(stderr, "s3cret") {
		t.Fatalf("piped password leaked: stdout=%s stderr=%s", stdout, stderr)
	}
	if fake.databaseLinkReq.UserID != "" || fake.lastHome != "" {
		t.Fatal("link called the daemon for an unsupported external password")
	}
}

func TestDBUserListJSONNeverLeaksSecret(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserList: []model.DatabaseUser{{
		ID: "usr_app", Username: "app", Engine: model.DatabaseMySQL, PasswordManaged: true,
	}}}
	out := runCLI(t, fake, "db", "user", "list", "--service", "mysql", "--json")
	if strings.Contains(out, "s3cret") {
		t.Fatalf("user list leaked a secret: %s", out)
	}
	if !strings.Contains(out, `"password_managed":true`) {
		t.Fatalf("user list missing password state: %s", out)
	}
}
