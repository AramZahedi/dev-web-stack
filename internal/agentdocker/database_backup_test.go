package agentdocker

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
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
