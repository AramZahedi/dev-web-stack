package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

type fakeDockerRunner struct {
	output map[string]string
	errors map[string]error
	calls  []string
}

func (f *fakeDockerRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, key)
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	return []byte(f.output[key]), nil
}

func readyDockerRunner(root string) *fakeDockerRunner {
	return &fakeDockerRunner{
		output: map[string]string{
			"systemctl is-active --quiet docker.service": "active\n",
			"docker info --format {{.DockerRootDir}}":    filepath.Join(root, "docker") + "\n",
			"docker compose version --short":             "v2.32.4\n",
		},
		errors: make(map[string]error),
	}
}

func TestDockerEngineHealthChecksSystemdDataRootAndCompose(t *testing.T) {
	root := t.TempDir()
	runner := readyDockerRunner(root)
	engine, err := NewDockerEngine(root, runner)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := engine.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.SystemdReady || !readiness.EngineReady || !readiness.ComposeReady || readiness.DataRoot != filepath.Join(root, "docker") {
		t.Fatalf("readiness = %+v", readiness)
	}
}

func TestDockerEngineMapsDataRootComposeAndSystemdFailures(t *testing.T) {
	root := t.TempDir()
	engine, err := NewDockerEngine(root, readyDockerRunner(root))
	if err != nil {
		t.Fatal(err)
	}
	runner := engine.runner.(*fakeDockerRunner)
	runner.output["docker info --format {{.DockerRootDir}}"] = "/var/lib/docker\n"
	if _, err := engine.Health(context.Background()); !errors.Is(err, ErrDockerDataRootMismatch) {
		t.Fatalf("data root error = %v", err)
	}
	runner.output["docker info --format {{.DockerRootDir}}"] = filepath.Join(root, "docker")
	runner.output["docker compose version --short"] = ""
	if _, err := engine.Health(context.Background()); !errors.Is(err, ErrDockerComposeUnavailable) {
		t.Fatalf("compose error = %v", err)
	}
	runner.errors["systemctl is-active --quiet docker.service"] = errors.New("inactive")
	if _, err := engine.Health(context.Background()); !errors.Is(err, ErrSystemdNotReady) {
		t.Fatalf("systemd error = %v", err)
	}
}

func TestDockerEngineBootstrapMapsStartupFailureAndUsesFixedScript(t *testing.T) {
	root := t.TempDir()
	runner := readyDockerRunner(root)
	engine, err := NewDockerEngine(root, runner)
	if err != nil {
		t.Fatal(err)
	}
	runner.errors["/usr/bin/env bash /data/devstack/bin/install-docker.sh"] = errors.New("script failed")
	if err := engine.Bootstrap(context.Background()); !errors.Is(err, ErrDockerBootstrapFailed) {
		t.Fatalf("Bootstrap error = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "/usr/bin/env bash /data/devstack/bin/install-docker.sh" {
		t.Fatalf("bootstrap commands = %#v", runner.calls)
	}
}

func TestDockerAgentHealthReadinessIncludesDocker(t *testing.T) {
	root := t.TempDir()
	identity, err := GenerateTLSIdentity(filepath.Join(root, "tls"))
	if err != nil {
		t.Fatal(err)
	}
	runner := readyDockerRunner(root)
	engine, err := NewDockerEngine(root, runner)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithRuntimeAndDocker(Config{Root: root, Distro: "devstack"}, "docker-secret", identity, nil, nil, engine)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, agentproto.PathHealth, nil)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"docker-secret")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "docker") {
		t.Fatalf("healthy Docker health = %d %s", recorder.Code, recorder.Body.String())
	}
	runner.errors["systemctl is-active --quiet docker.service"] = errors.New("inactive")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready Docker health status = %d", recorder.Code)
	}
}

func TestDockerDaemonConfigPinsDataRootWithoutTCPListener(t *testing.T) {
	path := filepath.Join("..", "..", "build", "wsl", "docker-daemon.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["data-root"] != "/data/devstack/docker" {
		t.Fatalf("Docker data-root = %v", config["data-root"])
	}
	if hosts, ok := config["hosts"]; ok {
		for _, host := range hosts.([]any) {
			if strings.HasPrefix(host.(string), "tcp://") {
				t.Fatalf("Docker TCP API listener is configured: %v", host)
			}
		}
	}
}
