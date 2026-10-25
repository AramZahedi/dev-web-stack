package helper_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestServiceHelperEndpoints(t *testing.T) {
	ctx := context.Background()
	allowUID := syscall.Getuid()

	var recordedInstall, recordedActivate, recordedDeactivate string
	var recordedUninstallID string
	var recordedDeleteData bool
	var recordedLogsID string
	var recordedLogsTail int
	var recordedRestart, recordedValidate, recordedReload string

	fakeStatus := []helperproto.ServiceStatus{
		{
			ID:          "nginx",
			State:       "active",
			Active:      true,
			Image:       "fixture-nginx:latest",
			ContainerID: "cid-123456",
			Health:      "healthy",
			Ports: []helperproto.ServicePortStatus{
				{Host: 8080, Container: 80, Protocol: "http", Loopback: true},
			},
		},
	}

	cfg := helper.Config{
		AllowUID: allowUID,
		Service: helper.Service{
			ServiceStatus: func(ctx context.Context) ([]helperproto.ServiceStatus, error) {
				return fakeStatus, nil
			},
			ServiceInstall: func(ctx context.Context, id string) error {
				recordedInstall = id
				return nil
			},
			ServiceActivate: func(ctx context.Context, id string) error {
				recordedActivate = id
				return nil
			},
			ServiceDeactivate: func(ctx context.Context, id string) error {
				recordedDeactivate = id
				return nil
			},
			ServiceUninstall: func(ctx context.Context, id string, deleteData bool) error {
				recordedUninstallID = id
				recordedDeleteData = deleteData
				return nil
			},
			ServiceLogs: func(ctx context.Context, id string, tail int) (string, error) {
				recordedLogsID = id
				recordedLogsTail = tail
				return "fake-logs-output", nil
			},
			ServiceRestart: func(ctx context.Context, id string) error {
				recordedRestart = id
				return nil
			},
			ServiceValidate: func(ctx context.Context, id string) error {
				recordedValidate = id
				return nil
			},
			ServiceReload: func(ctx context.Context, id string) error {
				recordedReload = id
				return nil
			},
		},
	}

	socketPath, _ := startTestServer(t, cfg)
	client := dialClient(t, socketPath)

	// 1. Status
	st, err := client.ServiceStatus(ctx)
	if err != nil {
		t.Fatalf("ServiceStatus: %v", err)
	}
	if !reflect.DeepEqual(st, fakeStatus) {
		t.Errorf("ServiceStatus = %+v, want %+v", st, fakeStatus)
	}

	// 2. Install
	if err := client.ServiceInstall(ctx, "nginx"); err != nil {
		t.Fatalf("ServiceInstall: %v", err)
	}
	if recordedInstall != "nginx" {
		t.Errorf("recordedInstall = %q, want %q", recordedInstall, "nginx")
	}

	// 3. Activate
	if err := client.ServiceActivate(ctx, "nginx"); err != nil {
		t.Fatalf("ServiceActivate: %v", err)
	}
	if recordedActivate != "nginx" {
		t.Errorf("recordedActivate = %q, want %q", recordedActivate, "nginx")
	}

	// 4. Deactivate
	if err := client.ServiceDeactivate(ctx, "nginx"); err != nil {
		t.Fatalf("ServiceDeactivate: %v", err)
	}
	if recordedDeactivate != "nginx" {
		t.Errorf("recordedDeactivate = %q, want %q", recordedDeactivate, "nginx")
	}

	// 5. Uninstall
	if err := client.ServiceUninstall(ctx, "nginx", true); err != nil {
		t.Fatalf("ServiceUninstall: %v", err)
	}
	if recordedUninstallID != "nginx" || !recordedDeleteData {
		t.Errorf("recordedUninstall = (%q, %v), want (%q, true)", recordedUninstallID, recordedDeleteData, "nginx")
	}

	// 6. Logs
	logs, err := client.ServiceLogs(ctx, "nginx", 50)
	if err != nil {
		t.Fatalf("ServiceLogs: %v", err)
	}
	if logs != "fake-logs-output" {
		t.Errorf("logs = %q, want %q", logs, "fake-logs-output")
	}
	if recordedLogsID != "nginx" || recordedLogsTail != 50 {
		t.Errorf("recordedLogs = (%q, %d), want (%q, 50)", recordedLogsID, recordedLogsTail, "nginx")
	}

	// 7. Restart, validate, reload
	if err := client.ServiceRestart(ctx, "nginx"); err != nil {
		t.Fatalf("ServiceRestart: %v", err)
	}
	if recordedRestart != "nginx" {
		t.Errorf("recordedRestart = %q, want %q", recordedRestart, "nginx")
	}
	if err := client.ServiceValidate(ctx, "nginx"); err != nil {
		t.Fatalf("ServiceValidate: %v", err)
	}
	if recordedValidate != "nginx" {
		t.Errorf("recordedValidate = %q, want %q", recordedValidate, "nginx")
	}
	if err := client.ServiceReload(ctx, "nginx"); err != nil {
		t.Fatalf("ServiceReload: %v", err)
	}
	if recordedReload != "nginx" {
		t.Errorf("recordedReload = %q, want %q", recordedReload, "nginx")
	}

	// 8. Invalid IDs rejection
	invalidIDs := []string{"", "../escaped", "nginx/extra", "invalid service!@#"}
	for _, badID := range invalidIDs {
		if err := client.ServiceInstall(ctx, badID); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceInstall(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
		if err := client.ServiceActivate(ctx, badID); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceActivate(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
		if err := client.ServiceDeactivate(ctx, badID); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceDeactivate(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
		if err := client.ServiceUninstall(ctx, badID, false); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceUninstall(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
		if err := client.ServiceRestart(ctx, badID); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceRestart(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
		if err := client.ServiceValidate(ctx, badID); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceValidate(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
		if err := client.ServiceReload(ctx, badID); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Errorf("ServiceReload(%q) error = %v, want ErrInvalidRequest", badID, err)
		}
	}
}

type fakeDockerRunner struct {
	mu        sync.Mutex
	calls     [][]string
	psOutput  string
	logsOut   string
	upSeen    bool
	psVisible bool
}

func (f *fakeDockerRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := append([]string{name}, args...)
	f.calls = append(f.calls, cmd)

	for _, arg := range args {
		if arg == "ps" {
			if !f.psVisible {
				return []byte("[]"), nil
			}
			return []byte(f.psOutput), nil
		}
		if arg == "logs" {
			return []byte(f.logsOut), nil
		}
		if arg == "up" {
			f.upSeen = true
			f.psVisible = true
		}
	}
	return []byte("ok"), nil
}

func (f *fakeDockerRunner) probe(ctx context.Context, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upSeen {
		return nil
	}
	return errors.New("port is not listening")
}

func TestServiceDockerController(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	cat := services.Catalog{
		Services: []services.Service{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx",
				Group:       services.GroupWeb,
				Image:       "fixture-nginx@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				NetworkMode: "host",
				Ports: []services.Port{
					{Host: 8080, Container: 80, Protocol: "http", Loopback: true},
				},
				Config: []services.Mount{
					{HostSubdir: "config", ContainerPath: "/etc/nginx/conf.d", ReadOnly: true},
				},
			},
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL",
				Group:       services.GroupMySQLFamily,
				Image:       "fixture-mysql@sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Ports: []services.Port{
					{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true},
				},
			},
		},
	}

	runner := &fakeDockerRunner{
		psOutput: `[{"ID":"c123456","Name":"devstack-nginx","Service":"nginx","State":"running","Health":"healthy"}]`,
		logsOut:  "nginx logs stream here",
	}

	controller := helper.NewDockerServiceController(root, cat, runner)
	controller.Probe = runner.probe
	project := "devstack-" + services.RootHash(root)
	composePath := filepath.Join(root, "services", "compose.yaml")

	// 1. Install
	if err := controller.Install(ctx, services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Verify compose.yaml was written
	if _, err := os.Stat(composePath); err != nil {
		t.Fatalf("compose.yaml was not created: %v", err)
	}
	wantInstallCmd := []string{"docker", "compose", "-p", project, "-f", composePath, "pull", "nginx"}
	runner.mu.Lock()
	lastCmd := runner.calls[len(runner.calls)-1]
	runner.mu.Unlock()
	if !reflect.DeepEqual(lastCmd, wantInstallCmd) {
		t.Errorf("Install command = %v, want %v", lastCmd, wantInstallCmd)
	}

	// 2. Activate
	if err := controller.Activate(ctx, services.IDNginx); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	wantActivateCmd := []string{"docker", "compose", "-p", project, "-f", composePath, "up", "-d", "--no-deps", "nginx"}
	runner.mu.Lock()
	var activateCmd []string
	for _, call := range runner.calls {
		if len(call) > 6 && call[6] == "up" {
			activateCmd = call
			break
		}
	}
	runner.mu.Unlock()
	if !reflect.DeepEqual(activateCmd, wantActivateCmd) {
		t.Errorf("Activate command = %v, want %v", activateCmd, wantActivateCmd)
	}

	// 3. Deactivate
	if err := controller.Deactivate(ctx, services.IDNginx); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	wantDeactivateCmd := []string{"docker", "compose", "-p", project, "-f", composePath, "stop", "nginx"}
	runner.mu.Lock()
	lastCmd = runner.calls[len(runner.calls)-1]
	runner.mu.Unlock()
	if !reflect.DeepEqual(lastCmd, wantDeactivateCmd) {
		t.Errorf("Deactivate command = %v, want %v", lastCmd, wantDeactivateCmd)
	}

	// 4. Uninstall with deleteData = true
	dataDir := filepath.Join(root, "services", "nginx", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "data.db"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := controller.Uninstall(ctx, services.IDNginx, true); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	wantUninstallCmd := []string{"docker", "compose", "-p", project, "-f", composePath, "rm", "-f", "nginx"}
	runner.mu.Lock()
	lastCmd = runner.calls[len(runner.calls)-1]
	runner.mu.Unlock()
	if !reflect.DeepEqual(lastCmd, wantUninstallCmd) {
		t.Errorf("Uninstall command = %v, want %v", lastCmd, wantUninstallCmd)
	}
	// Check dataDir was removed
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("data dir %s should have been deleted, but still exists", dataDir)
	}

	// 5. Status
	statuses, err := controller.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("Status returned %d entries, want 2 (catalog size)", len(statuses))
	}
	var nginxStatus *helperproto.ServiceStatus
	for i := range statuses {
		if statuses[i].ID == "nginx" {
			nginxStatus = &statuses[i]
			break
		}
	}
	if nginxStatus == nil {
		t.Fatalf("nginx status not found")
	}
	if nginxStatus.State != string(services.StateActive) || !nginxStatus.Active || !nginxStatus.Installed || nginxStatus.ContainerID != "c123456" {
		t.Errorf("unexpected nginx status: %+v", nginxStatus)
	}

	// 6. Logs
	logs, err := controller.Logs(ctx, services.IDNginx, 25)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if logs != "nginx logs stream here" {
		t.Errorf("logs = %q, want %q", logs, "nginx logs stream here")
	}
	wantLogsCmd := []string{"docker", "compose", "-p", project, "-f", composePath, "logs", "--tail", "25", "nginx"}
	runner.mu.Lock()
	lastCmd = runner.calls[len(runner.calls)-1]
	runner.mu.Unlock()
	if !reflect.DeepEqual(lastCmd, wantLogsCmd) {
		t.Errorf("Logs command = %v, want %v", lastCmd, wantLogsCmd)
	}

	// 7. Unknown ID rejection
	runner.mu.Lock()
	callCountBefore := len(runner.calls)
	runner.mu.Unlock()

	err = controller.Install(ctx, "unknown-service")
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Install unknown service err = %v, want ErrInvalidRequest", err)
	}
	err = controller.Activate(ctx, "unknown-service")
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Activate unknown service err = %v, want ErrInvalidRequest", err)
	}

	runner.mu.Lock()
	callCountAfter := len(runner.calls)
	runner.mu.Unlock()
	if callCountAfter != callCountBefore {
		t.Errorf("commands were executed for unknown service: before=%d, after=%d", callCountBefore, callCountAfter)
	}
}
