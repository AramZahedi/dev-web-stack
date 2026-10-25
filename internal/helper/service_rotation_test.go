package helper_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

var rotationSecretPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type rotationRunner struct {
	mu sync.Mutex

	calls     [][]string
	inputs    [][]byte
	psOutput  string
	psAfterUp string
	upSeen    bool

	runInputErr error
	onInput     func(string) error
}

func (r *rotationRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	for _, arg := range args {
		if arg == "ps" {
			return []byte(r.psOutput), nil
		}
		if arg == "up" && r.psAfterUp != "" {
			r.psOutput = r.psAfterUp
			r.upSeen = true
		}
	}
	return []byte("ok"), nil
}

func (r *rotationRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	r.inputs = append(r.inputs, append([]byte(nil), input...))
	if r.runInputErr != nil {
		return nil, r.runInputErr
	}
	if r.onInput != nil {
		if err := r.onInput(string(input)); err != nil {
			return nil, err
		}
	}
	return []byte("ok"), nil
}

func (r *rotationRunner) probe(context.Context, int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.upSeen {
		return nil
	}
	return errors.New("port is not listening")
}

func (r *rotationRunner) snapshot() ([][]string, [][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([][]string, len(r.calls))
	for i := range r.calls {
		calls[i] = append([]string(nil), r.calls[i]...)
	}
	inputs := make([][]byte, len(r.inputs))
	for i := range r.inputs {
		inputs[i] = append([]byte(nil), r.inputs[i]...)
	}
	return calls, inputs
}

func rotationCatalog() services.Catalog {
	return services.Catalog{Services: []services.Service{
		{
			ID:          services.IDMySQL,
			DisplayName: "MySQL",
			Group:       services.GroupMySQLFamily,
			Image:       testImage("mysql", 'c'),
			Ports:       []services.Port{{Host: 43006, Container: 3306, Protocol: "tcp", Loopback: true}},
		},
		{
			ID:          services.IDMariaDB,
			DisplayName: "MariaDB",
			Group:       services.GroupMySQLFamily,
			Image:       testImage("mariadb", 'd'),
			Ports:       []services.Port{{Host: 43007, Container: 3306, Protocol: "tcp", Loopback: true}},
		},
		{
			ID:          services.IDPostgreSQL,
			DisplayName: "PostgreSQL",
			Group:       services.GroupStandalone,
			Image:       testImage("postgres", 'e'),
			Ports:       []services.Port{{Host: 45432, Container: 5432, Protocol: "tcp", Loopback: true}},
		},
		{
			ID:          services.IDPHPMyAdmin,
			DisplayName: "phpMyAdmin",
			Group:       services.GroupStandalone,
			Image:       testImage("phpmyadmin", 'f'),
			Ports:       []services.Port{{Host: 48082, Container: 80, Protocol: "http", Loopback: true}},
		},
	}}
}

func runningPS(ids ...services.ID) string {
	entries := make([]string, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, fmt.Sprintf(`{"ID":"cid-%s","Name":"devstack-%s","Service":"%s","State":"running","Health":"healthy"}`, id, id, id))
	}
	return "[" + strings.Join(entries, ",") + "]"
}

func rotationFixture(t *testing.T, id services.ID) (string, *credentials.Store, *rotationRunner, *helper.DockerServiceController) {
	t.Helper()
	root := t.TempDir()
	store := &credentials.Store{Root: root}
	if _, err := store.Write(string(id), "devstack_password"); err != nil {
		t.Fatal(err)
	}
	if id != services.IDPostgreSQL {
		if _, err := store.Write(string(id), "root_password"); err != nil {
			t.Fatal(err)
		}
	}
	runner := &rotationRunner{psOutput: runningPS(id)}
	controller := helper.NewDockerServiceController(root, rotationCatalog(), runner)
	controller.Probe = func(context.Context, int) error { return nil }
	return root, store, runner, controller
}

