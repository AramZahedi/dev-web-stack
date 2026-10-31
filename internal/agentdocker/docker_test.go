package agentdocker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentdocker"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeRunner struct {
	calls [][]string
	err   error
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, r.err
}

type fakeInputRunner struct {
	calls  [][]string
	inputs [][]byte
	output map[string][]byte
	err    error
}

func (r *fakeInputRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, append([]string{name}, args...))
	if out, ok := r.output[key]; ok {
		return out, r.err
	}
	for pattern, out := range r.output {
		if strings.Contains(key, pattern) {
			return out, r.err
		}
	}
	return nil, r.err
}

func (r *fakeInputRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	r.inputs = append(r.inputs, input)
	return r.Run(context.Background(), name, args...)
}

func TestAllowedServiceIDsAreFixedCatalog(t *testing.T) {
	want := []string{"apache", "mariadb", "mysql", "nginx", "phpmyadmin", "postgres", "redis"}
	got := agentdocker.AllowedServiceIDs()
	if len(got) != len(want) {
		t.Fatalf("AllowedServiceIDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllowedServiceIDs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

type offlinePullRunner struct {
	calls      [][]string
	localImage bool
}

func (r *offlinePullRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if len(args) > 0 && args[0] == "image" {
		if r.localImage {
			return []byte("sha256:local\n"), nil
		}
		return nil, errors.New("image not found")
	}
	if len(args) > 1 && args[len(args)-2] == "pull" {
		return nil, errors.New("registry unreachable")
	}
	return nil, nil
}

func TestInstallFallsBackToLocalDigestPinnedImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixed service Compose mounts are POSIX paths and execute in WSL")
	}
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &offlinePullRunner{localImage: true}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Install(context.Background(), "mysql"); err != nil {
		t.Fatalf("Install with a local digest-pinned image = %v", err)
	}
	inspected := false
	for _, call := range runner.calls {
		if len(call) >= 3 && call[1] == "image" && call[2] == "inspect" {
			inspected = true
		}
	}
	if !inspected {
		t.Fatalf("Docker calls = %v, want a local image inspect fallback", runner.calls)
	}

	missing := &offlinePullRunner{}
	module, err = agentdocker.New(agentdocker.Config{Root: filepath.Join(t.TempDir(), "data", "devstack"), Runner: missing})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Install(context.Background(), "mysql"); err == nil || !strings.Contains(err.Error(), "pull mysql") {
		t.Fatalf("Install without a local image = %v, want pull failure", err)
	}
}

func TestComposeUsesFixedProjectDigestImagesAndWSLStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixed service Compose mounts are POSIX paths and execute in WSL")
	}
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeRunner{}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if got := module.ProjectName(); got != "devstack" {
		t.Fatalf("ProjectName() = %q, want devstack", got)
	}
	if err := module.Install(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join(root, "services", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	if strings.Contains(text, "C:\\") || strings.Contains(text, "\\wsl.localhost") {
		t.Fatalf("Compose embeds a Windows/UNC bind mount:\n%s", text)
	}
	if !strings.Contains(text, "services/postgres/data") || strings.Contains(text, "services/postgresql/data") {
		t.Fatalf("Compose does not use the fixed postgres service ID and WSL data path:\n%s", text)
	}
	if !strings.Contains(text, "services/mysql/secrets/root_password") || strings.Contains(text, "secrets/mysql/root_password") {
		t.Fatalf("Compose secrets are not stored below the per-service ext4 tree:\n%s", text)
	}
	for _, service := range services.DefaultCatalog().Services {
		if !strings.Contains(service.Image, "@sha256:") {
			t.Errorf("service %q image is not digest pinned: %q", service.ID, service.Image)
		}
	}
	if len(runner.calls) != 1 || len(runner.calls[0]) < 8 ||
		runner.calls[0][1] != "compose" || runner.calls[0][2] != "--project-name" || runner.calls[0][3] != "devstack" ||
		runner.calls[0][4] != "--file" || runner.calls[0][len(runner.calls[0])-2] != "pull" || runner.calls[0][len(runner.calls[0])-1] != "mysql" {
		t.Fatalf("Docker calls = %v, want fixed Compose project pull", runner.calls)
	}
	for _, id := range []string{"mysql", "nginx"} {
		for _, dir := range []string{"config", "data", "logs"} {
			if _, err := filepath.Abs(filepath.Join(root, "services", id, dir)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestLifecycleRejectsArbitraryIDsAndCommands(t *testing.T) {
	module, err := agentdocker.New(agentdocker.Config{Root: filepath.Join(t.TempDir(), "wsl-root"), Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []agentproto.ServiceRequest{
		{ID: "untrusted-image", Action: "install"},
		{ID: "mysql", Action: "exec", Config: map[string]string{"command": "id"}},
		{ID: "mysql", Action: "compose", Config: map[string]string{"file": "./attacker.yml"}},
	} {
		if _, err := module.Action(context.Background(), action); err == nil {
			t.Errorf("Action(%+v) accepted arbitrary service/action input", action)
		}
	}
}

func TestWebValidationAndReloadUseCatalogOwnedCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixed service Compose mounts are POSIX paths and execute in WSL")
	}
	runner := &fakeRunner{}
	module, err := agentdocker.New(agentdocker.Config{Root: filepath.Join(t.TempDir(), "wsl-root"), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []agentproto.ServiceRequest{
		{ID: "nginx", Action: "validate"},
		{ID: "apache", Action: "reload"},
	} {
		if _, err := module.Action(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.calls) != 2 || !containsSequence(runner.calls[0], []string{"exec", "-T", "nginx", "nginx", "-t"}) ||
		!containsSequence(runner.calls[1], []string{"exec", "-T", "apache", "apachectl", "graceful"}) {
		t.Fatalf("validation/reload Docker calls = %v", runner.calls)
	}
	before := len(runner.calls)
	if _, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: "mysql", Action: "validate", Config: map[string]string{"command": "id"}}); err == nil {
		t.Fatal("non-web validation accepted caller-supplied commands")
	}
	if len(runner.calls) != before {
		t.Fatalf("rejected validation still invoked Docker: %v", runner.calls)
	}
}

func TestModuleMountsAuthenticatedServiceRoutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixed service Compose mounts are POSIX paths and execute in WSL")
	}
	runner := &fakeRunner{}
	module, err := agentdocker.New(agentdocker.Config{Root: filepath.Join(t.TempDir(), "wsl-root"), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	agentapi.Mount(mux, module)
	requestBody, _ := json.Marshal(agentproto.ServiceRequest{ID: "mysql", Action: "install"})
	req := httptest.NewRequest(http.MethodPost, agentproto.PathServiceActionFor("mysql"), bytes.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("module response = %d %s", recorder.Code, recorder.Body.String())
	}
	var response agentproto.ServiceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "mysql" || response.State == "" {
		t.Fatalf("response = %+v, want installed mysql", response)
	}
}

func containsSequence(got, want []string) bool {
	for i := 0; i+len(want) <= len(got); i++ {
		match := true
		for j := range want {
			if got[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestCredentialRotationUpdatesLiveAccountAndSecret(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeInputRunner{
		output: map[string][]byte{
			"compose --project-name devstack --file": []byte(`[{"State":"running"}]`),
		},
	}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	secretsDir := filepath.Join(root, "services", "mysql", "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldSecret := "1111111111111111111111111111111111111111111111111111111111111111"
	if err := os.WriteFile(filepath.Join(secretsDir, "devstack_password"), []byte(oldSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: "mysql", Action: "rotate-credentials"})
	if err != nil {
		t.Fatalf("Action(rotate-credentials) failed: %v", err)
	}
	if res.ID != "mysql" || res.State != "active" {
		t.Fatalf("Action(rotate-credentials) = %+v, want mysql/active", res)
	}

	newSecretBytes, err := os.ReadFile(filepath.Join(secretsDir, "devstack_password"))
	if err != nil {
		t.Fatal(err)
	}
	newSecret := strings.TrimSpace(string(newSecretBytes))
	if newSecret == oldSecret {
		t.Fatal("secret was not updated after rotation")
	}
	if len(newSecret) != 64 {
		t.Fatalf("new secret length = %d, want 64 hex characters", len(newSecret))
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("expected 1 SQL input execution, got %d", len(runner.inputs))
	}
	if !strings.Contains(string(runner.inputs[0]), "ALTER USER 'devstack'@'%' IDENTIFIED BY '"+newSecret+"'") {
		t.Fatalf("SQL input = %q, want ALTER USER with new secret", string(runner.inputs[0]))
	}
}

func TestCredentialRotationRejectsUnsupportedServicesAndStoppedState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeInputRunner{
		output: map[string][]byte{
			"compose --project-name devstack --file": []byte(`[{"State":"exited"}]`),
		},
	}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: "redis", Action: "rotate-credentials"}); err == nil {
		t.Fatal("expected error rotating redis credentials")
	}
	if _, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: "mysql", Action: "rotate-credentials"}); err == nil {
		t.Fatal("expected error rotating stopped mysql service")
	}
}

func TestScaffoldSeedsBaseConfigs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeRunner{}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}

	for _, svc := range []struct {
		id       string
		fileRel  string
		expected string
	}{
		{"redis", filepath.Join("config", "redis.conf"), "appendonly yes"},
		{"mysql", filepath.Join("config", "my.cnf"), "[mysqld]"},
		{"mariadb", filepath.Join("config", "my.cnf"), "[mysqld]"},
		{"postgres", filepath.Join("config", "postgresql.conf"), "listen_addresses = '*'"},
		{"phpmyadmin", filepath.Join("config", "config.user.inc.php"), "$cfg['UploadDir']"},
	} {
		if _, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: svc.id, Action: "install"}); err != nil {
			t.Fatalf("install %s: %v", svc.id, err)
		}
		path := filepath.Join(root, "services", svc.id, svc.fileRel)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing seeded config for %s at %s: %v", svc.id, path, err)
		}
		if !strings.Contains(string(content), svc.expected) {
			t.Fatalf("config for %s does not contain %q:\n%s", svc.id, svc.expected, string(content))
		}
	}
}

func TestScaffoldSeedsPhpMyAdminServerConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeRunner{}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: "phpmyadmin", Action: "install"}); err != nil {
		t.Fatalf("install phpmyadmin: %v", err)
	}
	path := filepath.Join(root, "services", "phpmyadmin", "config", "config.user.inc.php")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"$cfg['Servers'][1]['host'] = 'db';",
		"$cfg['Servers'][1]['port'] = '3306';",
		"$cfg['Servers'][1]['auth_type'] = 'cookie';",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("phpMyAdmin config missing %q:\n%s", want, content)
		}
	}
}

func TestStatusDecodesArrayObjectAndNDJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data", "devstack")
	runner := &fakeInputRunner{
		output: map[string][]byte{
			"compose --project-name devstack --file": []byte(`{"State":"running","Health":"healthy"}`),
		},
	}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := module.Status(context.Background(), "redis")
	if err != nil {
		t.Fatalf("Status(redis) = %v", err)
	}
	if resp.State != "active" {
		t.Fatalf("State = %q, want active", resp.State)
	}
}
