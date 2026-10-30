package agentdocker

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func gzipBytes(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestCreateBackupWritesGzipAndMetadata(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if job.BackupID == "" || !strings.HasPrefix(job.BackupID, "app/") {
		t.Fatalf("backup ID = %q", job.BackupID)
	}
	path := filepath.Join(d.root, "backups", "databases", "mysql", "app")
	matches, _ := filepath.Glob(filepath.Join(path, "*.sql.gz"))
	if len(matches) != 1 {
		t.Fatalf("backups = %v", matches)
	}
	file, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "CREATE TABLE t (id int);\n" {
		t.Fatalf("backup content = %q", data)
	}
	backups, err := d.ListBackups(context.Background(), "mysql")
	if err != nil || len(backups) != 1 {
		t.Fatalf("ListBackups = %+v, %v", backups, err)
	}
	if backups[0].ID != job.BackupID || backups[0].SHA256 == "" || backups[0].SizeBytes == 0 {
		t.Fatalf("backup metadata = %+v", backups[0])
	}
}

func TestImportRejectsOverOneGiB(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	_, err := d.ImportDatabase(context.Background(), "mysql", "app", io.LimitReader(zeroReader{}, (1<<30)+1), "big.sql", databases.ImportRequest{Mode: databases.ImportMerge})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("oversized import invoked Docker: %v", runner.joinedCalls())
	}
}

func TestExportStreamsWithoutBuffering(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("dump")
	body, err := d.ExportDatabase(context.Background(), "mysql", "app")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "dump" {
		t.Fatalf("export = %q", data)
	}
}

func TestDeleteBackupRejectsTraversal(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("dump")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, backupID := range []string{"../../etc/passwd", "app/../secret.sql.gz", "/abs/app/x.sql.gz", "app/x.sql", "app/.."} {
		if err := d.DeleteBackup(context.Background(), "mysql", backupID); err == nil {
			t.Fatalf("DeleteBackup accepted %q", backupID)
		}
	}
	path := filepath.Join(d.root, "backups", "databases", "mysql", job.BackupID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("traversal deleted the backup: %v", err)
	}
}

func TestRestoreBackupNewTarget(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
	runner.runOutputs["information_schema.SCHEMATA"] = []byte("app\t0\t0\n")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := d.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{Target: "app_restore"})
	if err != nil || restored.Status != model.JobSucceeded {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if !runner.stdinContains("CREATE DATABASE `app_restore`") {
		t.Fatal("restore did not create the new target")
	}
	if !runner.stdinContains("CREATE TABLE t (id int);") {
		t.Fatalf("restore did not stream the dump: %s", runner.joinedCalls())
	}
}

func TestRestoreOverwriteRequiresConfirmAndTerminates(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
	runner.runOutputs["information_schema.PROCESSLIST"] = []byte("KILL 42;\n")
	runner.runOutputs["information_schema.SCHEMATA"] = []byte("app\t0\t0\n")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "wrong"}); !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("wrong confirm err = %v", err)
	}
	if _, err := d.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("DROP DATABASE `app`") || !runner.stdinContains("CREATE DATABASE `app`") {
		t.Fatal("overwrite restore did not drop and recreate the target")
	}
	if !runner.stdinContains("information_schema.PROCESSLIST") {
		t.Fatal("overwrite restore did not terminate sessions")
	}
	if !runner.stdinContains("CREATE TABLE t (id int);") {
		t.Fatal("overwrite restore did not stream the dump")
	}
}

