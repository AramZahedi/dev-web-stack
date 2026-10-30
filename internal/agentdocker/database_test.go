package agentdocker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type databaseCall struct {
	args  []string
	input []byte
}

type fakeDatabaseRunner struct {
	mu           sync.Mutex
	calls        []databaseCall
	runOutputs   map[string][]byte
	streamOutput   []byte
	streamErr      error
	streamCloseErr error
	inputErr       error
	failOnInput    string
	state          string
}

type fakeReadCloser struct {
	*bytes.Reader
	closeErr error
}

func (r *fakeReadCloser) Close() error { return r.closeErr }

func (r *fakeDatabaseRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, databaseCall{args: append([]string{name}, args...)})
	r.mu.Unlock()
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "ps -a") {
		state := r.state
		if state == "" {
			state = "running"
		}
		return []byte(`[{"State":"` + state + `"}]`), nil
	}
	for pattern, out := range r.runOutputs {
		if strings.Contains(joined, pattern) {
			return out, nil
		}
	}
	return nil, nil
}

func (r *fakeDatabaseRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, databaseCall{args: append([]string{name}, args...), input: append([]byte(nil), input...)})
	failOn := r.failOnInput
	r.mu.Unlock()
	if failOn != "" && strings.Contains(string(input), failOn) {
		return nil, errors.New("simulated input failure")
	}
	if r.inputErr != nil {
		return nil, r.inputErr
	}
	joined := strings.Join(args, " ")
	for pattern, out := range r.runOutputs {
		if strings.Contains(joined, pattern) {
			return out, nil
		}
	}
	return nil, nil
}

func (r *fakeDatabaseRunner) RunStream(_ context.Context, name string, args ...string) (io.ReadCloser, error) {
	r.mu.Lock()
	r.calls = append(r.calls, databaseCall{args: append([]string{name}, args...)})
	r.mu.Unlock()
	if r.streamErr != nil {
		return nil, r.streamErr
	}
	return &fakeReadCloser{Reader: bytes.NewReader(r.streamOutput), closeErr: r.streamCloseErr}, nil
}

func (r *fakeDatabaseRunner) RunInputStream(_ context.Context, input io.Reader, name string, args ...string) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.calls = append(r.calls, databaseCall{args: append([]string{name}, args...), input: data})
	r.mu.Unlock()
	return r.inputErr
}

func (r *fakeDatabaseRunner) joinedCalls() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var parts []string
	for _, call := range r.calls {
		parts = append(parts, strings.Join(call.args, " "))
	}
	return strings.Join(parts, "\n")
}

func (r *fakeDatabaseRunner) contains(fragment string) bool {
	return strings.Contains(r.joinedCalls(), fragment)
}

func (r *fakeDatabaseRunner) stdinContains(fragment string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.Contains(string(call.input), fragment) {
			return true
		}
	}
	return false
}

func (r *fakeDatabaseRunner) inputFor(fragment string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call.args, " "), fragment) {
			return string(call.input)
		}
	}
	return ""
}

func newDatabaseTestDocker(t *testing.T) (*Docker, *fakeDatabaseRunner) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeDatabaseRunner{runOutputs: make(map[string][]byte)}
	docker, err := New(Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	return docker, runner
}

func TestDatabaseSecretRejectsTraversal(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	for _, userID := range []string{"../root", "..", "a/b", `a\b`, "usr.app", ""} {
		if err := d.writeDatabaseSecret("mysql", userID, "x"); err == nil {
			t.Fatalf("accepted traversal user ID %q", userID)
		}
	}
}

func TestDatabaseSecretRejectsUnsupportedService(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	if err := d.writeDatabaseSecret("nginx", "usr_app", "x"); err == nil {
		t.Fatal("accepted a non-database service")
	}
}

func TestDatabaseSecretIsPrivate(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	if err := d.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.root, "services", "mysql", "secrets", "database-users", "usr_app")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
		}
	}
	value, err := d.readDatabaseSecret("mysql", "usr_app")
	if err != nil || value != "s3cret" {
		t.Fatalf("read = %q, %v", value, err)
	}
	if err := d.deleteDatabaseSecret("mysql", "usr_app"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.readDatabaseSecret("mysql", "usr_app"); err == nil {
		t.Fatal("deleted secret is still readable")
	}
}

func TestDatabaseSecretRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	d, _ := newDatabaseTestDocker(t)
	dir := filepath.Join(d.root, "services", "mysql", "secrets", "database-users")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "usr_link")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.readDatabaseSecret("mysql", "usr_link"); err == nil {
		t.Fatal("read a symlinked secret")
	}
	if err := d.writeDatabaseSecret("mysql", "usr_link", "x"); err == nil {
		t.Fatal("wrote through a symlinked secret")
	}
}

func TestPerServiceLocksAreIndependent(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	if d.databaseLock("mysql") == d.databaseLock("postgres") {
		t.Fatal("services share a database lock")
	}
	if d.databaseLock("mysql") != d.databaseLock("mysql") {
		t.Fatal("the same service does not share its lock")
	}
}

func TestDatabaseEngineRejectsUnknownService(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	if _, _, err := d.databaseEngine("nginx"); err == nil {
		t.Fatal("nginx resolved to a database engine")
	}
	engine, service, err := d.databaseEngine("mysql")
	if err != nil {
		t.Fatal(err)
	}
	if engine.ID() != "mysql" || service.ID != services.IDMySQL {
		t.Fatalf("engine=%s service=%s", engine.ID(), service.ID)
	}
}

func TestLimitedBufferTruncates(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 10
	n, err := buffer.Write([]byte("123456789012345"))
	if err != nil || n != 15 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if buffer.String() != "1234567890" {
		t.Fatalf("buffer = %q", buffer.String())
	}
}

func TestRunInputStreamDiscardsLargeStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	err := (execRunner{}).RunInputStream(context.Background(), strings.NewReader(""), "sh", "-c", "head -c 2000000 /dev/zero; echo boom >&2; exit 1")
	if err == nil {
		t.Fatal("RunInputStream reported success for a failed command")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr missing from error: %v", err)
	}
	if len(err.Error()) > 128*1024 {
		t.Fatalf("RunInputStream buffered large stdout in its error (%d bytes)", len(err.Error()))
	}
}