func readRotationSecret(t *testing.T, store *credentials.Store, id services.ID) string {
	t.Helper()
	value, err := store.Read(string(id), "devstack_password")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRotateDatabaseCredentialsMySQL(t *testing.T) {
	root, store, runner, controller := rotationFixture(t, services.IDMySQL)
	old := readRotationSecret(t, store, services.IDMySQL)

	if err := controller.RotateCredentials(context.Background(), services.IDMySQL); err != nil {
		t.Fatalf("RotateCredentials() = %v", err)
	}

	newSecret := readRotationSecret(t, store, services.IDMySQL)
	if newSecret == old {
		t.Fatal("RotateCredentials() kept the old secret")
	}
	if !rotationSecretPattern.MatchString(newSecret) {
		t.Fatalf("new secret = %q, want 64 lowercase hex characters", newSecret)
	}
	requireFileMode(t, filepath.Join(root, "secrets", "mysql", "devstack_password"), 0o600)

	calls, inputs := runner.snapshot()
	if len(inputs) != 1 {
		t.Fatalf("stdin calls = %d, want 1: %v", len(inputs), calls)
	}
	sql := string(inputs[0])
	if !strings.Contains(sql, "ALTER USER 'devstack'@'%' IDENTIFIED BY '"+newSecret+"'") {
		t.Fatalf("rotation SQL does not set the new password:\n%s", sql)
	}
	if !strings.Contains(sql, "FLUSH PRIVILEGES") {
		t.Fatalf("rotation SQL does not flush privileges:\n%s", sql)
	}
	assertNoSecretInArgv(t, calls, old, newSecret)
	foundMySQL := false
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), "mysql --user=root") {
			foundMySQL = true
		}
	}
	if !foundMySQL {
		t.Fatalf("rotation did not exec the fixed mysql client command: %v", calls)
	}
}

func TestRotateDatabaseCredentialsMariaDB(t *testing.T) {
	_, store, runner, controller := rotationFixture(t, services.IDMariaDB)
	old := readRotationSecret(t, store, services.IDMariaDB)

	if err := controller.RotateCredentials(context.Background(), services.IDMariaDB); err != nil {
		t.Fatalf("RotateCredentials() = %v", err)
	}
	newSecret := readRotationSecret(t, store, services.IDMariaDB)
	if newSecret == old || !rotationSecretPattern.MatchString(newSecret) {
		t.Fatalf("new secret = %q, old = %q", newSecret, old)
	}
	calls, inputs := runner.snapshot()
	if len(inputs) != 1 || !strings.Contains(string(inputs[0]), "ALTER USER 'devstack'@'%'") {
		t.Fatalf("rotation input = %q", string(inputs[0]))
	}
	found := false
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), "mariadb --user=root") {
			found = true
		}
	}
	if !found {
		t.Fatalf("rotation did not exec the fixed mariadb client command: %v", calls)
	}
	assertNoSecretInArgv(t, calls, old, newSecret)
}

func TestRotateDatabaseCredentialsPostgreSQL(t *testing.T) {
	_, store, runner, controller := rotationFixture(t, services.IDPostgreSQL)
	old := readRotationSecret(t, store, services.IDPostgreSQL)

	if err := controller.RotateCredentials(context.Background(), services.IDPostgreSQL); err != nil {
		t.Fatalf("RotateCredentials() = %v", err)
	}
	newSecret := readRotationSecret(t, store, services.IDPostgreSQL)
	if newSecret == old || !rotationSecretPattern.MatchString(newSecret) {
		t.Fatalf("new secret = %q, old = %q", newSecret, old)
	}
	calls, inputs := runner.snapshot()
	if len(inputs) != 1 || !strings.Contains(string(inputs[0]), "ALTER ROLE devstack WITH PASSWORD '"+newSecret+"'") {
		t.Fatalf("rotation input = %q", string(inputs[0]))
	}
	if !strings.Contains(strings.Join(calls[len(calls)-1], " "), "psql --no-psqlrc") {
		t.Fatalf("rotation did not exec psql: %v", calls)
	}
	assertNoSecretInArgv(t, calls, old, newSecret)
}

func TestRotateDatabaseCredentialsRequiresActiveContainer(t *testing.T) {
	_, store, runner, controller := rotationFixture(t, services.IDMySQL)
	runner.psOutput = "[]"
	old := readRotationSecret(t, store, services.IDMySQL)

	err := controller.RotateCredentials(context.Background(), services.IDMySQL)
	if !errors.Is(err, helperproto.ErrServiceConflict) {
		t.Fatalf("RotateCredentials() error = %v, want ErrServiceConflict", err)
	}
	if got := readRotationSecret(t, store, services.IDMySQL); got != old {
		t.Fatal("failed rotation changed the stored secret")
	}
	_, inputs := runner.snapshot()
	if len(inputs) != 0 {
		t.Fatalf("rotation ran %d database commands for an inactive service", len(inputs))
	}
}

func TestRotateDatabaseCredentialsRejectsUnsupportedService(t *testing.T) {
	_, store, runner, controller := rotationFixture(t, services.IDMySQL)
	if err := controller.RotateCredentials(context.Background(), services.IDRedis); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("RotateCredentials(redis) error = %v, want ErrInvalidRequest", err)
	}
	if _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatalf("unsupported rotation ran %d commands", len(inputs))
	}
	_ = store
}

