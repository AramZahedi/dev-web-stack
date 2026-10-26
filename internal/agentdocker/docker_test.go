package agentdocker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	compose, err := services.RenderCompose(services.DefaultCatalog(), root)
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	if strings.Contains(text, "C:\\") || strings.Contains(text, "\\wsl.localhost") {
		t.Fatalf("Compose embeds a Windows/UNC bind mount:\n%s", text)
	}
	for _, service := range services.DefaultCatalog().Services {
		if !strings.Contains(service.Image, "@sha256:") {
			t.Errorf("service %q image is not digest pinned: %q", service.ID, service.Image)
		}
	}
	if len(runner.calls) != 1 || !containsSequence(runner.calls[0], []string{"compose", "-p", "devstack", "pull", "mysql"}) {
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