func TestImportMergeAndReplace(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	content := "CREATE TABLE t (id int);\n"
	upload := gzipBytes(t, content)
	job, err := d.ImportDatabase(context.Background(), "mysql", "app", bytes.NewReader(upload), "dump.sql.gz", databases.ImportRequest{Mode: databases.ImportMerge})
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("merge import = %+v, %v", job, err)
	}
	if !runner.stdinContains(content) {
		t.Fatal("merge import did not stream the decompressed dump")
	}
	runner.runOutputs["information_schema.PROCESSLIST"] = []byte("KILL 42;\n")
	if _, err := d.ImportDatabase(context.Background(), "mysql", "app", bytes.NewReader(upload), "dump.sql.gz", databases.ImportRequest{Mode: databases.ImportReplace, Confirm: "wrong"}); !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("replace wrong confirm err = %v", err)
	}
	if _, err := d.ImportDatabase(context.Background(), "mysql", "app", bytes.NewReader(upload), "dump.sql.gz", databases.ImportRequest{Mode: databases.ImportReplace, Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("DROP DATABASE `app`") || !runner.stdinContains("CREATE DATABASE `app`") {
		t.Fatal("replace import did not drop and recreate the target")
	}
	entries, err := os.ReadDir(filepath.Join(d.root, "backups", "databases", "mysql", ".staging"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("staging files were not deleted: %v", entries)
	}
}

func TestImportMergeCreatesPreImportBackup(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("dump")
	upload := gzipBytes(t, "CREATE TABLE t (id int);\n")
	job, err := d.ImportDatabase(context.Background(), "mysql", "app", bytes.NewReader(upload), "dump.sql.gz", databases.ImportRequest{
		Mode: databases.ImportMerge, PreImportBackup: true,
	})
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("import = %+v, %v", job, err)
	}
	if job.BackupID == "" {
		t.Fatal("merge import did not report the pre-import backup")
	}
	path := filepath.Join(d.root, "backups", "databases", "mysql", job.BackupID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pre-import backup missing: %v", err)
	}
}

func TestListDownloadAndDeleteBackupWorkWhileStopped(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("dump")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	runner.state = "exited"
	runner.calls = nil
	backups, err := d.ListBackups(context.Background(), "mysql")
	if err != nil || len(backups) != 1 {
		t.Fatalf("ListBackups while stopped = %+v, %v", backups, err)
	}
	body, err := d.DownloadBackup(context.Background(), "mysql", job.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if len(data) == 0 {
		t.Fatal("empty download")
	}
	if err := d.DeleteBackup(context.Background(), "mysql", job.BackupID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.root, "backups", "databases", "mysql", job.BackupID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup still exists: %v", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " "), "ps -a") {
			t.Fatalf("stopped backup operation checked service status: %v", call.args)
		}
	}
}

func TestBackupOperationsRejectSymlinkedParentDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	d, runner := newDatabaseTestDocker(t)
	outside := t.TempDir()
	name := "20260101T000000.000000000Z.sql.gz"
	if err := os.WriteFile(filepath.Join(outside, name), gzipBytes(t, "outside dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	serviceRoot := filepath.Join(d.root, "backups", "databases", "mysql")
	if err := os.MkdirAll(serviceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(serviceRoot, "app")); err != nil {
		t.Fatal(err)
	}
	backupID := "app/" + name

	if backups, err := d.ListBackups(context.Background(), "mysql"); err != nil || len(backups) != 0 {
		t.Fatalf("ListBackups followed a symlinked database directory: %+v, %v", backups, err)
	}
	if _, err := d.DownloadBackup(context.Background(), "mysql", backupID); err == nil {
		t.Fatal("DownloadBackup followed a symlinked database directory")
	}
	if err := d.DeleteBackup(context.Background(), "mysql", backupID); err == nil {
		t.Fatal("DeleteBackup followed a symlinked database directory")
	}
	if _, err := os.Stat(filepath.Join(outside, name)); err != nil {
		t.Fatalf("DeleteBackup removed a file outside the backup tree: %v", err)
	}
	runner.calls = nil
	if _, err := d.RestoreBackup(context.Background(), "mysql", backupID, databases.RestoreBackupRequest{Target: "restored"}); err == nil {
		t.Fatal("RestoreBackup followed a symlinked database directory")
	}
	if runner.stdinContains("outside dump") {
		t.Fatalf("RestoreBackup read a file outside the backup tree: %s", runner.joinedCalls())
	}
}

func TestBackupOperationsRejectSymlinkedServiceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	d, _ := newDatabaseTestDocker(t)
	outside := t.TempDir()
	name := "20260101T000000.000000000Z.sql.gz"
	if err := os.MkdirAll(filepath.Join(outside, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "app", name), gzipBytes(t, "outside dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	databasesRoot := filepath.Join(d.root, "backups", "databases")
	if err := os.MkdirAll(databasesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(databasesRoot, "mysql")); err != nil {
		t.Fatal(err)
	}
	if backups, err := d.ListBackups(context.Background(), "mysql"); err == nil && len(backups) != 0 {
		t.Fatalf("ListBackups followed a symlinked service root: %+v", backups)
	}
	if _, err := d.DownloadBackup(context.Background(), "mysql", "app/"+name); err == nil {
		t.Fatal("DownloadBackup followed a symlinked service root")
	}
	if err := d.DeleteBackup(context.Background(), "mysql", "app/"+name); err == nil {
		t.Fatal("DeleteBackup followed a symlinked service root")
	}
}

func TestDownloadBackupRejectsTraversal(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	for _, backupID := range []string{"../../etc/passwd", "app/../x.sql.gz", "app/x.sql", ""} {
		if _, err := d.DownloadBackup(context.Background(), "mysql", backupID); err == nil {
			t.Fatalf("DownloadBackup accepted %q", backupID)
		}
	}
}

func TestPostgresBackupUsesAdminUserEnvironment(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("dump")
	if _, err := d.CreateBackup(context.Background(), "postgres", "app", databases.CreateBackupRequest{}); err != nil {
		t.Fatal(err)
	}
	joined := runner.joinedCalls()
	if !strings.Contains(joined, "PGUSER=postgres") || !strings.Contains(joined, "PGPASSWORD=") {
		t.Fatalf("postgres backup wrapper = %s", joined)
	}
}

func TestCreateBackupFailsWhenDumpProcessFails(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("partial dump")
	runner.streamCloseErr = errors.New("mysqldump exited 2: access denied")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err == nil {
		t.Fatal("CreateBackup reported success for a failed dump")
	}
	if job.Status != model.JobFailed {
		t.Fatalf("job = %+v, want failed", job)
	}
	matches, _ := filepath.Glob(filepath.Join(d.root, "backups", "databases", "mysql", "app", "*.sql.gz"))
	if len(matches) != 0 {
		t.Fatalf("failed dump left a ready backup: %v", matches)
	}
}

func TestExportHandlerAbortsOnDumpFailure(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("partial dump")
	runner.streamCloseErr = errors.New("pg_dump exited 1")
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/services/mysql/databases/app/export")
	if err != nil {
		return
	}
	defer func() { _ = response.Body.Close() }()
	if _, readErr := io.ReadAll(response.Body); readErr == nil {
		t.Fatal("export returned a clean 200 for a failed dump")
	}
}

func TestFailedRestoreReturnsPreRestoreBackupID(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
	runner.runOutputs["information_schema.SCHEMATA"] = []byte("app\t0\t0\n")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	runner.failOnStreamInput = "CREATE TABLE"
	body, _ := json.Marshal(dbproto.BackupRestoreRequest{
		ID: "mysql", BackupID: job.BackupID, Target: "app", Overwrite: true, Confirm: "app", PreRestoreBackup: true,
	})
	recorder := serveDatabase(t, d, http.MethodPost, dbproto.ServiceBackupRestore("mysql", job.BackupID), body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	var response dbproto.JobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Job.Status != model.JobFailed {
		t.Fatalf("job = %+v, want failed", response.Job)
	}
	if response.Job.BackupID == "" {
		t.Fatal("failed restore dropped the pre-restore backup ID")
	}
}

func TestFailedReplaceImportReturnsPreImportBackupID(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["information_schema.PROCESSLIST"] = []byte("KILL 42;\n")
	runner.streamOutput = []byte("dump")
	runner.failOnStreamInput = "CREATE TABLE"
	upload := gzipBytes(t, "CREATE TABLE t (id int);\n")
	req := httptest.NewRequest(http.MethodPost, "/v1/services/mysql/databases/app/import?mode=replace&confirm=app&pre_import_backup=true", bytes.NewReader(upload))
	req.Header.Set("X-Devstack-Filename", "dump.sql.gz")
	req.Header.Set("Content-Type", "application/gzip")
	recorder := httptest.NewRecorder()
	d.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	var response dbproto.JobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Job.Status != model.JobFailed || response.Job.BackupID == "" {
		t.Fatalf("job = %+v, want failed with backup_id", response.Job)
	}
}

func TestRedisBackupOperationsUnsupported(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	if _, err := d.DownloadBackup(context.Background(), "redis", "app/x.sql.gz"); !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("DownloadBackup(redis) = %v", err)
	}
	if err := d.DeleteBackup(context.Background(), "redis", "app/x.sql.gz"); !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("DeleteBackup(redis) = %v", err)
	}
}

func TestImportRejectsNilReader(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	_, err := d.ImportDatabase(context.Background(), "mysql", "app", nil, "dump.sql", databases.ImportRequest{Mode: databases.ImportMerge})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("nil import invoked Docker: %v", runner.joinedCalls())
	}
}

func TestDownloadBackupRejectsInsecureMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	d, _ := newDatabaseTestDocker(t)
	dir := filepath.Join(d.root, "backups", "databases", "mysql", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "20260101T000000.000000000Z.sql.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DownloadBackup(context.Background(), "mysql", "app/"+name); err == nil {
		t.Fatal("downloaded a backup with insecure permissions")
	}
}