func TestRotateDatabaseCredentialsRejectsNonManagedSecret(t *testing.T) {
	_, store, runner, controller := rotationFixture(t, services.IDMySQL)
	if err := store.Replace("mysql", "devstack_password", "manually-set-password"); err != nil {
		t.Fatal(err)
	}
	err := controller.RotateCredentials(context.Background(), services.IDMySQL)
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("RotateCredentials() error = %v, want ErrInvalidRequest", err)
	}
	if strings.Contains(err.Error(), "manually-set-password") {
		t.Fatal("rotation error leaks the stored secret")
	}
	if _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatalf("non-managed rotation ran %d commands", len(inputs))
	}
}

func TestRotateDatabaseCredentialsKeepsOldSecretOnSQLFailure(t *testing.T) {
	_, store, runner, controller := rotationFixture(t, services.IDMySQL)
	old := readRotationSecret(t, store, services.IDMySQL)
	runner.runInputErr = errors.New("ERROR 1045: access denied")

	err := controller.RotateCredentials(context.Background(), services.IDMySQL)
	if err == nil {
		t.Fatal("RotateCredentials() = nil, want an error")
	}
	if got := readRotationSecret(t, store, services.IDMySQL); got != old {
		t.Fatal("failed rotation changed the stored secret")
	}
	if strings.Contains(err.Error(), old) {
		t.Fatal("rotation error leaks the old secret")
	}
}

type failingSecretStore struct {
	old        string
	replaceErr error
}

func (s *failingSecretStore) Read(_, _ string) (string, error) { return s.old, nil }

func (s *failingSecretStore) Replace(_, _, _ string) error { return s.replaceErr }

func TestRotateDatabaseCredentialsRestoresAccountWhenSecretWriteFails(t *testing.T) {
	old := strings.Repeat("a", 64)
	store := &failingSecretStore{old: old, replaceErr: errors.New("disk full")}
	runner := &rotationRunner{psOutput: runningPS(services.IDMySQL)}
	root := t.TempDir()
	controller := helper.NewDockerServiceController(root, rotationCatalog(), runner)
	controller.Secrets = store

	err := controller.RotateCredentials(context.Background(), services.IDMySQL)
	if err == nil {
		t.Fatal("RotateCredentials() = nil, want an error")
	}
	_, inputs := runner.snapshot()
	if len(inputs) != 2 {
		t.Fatalf("database statements = %d, want update and rollback: %q", len(inputs), inputs)
	}
	if !strings.Contains(string(inputs[1]), "IDENTIFIED BY '"+old+"'") {
		t.Fatalf("rollback statement does not restore the old account password:\n%s", inputs[1])
	}
	if strings.Contains(err.Error(), old) {
		t.Fatal("rollback error leaks the old secret")
	}
}

func TestActivatePhpMyAdminWithActiveDatabase(t *testing.T) {
	runner := &rotationRunner{
		psOutput:  runningPS(services.IDMySQL),
		psAfterUp: runningPS(services.IDMySQL, services.IDPHPMyAdmin),
	}
	controller := helper.NewDockerServiceController(t.TempDir(), rotationCatalog(), runner)
	controller.Probe = runner.probe
	controller.WaitTimeout = time.Second

	if err := controller.Activate(context.Background(), services.IDPHPMyAdmin); err != nil {
		t.Fatalf("Activate(phpmyadmin) = %v", err)
	}
	calls, _ := runner.snapshot()
	found := false
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), "up -d --no-deps phpmyadmin") {
			found = true
		}
	}
	if !found {
		t.Fatalf("phpmyadmin was not started: %v", calls)
	}
}

func TestActivatePhpMyAdminRequiresActiveDatabase(t *testing.T) {
	runner := &rotationRunner{psOutput: "[]"}
	controller := helper.NewDockerServiceController(t.TempDir(), rotationCatalog(), runner)
	controller.Probe = func(context.Context, int) error { return nil }

	err := controller.Activate(context.Background(), services.IDPHPMyAdmin)
	if !errors.Is(err, helperproto.ErrServiceConflict) {
		t.Fatalf("Activate(phpmyadmin) error = %v, want ErrServiceConflict", err)
	}
	if !strings.Contains(err.Error(), "requires an active MySQL or MariaDB") {
		t.Fatalf("error = %v, want a clear dependency message", err)
	}
	if _, inputs := runner.snapshot(); len(inputs) != 0 {
		t.Fatal("phpmyadmin activation ran database commands")
	}
}

func assertNoSecretInArgv(t *testing.T, calls [][]string, secrets ...string) {
	t.Helper()
	for _, call := range calls {
		for _, arg := range call {
			for _, secret := range secrets {
				if secret != "" && strings.Contains(arg, secret) {
					t.Fatalf("secret leaked into command argv: %v", call)
				}
			}
		}
	}
}

func requireFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}
