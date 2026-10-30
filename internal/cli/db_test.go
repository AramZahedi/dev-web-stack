package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestDBUserPasswordSetFailsWithoutAPICallAndDoesNotLeakInput(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseUserSecret: databases.Secret{Value: "generated-secret"}}
	code, stdout, stderr := runCLIExitInput(t, fake, "s3cret-from-stdin\n",
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

func TestDBLinkReadsPasswordFromStdin(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinked: model.DatabaseLink{ID: "lnk_1", Database: "app"}}
	code, stdout, stderr := runCLIExitInput(t, fake, "s3cret\n", "db", "link", "--site", "blog", "--service", "mysql", "--database", "app", "--user-id", "usr_app", "--password-stdin")
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if fake.databaseLinkReq.Password != "s3cret" {
		t.Fatalf("password=%q", fake.databaseLinkReq.Password)
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
