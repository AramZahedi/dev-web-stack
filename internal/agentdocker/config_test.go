package agentdocker_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentdocker"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestServiceConfigReadApplyResetAndBackupStayAgentSide(t *testing.T) {
	root := t.TempDir()
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(root, "services", "mysql", "config")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(configRoot, "my.cnf")
	if err := os.WriteFile(file, []byte("old configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := agentproto.ServiceConfigRequest{ID: "mysql", Rel: "my.cnf"}
	content, err := module.ServiceContent(context.Background(), request)
	if err != nil || content.Content != "old configuration\n" {
		t.Fatalf("ServiceContent() = %+v, %v", content, err)
	}
	request.Content = "new configuration\n"
	result, err := module.ServiceApply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != request.Content || result.Path != "my.cnf" || result.Hash == "" {
		t.Fatalf("ServiceApply() = %+v", result)
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "services", "mysql", "config", "*", "my.cnf"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("config backups = %v, err %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || string(backup) != "old configuration\n" {
		t.Fatalf("apply backup = %q, err %v", backup, err)
	}
	if runtime.GOOS != "windows" {
		backupInfo, err := os.Stat(backups[0])
		if err != nil || backupInfo.Mode().Perm() != 0o600 {
			t.Fatalf("backup file mode = %v, err %v; want 0600", backupInfo, err)
		}
		backupDir, err := os.Stat(filepath.Dir(backups[0]))
		if err != nil || backupDir.Mode().Perm() != 0o700 {
			t.Fatalf("backup directory mode = %v, err %v; want 0700", backupDir, err)
		}
	}
	reset, err := module.ServiceReset(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Content != "old configuration\n" {
		t.Fatalf("ServiceReset() content = %q", reset.Content)
	}
}

type serviceApplyRunner struct {
	calls       [][]string
	state       string
	validateErr error
	reloadErr   error
}

func (r *serviceApplyRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	joined := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "ps -a --format json"):
		return []byte(`{"State":"` + r.state + `"}`), nil
	case strings.Contains(joined, "exec -T nginx nginx -t"):
		return nil, r.validateErr
	case strings.Contains(joined, "exec -T nginx nginx -s reload"):
		return nil, r.reloadErr
	default:
		return nil, nil
	}
}

func (r *serviceApplyRunner) called(fragment string) bool {
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call, " "), fragment) {
			return true
		}
	}
	return false
}

func TestWebServiceApplyValidatesAndReloadsWhenActive(t *testing.T) {
	root := t.TempDir()
	runner := &serviceApplyRunner{state: "running"}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "services", "nginx", "config", "conf.d", "custom")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(configDir, "e2e.conf")
	if err := os.WriteFile(file, []byte("old configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := module.ServiceApply(context.Background(), agentproto.ServiceConfigRequest{
		ID: "nginx", Rel: "conf.d/custom/e2e.conf", Content: "new configuration\n",
	}); err != nil {
		t.Fatalf("ServiceApply() = %v", err)
	}
	if !runner.called("exec -T nginx nginx -t") {
		t.Fatalf("ServiceApply() did not validate the active nginx config: %v", runner.calls)
	}
	if !runner.called("exec -T nginx nginx -s reload") {
		t.Fatalf("ServiceApply() did not reload nginx after a valid apply: %v", runner.calls)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "new configuration\n" {
		t.Fatalf("config after apply = %q, err %v", data, err)
	}
}

func TestWebServiceApplyRollsBackWhenValidationFails(t *testing.T) {
	root := t.TempDir()
	runner := &serviceApplyRunner{state: "running", validateErr: errors.New("nginx: configuration test failed")}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "services", "nginx", "config", "conf.d", "custom")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(configDir, "e2e.conf")
	if err := os.WriteFile(file, []byte("old configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := module.ServiceApply(context.Background(), agentproto.ServiceConfigRequest{
		ID: "nginx", Rel: "conf.d/custom/e2e.conf", Content: "invalid configuration\n",
	}); err == nil {
		t.Fatal("ServiceApply() accepted a config that failed nginx -t")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "old configuration\n" {
		t.Fatalf("config after failed validation = %q, err %v", data, err)
	}
	if runner.called("nginx -s reload") {
		t.Fatalf("failed validation still reloaded nginx: %v", runner.calls)
	}
}

func TestWebServiceApplyRestoresPreviousConfigWhenReloadFails(t *testing.T) {
	root := t.TempDir()
	runner := &serviceApplyRunner{state: "running", reloadErr: errors.New("nginx: reload failed")}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "services", "nginx", "config", "conf.d", "custom")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(configDir, "e2e.conf")
	if err := os.WriteFile(file, []byte("old configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := module.ServiceApply(context.Background(), agentproto.ServiceConfigRequest{
		ID: "nginx", Rel: "conf.d/custom/e2e.conf", Content: "new configuration\n",
	}); err == nil {
		t.Fatal("ServiceApply() accepted a config that failed to reload")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "old configuration\n" {
		t.Fatalf("config after failed reload = %q, err %v", data, err)
	}
	reloads := 0
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call, " "), "nginx -s reload") {
			reloads++
		}
	}
	if reloads < 2 {
		t.Fatalf("reload attempts = %d, want the failed reload plus a rollback reload", reloads)
	}
}

