package agentdocker

import (
	"context"
	"errors"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestRedactSQLSecrets(t *testing.T) {
	cases := []string{
		"ERROR 1064: ALTER USER 'app'@'%' IDENTIFIED BY 's3cret'",
		"ERROR: CREATE ROLE \"app\" WITH LOGIN PASSWORD 's3cret'",
		"ERROR: ALTER USER 'app'@'%' IDENTIFIED WITH caching_sha2_password BY 's3cret'",
		"ERROR: IDENTIFIED BY 'it''s-s3cret'",
	}
	for _, input := range cases {
		output := redactSQLSecrets(input)
		if strings.Contains(output, "s3cret") {
			t.Fatalf("redaction left the password in %q", output)
		}
		if !strings.Contains(output, "[redacted]") {
			t.Fatalf("redaction did not mark the secret in %q", output)
		}
	}
	if got := redactSQLSecrets("ERROR: relation does not exist"); got != "ERROR: relation does not exist" {
		t.Fatalf("redaction changed an unrelated error: %q", got)
	}
}

func TestSetUserPasswordRedactsEngineError(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	userID := databaseUserID("mysql", "app", "%")
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\n")
	runner.inputErr = errors.New("ERROR 1064: ALTER USER 'app'@'%' IDENTIFIED BY 's3cret'")
	_, err := d.SetUserPassword(context.Background(), "mysql", userID)
	if err == nil {
		t.Fatal("SetUserPassword ignored the engine error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("engine error leaked the password: %v", err)
	}
}

func TestDatabaseErrorResponseRedactsSQLSecrets(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeDatabaseError(recorder, errors.New("ERROR: PASSWORD 's3cret'"))
	if strings.Contains(recorder.Body.String(), "s3cret") {
		t.Fatalf("HTTP error leaked the password: %s", recorder.Body.String())
	}
}

func TestFailedDatabaseJobRedactsSQLSecrets(t *testing.T) {
	job, err := failDatabaseJob(newDatabaseJob("mysql", "app", model.JobBackup), errors.New("ERROR: IDENTIFIED BY 's3cret'"))
	if err == nil {
		t.Fatal("failDatabaseJob returned no error")
	}
	if strings.Contains(job.Message, "s3cret") || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("failed job leaked the password: %q / %v", job.Message, err)
	}
}

func TestExecRunnerRedactsPasswordErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	_, err := (execRunner{}).Run(context.Background(), "sh", "-c", `echo "ALTER USER 'app'@'%' IDENTIFIED BY 's3cret'" >&2; exit 1`)
	if err == nil {
		t.Fatal("execRunner reported success for a failed command")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("execRunner leaked the password: %v", err)
	}
}

func TestDatabaseErrorPreservesSentinelsAfterRedaction(t *testing.T) {
	err := redactSQLError(errors.Join(databases.ErrNotFound, errors.New("PASSWORD 's3cret'")))
	if !errors.Is(err, databases.ErrNotFound) {
		t.Fatalf("redaction dropped a sentinel: %v", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("redaction leaked the password: %v", err)
	}
}