func TestWebServiceApplySkipsValidationWhenInactive(t *testing.T) {
	root := t.TempDir()
	runner := &serviceApplyRunner{state: "exited"}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "services", "nginx", "config", "conf.d", "custom")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(configDir, "e2e.conf")
	if err := os.WriteFile(file, []byte("old configuration\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := module.ServiceApply(context.Background(), agentproto.ServiceConfigRequest{
		ID: "nginx", Rel: "conf.d/custom/e2e.conf", Content: "staged configuration\n",
	}); err != nil {
		t.Fatal(err)
	}
	if runner.called("nginx -t") || runner.called("nginx -s reload") {
		t.Fatalf("inactive apply validated or reloaded: %v", runner.calls)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "staged configuration\n" {
		t.Fatalf("config after inactive apply = %q, err %v", data, err)
	}
}

func TestServiceConfigRejectsTraversalAndSensitivePaths(t *testing.T) {
	module, err := agentdocker.New(agentdocker.Config{Root: t.TempDir(), Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../outside", "/etc/passwd", `config\outside`, "secrets/password", "password.cnf", ".private.cnf"} {
		if _, err := module.ServiceContent(context.Background(), agentproto.ServiceConfigRequest{ID: "mysql", Rel: rel}); err == nil {
			t.Errorf("ServiceContent(%q) accepted an unsafe config path", rel)
		}
	}
}

func TestServiceConfigRejectsSymlinkedConfigRootAndFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("WSL symlink checks run on the Linux agent filesystem")
	}
	root := t.TempDir()
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "my.cnf"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(root, "services", "mysql", "config")
	if err := os.MkdirAll(filepath.Dir(configRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, configRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := module.ServiceContent(context.Background(), agentproto.ServiceConfigRequest{ID: "mysql", Rel: "my.cnf"}); err == nil {
		t.Fatal("config root symlink escaped the WSL service config tree")
	}
}

func TestServiceCredentialReadsOnlyFixedPrivateSecretFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX secret permission checks are verified on the WSL/Linux agent")
	}
	root := t.TempDir()
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "services", "postgres", "secrets", "postgres_password")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("postgres-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(secret), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := module.ServiceCredential(context.Background(), agentproto.ServiceCredentialRequest{ID: "postgres", Field: "postgres_password"}); err == nil {
		t.Fatal("credential was revealed while the secret directory was not private")
	}
	if err := os.Chmod(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	response, err := module.ServiceCredential(context.Background(), agentproto.ServiceCredentialRequest{ID: "postgres", Field: "postgres_password"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "postgres" || response.User != "postgres" || response.Value != "postgres-secret" {
		t.Fatalf("credential response = %+v", response)
	}
	if _, err := module.ServiceCredential(context.Background(), agentproto.ServiceCredentialRequest{ID: "postgres", Field: "arbitrary"}); err == nil {
		t.Fatal("arbitrary credential field was accepted")
	}
}

func TestInstallStoresPerServiceSecretsWithPrivateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX secret permissions are verified on the WSL/Linux agent")
	}
	root := t.TempDir()
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Install(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	secretRoot := filepath.Join(root, "services", "mysql", "secrets")
	if info, err := os.Stat(secretRoot); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("secrets directory = %v, err %v; want 0700", info, err)
	}
	for _, name := range []string{"root_password", "devstack_password"} {
		info, err := os.Stat(filepath.Join(secretRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("secret %s mode = %#o, want 0600", name, info.Mode().Perm())
		}
	}
	compose, err := os.ReadFile(filepath.Join(root, "services", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := filepath.ToSlash(string(compose))
	if !strings.Contains(text, filepath.ToSlash(filepath.Join(root, "services", "mysql", "secrets", "root_password"))) {
		t.Fatalf("Compose secret does not use per-service WSL path:\n%s", text)
	}
	if strings.Contains(text, filepath.ToSlash(filepath.Join(root, "secrets", "mysql"))) {
		t.Fatalf("Compose still mounts the old global secrets tree:\n%s", text)
	}
}

func TestScaffoldRejectsSymlinkedServiceData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("WSL service data ownership is verified on Linux")
	}
	root := t.TempDir()
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	serviceRoot := filepath.Join(root, "services", "mysql")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(serviceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(serviceRoot, "data")); err != nil {
		t.Fatal(err)
	}
	if err := module.Install(context.Background(), "mysql"); err == nil {
		t.Fatal("service install followed a symlinked data directory")
	}
}

func TestConfirmedUninstallBacksUpBeforeDeletingServiceState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("WSL tar backup permissions and layout are verified on Linux")
	}
	root := t.TempDir()
	runner := &fakeRunner{}
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(root, "services", "mysql", "data", "db.bin")
	configFile := filepath.Join(root, "services", "mysql", "config", "my.cnf")
	for file, content := range map[string]string{dataFile: "data", configFile: "config"} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	response, err := module.Action(context.Background(), agentproto.ServiceRequest{
		ID: "mysql", Action: "uninstall", Config: map[string]string{"confirm_delete": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "mysql")); !os.IsNotExist(err) {
		t.Fatalf("service data remains after confirmed uninstall: %v", err)
	}
	if response.Message == "" {
		t.Fatal("confirmed uninstall did not return the WSL backup path")
	}
	backup, err := os.Open(response.Message)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	compressed, err := gzip.NewReader(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		seen[header.Name] = true
	}
	if !seen["data/db.bin"] || !seen["config/my.cnf"] {
		t.Fatalf("backup contents = %v", seen)
	}
}

func TestUnconfirmedUninstallPreservesServiceData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixed service Compose project executes in WSL")
	}
	root := t.TempDir()
	module, err := agentdocker.New(agentdocker.Config{Root: root, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(root, "services", "redis", "data", "dump.rdb")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Action(context.Background(), agentproto.ServiceRequest{ID: "redis", Action: "uninstall"}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dataFile); err != nil || string(data) != "keep" {
		t.Fatalf("unconfirmed uninstall data = %q, err %v", data, err)
	}
}
