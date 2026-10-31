//go:build !windows

package helper_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func testImage(name string, letter byte) string {
	return "fixture-" + name + "@sha256:" + strings.Repeat(string(letter), 64)
}

func testCatalog() services.Catalog {
	return services.Catalog{
		Services: []services.Service{
			{
				ID:              services.IDNginx,
				DisplayName:     "Nginx",
				Group:           services.GroupWeb,
				Image:           testImage("nginx", 'a'),
				NetworkMode:     "host",
				Ports:           []services.Port{{Host: 8080, Container: 80, Protocol: "http", Loopback: true}},
				Config:          []services.Mount{{HostSubdir: "config", ContainerPath: "/etc/nginx/conf.d", ReadOnly: true}},
				Healthcheck:     &services.Healthcheck{Test: []string{"CMD", "true"}},
				ValidateCommand: []string{"nginx", "-t"},
				ReloadCommand:   []string{"nginx", "-s", "reload"},
			},
			{
				ID:          services.IDApache,
				DisplayName: "Apache",
				Group:       services.GroupWeb,
				Image:       testImage("apache", 'b'),
				NetworkMode: "host",
				Ports:       []services.Port{{Host: 8080, Container: 80, Protocol: "http", Loopback: true}},
				Healthcheck: &services.Healthcheck{Test: []string{"CMD", "true"}},
			},
			{
				ID:          services.IDMySQL,
				DisplayName: "MySQL",
				Group:       services.GroupMySQLFamily,
				Image:       testImage("mysql", 'c'),
				Ports:       []services.Port{{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true}},
				Healthcheck: &services.Healthcheck{Test: []string{"CMD", "true"}},
			},
			{
				ID:          services.IDMariaDB,
				DisplayName: "MariaDB",
				Group:       services.GroupMySQLFamily,
				Image:       testImage("mariadb", 'd'),
				Ports:       []services.Port{{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true}},
				Healthcheck: &services.Healthcheck{Test: []string{"CMD", "true"}},
			},
			{
				ID:          services.IDPostgreSQL,
				DisplayName: "PostgreSQL",
				Group:       services.GroupStandalone,
				Image:       testImage("postgresql", 'e'),
				Ports:       []services.Port{{Host: 5432, Container: 5432, Protocol: "tcp", Loopback: true}},
			},
			{
				ID:          services.IDRedis,
				DisplayName: "Redis",
				Group:       services.GroupStandalone,
				Image:       testImage("redis", 'f'),
				Ports:       []services.Port{{Host: 6379, Container: 6379, Protocol: "tcp", Loopback: true}},
			},
			{
				ID:          services.ID("cache"),
				DisplayName: "Cache",
				Group:       services.GroupStandalone,
				Image:       testImage("cache", '1'),
			},
		},
	}
}

type fakeCommandRunner struct {
	mu     sync.Mutex
	calls  [][]string
	handle func(name string, args []string) ([]byte, error)
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.mu.Lock()
	f.calls = append(f.calls, call)
	handle := f.handle
	f.mu.Unlock()
	if handle != nil {
		return handle(name, args)
	}
	return []byte("ok"), nil
}

func (f *fakeCommandRunner) snapshot() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	for i := range f.calls {
		out[i] = append([]string(nil), f.calls[i]...)
	}
	return out
}

func (f *fakeCommandRunner) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeCommandRunner) callsWithArg(arg string) [][]string {
	var out [][]string
	for _, call := range f.snapshot() {
		for _, value := range call {
			if value == arg {
				out = append(out, call)
				break
			}
		}
	}
	return out
}

type simContainer struct {
	ID     string
	State  string
	Health string
}

type dockerSim struct {
	mu sync.Mutex

	catalog     services.Catalog
	containers  map[services.ID]simContainer
	images      map[string]bool
	portOwners  map[int]services.ID
	nativePorts map[int]bool

	psOverrides map[services.ID][]simContainer
	upHealth    map[services.ID]string
	psCalls     int

	psRaw  []byte
	psErr  error
	onStop func()

	inspectErr       error
	upErr            error
	restartErr       error
	stopErr          error
	stopAppliedOnErr bool
	rmErr            error
	pullErr          error
	execErr          error
}

func newDockerSim(cat services.Catalog) (*dockerSim, *fakeCommandRunner) {
	sim := &dockerSim{
		catalog:     cat,
		containers:  make(map[services.ID]simContainer),
		images:      make(map[string]bool),
		portOwners:  make(map[int]services.ID),
		nativePorts: make(map[int]bool),
		psOverrides: make(map[services.ID][]simContainer),
		upHealth:    make(map[services.ID]string),
	}
	runner := &fakeCommandRunner{handle: sim.run}
	return sim, runner
}

func (s *dockerSim) setContainer(id services.ID, state, health string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[id] = simContainer{ID: "cid-" + string(id), State: state, Health: health}
}

func (s *dockerSim) startContainer(id services.ID) {
	svc, ok := s.catalog.Get(id)
	if !ok {
		return
	}
	health := ""
	if svc.Healthcheck != nil {
		health = "healthy"
	}
	if override, ok := s.upHealth[id]; ok {
		health = override
	}
	s.containers[id] = simContainer{ID: "cid-" + string(id), State: "running", Health: health}
	for _, port := range svc.Ports {
		if port.Host > 0 {
			s.portOwners[port.Host] = id
		}
	}
}

func (s *dockerSim) removeContainer(id services.ID) {
	delete(s.containers, id)
	for port, owner := range s.portOwners {
		if owner == id {
			delete(s.portOwners, port)
		}
	}
}

func (s *dockerSim) setPort(port int, id services.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.portOwners[port] = id
}

func (s *dockerSim) setNativePort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nativePorts[port] = true
}

func (s *dockerSim) running(id services.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cont, ok := s.containers[id]
	return ok && strings.EqualFold(cont.State, "running")
}

func (s *dockerSim) probe(_ context.Context, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nativePorts[port] {
		return nil
	}
	if _, ok := s.portOwners[port]; ok {
		return nil
	}
	return fmt.Errorf("dial tcp 127.0.0.1:%d: connect: connection refused", port)
}

type psEntry struct {
	ID      string `json:"ID"`
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health,omitempty"`
}

func (s *dockerSim) psOutput() []byte {
	merged := make(map[services.ID]simContainer, len(s.containers))
	for id, cont := range s.containers {
		merged[id] = cont
	}
	ids := make([]services.ID, 0, len(s.psOverrides))
	for id := range s.psOverrides {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		seq := s.psOverrides[id]
		if len(seq) == 0 {
			continue
		}
		item := seq[0]
		s.psOverrides[id] = seq[1:]
		if item.State == "" {
			delete(merged, id)
			continue
		}
		merged[id] = item
	}

	entries := make([]psEntry, 0, len(merged))
	for id, cont := range merged {
		entries = append(entries, psEntry{
			ID:      cont.ID,
			Name:    "devstack-" + string(id),
			Service: string(id),
			State:   cont.State,
			Health:  cont.Health,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Service < entries[j].Service })
	data, _ := json.Marshal(entries)
	return data
}

func (s *dockerSim) run(name string, args []string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "docker" && len(args) >= 3 && args[0] == "image" && args[1] == "inspect" {
		if s.inspectErr != nil {
			return nil, s.inspectErr
		}
		image := args[2]
		if s.images[image] {
			return []byte("[]"), nil
		}
		return nil, fmt.Errorf("docker image inspect: Error response from daemon: No such image: %s", image)
	}

	if name != "docker" || len(args) < 6 || args[0] != "compose" {
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
	sub := args[5]
	last := ""
	if len(args) > 6 {
		last = args[len(args)-1]
	}
	switch sub {
	case "ps":
		s.psCalls++
		if s.psErr != nil {
			return nil, s.psErr
		}
		if len(s.psRaw) > 0 {
			return append([]byte(nil), s.psRaw...), nil
		}
		return s.psOutput(), nil
	case "pull":
		if s.pullErr != nil {
			return nil, s.pullErr
		}
	case "stop":
		if s.stopErr != nil {
			if s.stopAppliedOnErr {
				s.removeContainer(services.ID(last))
			}
			return nil, s.stopErr
		}
		s.removeContainer(services.ID(last))
		if s.onStop != nil {
			s.onStop()
		}
	case "up":
		if s.upErr != nil {
			return nil, s.upErr
		}
		s.startContainer(services.ID(last))
	case "restart":
		if s.restartErr != nil {
			return nil, s.restartErr
		}
		s.startContainer(services.ID(last))
	case "rm":
		if s.rmErr != nil {
			return nil, s.rmErr
		}
		s.removeContainer(services.ID(last))
	case "exec":
		if s.execErr != nil {
			return nil, s.execErr
		}
		return []byte("ok"), nil
	case "logs":
		return []byte("logs"), nil
	default:
		return nil, fmt.Errorf("unexpected compose subcommand %q", sub)
	}
	return []byte("ok"), nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now
	c.now = c.now.Add(time.Second)
	return now
}

func (c *fakeClock) Sleep(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

type controllerFixture struct {
	t       *testing.T
	catalog services.Catalog
	root    string
	ctrl    *helper.DockerServiceController
	runner  *fakeCommandRunner
	sim     *dockerSim
	clock   *fakeClock
}

func newControllerFixture(t *testing.T) *controllerFixture {
	t.Helper()
	cat := testCatalog()
	root := t.TempDir()
	sim, runner := newDockerSim(cat)
	ctrl := helper.NewDockerServiceController(root, cat, runner)
	clock := &fakeClock{now: time.Date(2026, 10, 25, 12, 34, 56, 0, time.UTC)}
	ctrl.Now = clock.Now
	ctrl.Sleep = clock.Sleep
	ctrl.Probe = sim.probe
	ctrl.WaitTimeout = 60 * time.Second
	return &controllerFixture{
		t:       t,
		catalog: cat,
		root:    root,
		ctrl:    ctrl,
		runner:  runner,
		sim:     sim,
		clock:   clock,
	}
}

func (f *controllerFixture) project() string {
	return "devstack-" + services.RootHash(f.root)
}

func (f *controllerFixture) composePath() string {
	return filepath.Join(f.root, "services", "compose.yaml")
}

func (f *controllerFixture) composeCall(rest ...string) []string {
	return append([]string{"docker", "compose", "-p", f.project(), "-f", f.composePath()}, rest...)
}

func (f *controllerFixture) writeCompose() {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.composePath()), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.composePath(), []byte("services: {}\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *controllerFixture) scaffold(id services.ID) {
	f.t.Helper()
	scaffoldServiceDirs(f.t, f.root, id)
}

func (f *controllerFixture) markInstalled(id services.ID) {
	f.t.Helper()
	f.scaffold(id)
}

func scaffoldServiceDirs(t *testing.T, root string, id services.ID) {
	t.Helper()
	for _, sub := range []string{"config", "data", "logs"} {
		if err := os.MkdirAll(filepath.Join(root, "services", string(id), sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *controllerFixture) image(id services.ID) string {
	svc, _ := f.catalog.Get(id)
	return svc.Image
}

func TestStatusMapsContainerAndImageStates(t *testing.T) {
	fx := newControllerFixture(t)
	fx.writeCompose()
	fx.scaffold(services.IDPostgreSQL)
	fx.sim.setContainer(services.IDNginx, "running", "healthy")
	fx.sim.setContainer(services.IDApache, "running", "unhealthy")
	fx.sim.setContainer(services.IDMySQL, "exited", "")
	fx.sim.setContainer(services.IDMariaDB, "created", "")
	fx.sim.setContainer(services.IDRedis, "running", "")
	fx.sim.images[fx.image(services.IDPostgreSQL)] = true

	statuses, err := fx.ctrl.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != len(fx.catalog.Services) {
		t.Fatalf("Status returned %d services, want %d", len(statuses), len(fx.catalog.Services))
	}

	byID := make(map[string]helperproto.ServiceStatus, len(statuses))
	for _, st := range statuses {
		byID[st.ID] = st
	}

	nginx := byID[string(services.IDNginx)]
	if nginx.State != string(services.StateActive) || !nginx.Active || !nginx.Installed {
		t.Errorf("nginx status = %+v, want active/installed", nginx)
	}
	if nginx.Health != "healthy" || nginx.ContainerID != "cid-nginx" {
		t.Errorf("nginx health/container = %q/%q", nginx.Health, nginx.ContainerID)
	}
	if nginx.ConfigPath != filepath.Join(fx.root, "services", "nginx", "config") {
		t.Errorf("nginx config path = %q", nginx.ConfigPath)
	}
	if nginx.DataPath != filepath.Join(fx.root, "services", "nginx", "data") {
		t.Errorf("nginx data path = %q", nginx.DataPath)
	}
	if len(nginx.Ports) != 1 || nginx.Ports[0].Host != 8080 {
		t.Errorf("nginx ports = %+v", nginx.Ports)
	}

	apache := byID[string(services.IDApache)]
	if apache.State != string(services.StateError) || apache.Active || !apache.Installed {
		t.Errorf("apache status = %+v, want error/installed", apache)
	}
	if apache.LastError == "" {
		t.Error("apache unhealthy status has no last error")
	}

	redis := byID[string(services.IDRedis)]
	if redis.State != string(services.StateActive) || !redis.Active || !redis.Installed {
		t.Errorf("redis (no healthcheck) status = %+v, want active", redis)
	}

	mysql := byID[string(services.IDMySQL)]
	if mysql.State != string(services.StateInstalledButStopped) || mysql.Active || !mysql.Installed {
		t.Errorf("mysql status = %+v, want installed_but_stopped", mysql)
	}

	mariadb := byID[string(services.IDMariaDB)]
	if mariadb.State != string(services.StateInstalledButStopped) {
		t.Errorf("mariadb status = %+v, want installed_but_stopped", mariadb)
	}

	postgres := byID[string(services.IDPostgreSQL)]
	if postgres.State != string(services.StateInstalled) || postgres.Active || !postgres.Installed {
		t.Errorf("postgres image-only status = %+v, want installed", postgres)
	}
	if postgres.ContainerID != "" {
		t.Errorf("postgres container ID = %q, want empty", postgres.ContainerID)
	}

	cache := byID["cache"]
	if cache.State != string(services.StateNotInstalled) || cache.Active || cache.Installed {
		t.Errorf("cache status = %+v, want not_installed", cache)
	}

	psCalls := fx.runner.callsWithArg("ps")
	if len(psCalls) != 1 {
		t.Fatalf("ps calls = %d, want 1", len(psCalls))
	}
	if want := fx.composeCall("ps", "-a", "--format", "json"); !reflect.DeepEqual(psCalls[0], want) {
		t.Errorf("ps argv = %v, want %v", psCalls[0], want)
	}

}

func TestStatusReportsHealthcheckPendingAsStarting(t *testing.T) {
	fx := newControllerFixture(t)
	fx.writeCompose()
	fx.sim.setContainer(services.IDNginx, "running", "")

	statuses, err := fx.ctrl.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, st := range statuses {
		if st.ID != string(services.IDNginx) {
			continue
		}
		if st.State != string(services.StateStarting) || st.Active {
			t.Fatalf("nginx status = %+v, want starting", st)
		}
		return
	}
	t.Fatal("nginx status not found")
}

func TestStatusReturnsRunnerErrors(t *testing.T) {
	t.Run("ps", func(t *testing.T) {
		fx := newControllerFixture(t)
		fx.writeCompose()
		fx.sim.psErr = errors.New("docker daemon unavailable")
		statuses, err := fx.ctrl.Status(context.Background())
		if err == nil || !strings.Contains(err.Error(), "docker daemon unavailable") {
			t.Fatalf("Status error = %v, want daemon error", err)
		}
		if statuses != nil {
			t.Fatalf("Status returned statuses with error: %+v", statuses)
		}
	})

	t.Run("malformed ps output", func(t *testing.T) {
		fx := newControllerFixture(t)
		fx.writeCompose()
		fx.sim.psRaw = []byte(`[{"Service":"nginx"`)
		statuses, err := fx.ctrl.Status(context.Background())
		if err == nil {
			t.Fatalf("Status succeeded with malformed ps output: %+v", statuses)
		}
		if statuses != nil {
			t.Fatalf("Status returned statuses with malformed ps output: %+v", statuses)
		}
	})

}

func TestStatusIgnoresCachedImageWithoutServiceScaffold(t *testing.T) {
	fx := newControllerFixture(t)
	fx.sim.images[fx.image(services.IDNginx)] = true

	statuses, err := fx.ctrl.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, st := range statuses {
		if st.ID != string(services.IDNginx) {
			continue
		}
		if st.Installed || st.State != string(services.StateNotInstalled) {
			t.Fatalf("nginx status = %+v, want not_installed despite cached image", st)
		}
		return
	}
	t.Fatal("nginx status not found")
}

func TestStatusDoesNotCreateComposeFileAsASideEffect(t *testing.T) {
	fx := newControllerFixture(t)
	if _, err := fx.ctrl.Status(context.Background()); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := os.Stat(fx.composePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Status created %s: %v", fx.composePath(), err)
	}
}

func TestActivateRejectsCachedImageWithoutServiceScaffold(t *testing.T) {
	fx := newControllerFixture(t)
	fx.sim.images[fx.image(services.IDNginx)] = true

	err := fx.ctrl.Activate(context.Background(), services.IDNginx)
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("Activate error = %v, want invalid-request not-installed error", err)
	}
	if calls := fx.runner.callsWithArg("up"); len(calls) != 0 {
		t.Fatalf("compose up calls = %v, want none", calls)
	}
}

func TestInstallPullFailureDoesNotMarkServiceInstalled(t *testing.T) {
	fx := newControllerFixture(t)
	fx.sim.pullErr = errors.New("registry unavailable")

	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err == nil {
		t.Fatal("Install succeeded, want pull failure")
	}
	for _, sub := range []string{"config", "data", "logs"} {
		if _, err := os.Stat(filepath.Join(fx.root, "services", "nginx", sub)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed install created %s: %v", sub, err)
		}
	}
}

func TestInstallCreatesDirectoriesPullsImageAndBacksUpCompose(t *testing.T) {
	fx := newControllerFixture(t)
	owner := os.Getuid()
	if owner == 0 {
		owner = 12345
	}
	fx.ctrl.OwnerUID = owner

	configFile := filepath.Join(fx.root, "services", "nginx", "config", "keep.conf")
	if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(fx.composePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	oldCompose := []byte("services: {}\n")
	if err := os.WriteFile(fx.composePath(), oldCompose, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, sub := range []string{"config", "data", "logs"} {
		dir := filepath.Join(fx.root, "services", "nginx", sub)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("install did not create %s: %v", dir, err)
		}
	}
	if data, err := os.ReadFile(configFile); err != nil || string(data) != "keep me" {
		t.Errorf("install did not preserve config file: %v %q", err, data)
	}

	for _, sub := range []string{"config", "logs"} {
		info, err := os.Stat(filepath.Join(fx.root, "services", "nginx", sub))
		if err != nil {
			t.Fatal(err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("stat info for %s is not syscall.Stat_t", sub)
		}
		if int(stat.Uid) != owner {
			t.Errorf("%s owner = %d, want %d", sub, stat.Uid, owner)
		}
	}

	backup, err := os.ReadFile(fx.composePath() + ".bak")
	if err != nil {
		t.Fatalf("compose backup missing: %v", err)
	}
	if string(backup) != string(oldCompose) {
		t.Errorf("compose backup = %q, want %q", backup, oldCompose)
	}
	rendered, err := os.ReadFile(fx.composePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), fx.image(services.IDNginx)) {
		t.Errorf("compose file does not contain nginx image: %s", rendered)
	}

	calls := fx.runner.snapshot()
	if len(calls) != 1 {
		t.Fatalf("install calls = %v, want only pull", calls)
	}
	if want := fx.composeCall("pull", "nginx"); !reflect.DeepEqual(calls[0], want) {
		t.Errorf("install argv = %v, want %v", calls[0], want)
	}
}

func TestActivateStopsCompetitorAndStartsTarget(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDNginx)
	fx.sim.setContainer(services.IDApache, "running", "healthy")
	fx.sim.setPort(8080, services.IDApache)

	if err := fx.ctrl.Activate(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "apache"),
		fx.composeCall("up", "-d", "--no-deps", "nginx"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("activation calls = %v, want %v", got, want)
	}
	if !fx.sim.running(services.IDNginx) {
		t.Error("nginx is not running after activation")
	}
	if fx.sim.running(services.IDApache) {
		t.Error("apache is still running after activation")
	}
}

func TestActivateRollsBackCompetitorOnHealthFailure(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDNginx)
	fx.sim.setContainer(services.IDApache, "running", "healthy")
	fx.sim.setPort(8080, services.IDApache)
	fx.sim.upHealth[services.IDNginx] = "unhealthy"

	err := fx.ctrl.Activate(context.Background(), services.IDNginx)
	if err == nil {
		t.Fatal("Activate succeeded, want health failure")
	}
	var activationErr *helper.ServiceActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error type = %T, want *helper.ServiceActivationError", err)
	}
	if activationErr.Phase != "health" {
		t.Errorf("activation phase = %q, want health", activationErr.Phase)
	}
	if activationErr.RollbackErr != nil {
		t.Errorf("rollback error = %v", activationErr.RollbackErr)
	}

	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "apache"),
		fx.composeCall("up", "-d", "--no-deps", "nginx"),
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "nginx"),
		fx.composeCall("restart", "apache"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rollback calls = %v, want %v", got, want)
	}
	if !fx.sim.running(services.IDApache) {
		t.Error("apache was not restarted during rollback")
	}
	if fx.sim.running(services.IDNginx) {
		t.Error("failed nginx container is still running")
	}
}

func TestActivateRestartsCompetitorWhenStopReportsFailure(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDNginx)
	fx.sim.setContainer(services.IDApache, "running", "healthy")
	fx.sim.setPort(8080, services.IDApache)
	fx.sim.stopErr = errors.New("stop reported failure")
	fx.sim.stopAppliedOnErr = true

	err := fx.ctrl.Activate(context.Background(), services.IDNginx)
	if err == nil {
		t.Fatal("Activate succeeded, want stop-competitor failure")
	}
	var activationErr *helper.ServiceActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error type = %T, want *helper.ServiceActivationError", err)
	}
	if activationErr.Phase != "stop-competitor" {
		t.Errorf("activation phase = %q, want stop-competitor", activationErr.Phase)
	}
	if activationErr.RollbackErr != nil {
		t.Errorf("rollback error = %v", activationErr.RollbackErr)
	}
	if !fx.sim.running(services.IDApache) {
		t.Error("competitor stopped by Docker was not restarted during rollback")
	}

	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "apache"),
		fx.composeCall("restart", "apache"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stop-failure rollback calls = %v, want %v", got, want)
	}
}

func TestActivateReportsFailedCompetitorRestore(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDNginx)
	fx.sim.setContainer(services.IDApache, "running", "healthy")
	fx.sim.setPort(8080, services.IDApache)
	fx.sim.upHealth[services.IDNginx] = "unhealthy"
	fx.sim.restartErr = errors.New("restart refused")

	err := fx.ctrl.Activate(context.Background(), services.IDNginx)
	if err == nil {
		t.Fatal("Activate succeeded, want health failure")
	}
	var activationErr *helper.ServiceActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error type = %T, want *helper.ServiceActivationError", err)
	}
	if activationErr.Phase != "health" {
		t.Errorf("activation phase = %q, want health", activationErr.Phase)
	}
	if activationErr.RollbackErr == nil || !strings.Contains(activationErr.RollbackErr.Error(), "restart apache") {
		t.Fatalf("rollback error = %v, want failed apache restart", activationErr.RollbackErr)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "rollback failed") {
		t.Errorf("error = %q, want rollback failure detail", err)
	}
	if fx.sim.running(services.IDApache) {
		t.Error("apache is running despite a failed restart")
	}
}

func TestActivateSwitchesMySQLToMariaDB(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDMariaDB)
	fx.sim.setContainer(services.IDMySQL, "running", "healthy")
	fx.sim.setPort(3306, services.IDMySQL)

	if err := fx.ctrl.Activate(context.Background(), services.IDMariaDB); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "mysql"),
		fx.composeCall("up", "-d", "--no-deps", "mariadb"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mysql-to-mariadb calls = %v, want %v", got, want)
	}
	if !fx.sim.running(services.IDMariaDB) {
		t.Error("mariadb is not running after switch")
	}
	if fx.sim.running(services.IDMySQL) {
		t.Error("mysql is still running after switch")
	}
}

func TestActivateMySQLToMariaDBRollsBackOnHealthFailure(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDMariaDB)
	fx.sim.setContainer(services.IDMySQL, "running", "healthy")
	fx.sim.setPort(3306, services.IDMySQL)
	fx.sim.upHealth[services.IDMariaDB] = "unhealthy"

	err := fx.ctrl.Activate(context.Background(), services.IDMariaDB)
	if err == nil {
		t.Fatal("Activate succeeded, want health failure")
	}
	var activationErr *helper.ServiceActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error type = %T, want *helper.ServiceActivationError", err)
	}
	if activationErr.Phase != "health" {
		t.Errorf("activation phase = %q, want health", activationErr.Phase)
	}
	if activationErr.RollbackErr != nil {
		t.Errorf("rollback error = %v", activationErr.RollbackErr)
	}

	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "mysql"),
		fx.composeCall("up", "-d", "--no-deps", "mariadb"),
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "mariadb"),
		fx.composeCall("restart", "mysql"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mysql/mariadb rollback calls = %v, want %v", got, want)
	}
	if !fx.sim.running(services.IDMySQL) {
		t.Error("mysql was not restarted during rollback")
	}
	if fx.sim.running(services.IDMariaDB) {
		t.Error("failed mariadb container is still running")
	}
}

func TestActivatePortConflictDoesNotStopNativeProcess(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDNginx)
	fx.ctrl.WaitTimeout = time.Minute
	fx.sim.setContainer(services.IDApache, "running", "healthy")
	fx.sim.setPort(8080, services.IDApache)
	fx.sim.setNativePort(8080)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	err := fx.ctrl.Activate(ctx, services.IDNginx)
	if err == nil {
		t.Fatal("Activate succeeded, want port conflict")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Activate waited %s for an occupied port; want a fast failure", elapsed)
	}
	var activationErr *helper.ServiceActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error type = %T, want *helper.ServiceActivationError", err)
	}
	if activationErr.Phase != "port-check" || activationErr.Port != 8080 {
		t.Errorf("activation error = phase %q port %d, want port-check/8080", activationErr.Phase, activationErr.Port)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "in use") {
		t.Errorf("error %q does not explain the port conflict", err)
	}

	calls := fx.runner.snapshot()
	for _, call := range calls {
		if len(call) > 6 && call[6] == "up" {
			t.Fatalf("activation started target despite port conflict: %v", calls)
		}
		if len(call) > 7 && call[6] == "stop" && call[len(call)-1] == "nginx" {
			t.Fatalf("activation stopped target despite never starting it: %v", calls)
		}
	}
	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "apache"),
		fx.composeCall("restart", "apache"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("conflict calls = %v, want %v", calls, want)
	}
	if !fx.sim.running(services.IDApache) {
		t.Error("competitor was not restored after port conflict")
	}
}

func TestActivateRollsBackWhenStartFails(t *testing.T) {
	fx := newControllerFixture(t)
	fx.markInstalled(services.IDNginx)
	fx.sim.setContainer(services.IDApache, "running", "healthy")
	fx.sim.setPort(8080, services.IDApache)
	fx.sim.upErr = errors.New("image pull failed")

	err := fx.ctrl.Activate(context.Background(), services.IDNginx)
	if err == nil {
		t.Fatal("Activate succeeded, want start failure")
	}
	var activationErr *helper.ServiceActivationError
	if !errors.As(err, &activationErr) {
		t.Fatalf("error type = %T, want *helper.ServiceActivationError", err)
	}
	if activationErr.Phase != "start" {
		t.Errorf("activation phase = %q, want start", activationErr.Phase)
	}
	if !fx.sim.running(services.IDApache) {
		t.Error("competitor was not restarted after start failure")
	}
	want := [][]string{
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("stop", "apache"),
		fx.composeCall("up", "-d", "--no-deps", "nginx"),
		fx.composeCall("stop", "nginx"),
		fx.composeCall("restart", "apache"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("start-failure rollback calls = %v, want %v", got, want)
	}
}

func TestRestartWaitsForHealth(t *testing.T) {
	fx := newControllerFixture(t)
	fx.ctrl.WaitTimeout = 10 * time.Second
	fx.sim.setContainer(services.IDNginx, "running", "healthy")
	fx.sim.psOverrides[services.IDNginx] = []simContainer{
		{ID: "cid-nginx", State: "running", Health: "starting"},
		{ID: "cid-nginx", State: "running", Health: "healthy"},
	}

	if err := fx.ctrl.Restart(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	want := [][]string{
		fx.composeCall("restart", "nginx"),
		fx.composeCall("ps", "-a", "--format", "json"),
		fx.composeCall("ps", "-a", "--format", "json"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restart calls = %v, want %v", got, want)
	}
}

func TestRestartReportsHealthTimeout(t *testing.T) {
	fx := newControllerFixture(t)
	fx.ctrl.WaitTimeout = time.Nanosecond
	fx.sim.setContainer(services.IDNginx, "running", "healthy")
	fx.sim.psOverrides[services.IDNginx] = []simContainer{
		{ID: "cid-nginx", State: "running", Health: "starting"},
	}

	err := fx.ctrl.Restart(context.Background(), services.IDNginx)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Restart error = %v, want timeout", err)
	}
}

func TestValidateAndReloadUseCatalogCommands(t *testing.T) {
	fx := newControllerFixture(t)
	ctx := context.Background()

	if err := fx.ctrl.Validate(ctx, services.IDNginx); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := fx.ctrl.Reload(ctx, services.IDNginx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	want := [][]string{
		fx.composeCall("exec", "-T", "nginx", "nginx", "-t"),
		fx.composeCall("exec", "-T", "nginx", "nginx", "-s", "reload"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("validate/reload calls = %v, want %v", got, want)
	}

	if err := fx.ctrl.Validate(ctx, services.IDPostgreSQL); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Validate without command error = %v, want ErrInvalidRequest", err)
	}
	if err := fx.ctrl.Reload(ctx, services.IDPostgreSQL); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Reload without command error = %v, want ErrInvalidRequest", err)
	}
	if got := fx.runner.snapshot(); len(got) != 2 {
		t.Errorf("commands executed for services without catalog commands: %v", got)
	}

	if err := fx.ctrl.Validate(ctx, "does-not-exist"); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Validate unknown service error = %v, want ErrInvalidRequest", err)
	}
	if err := fx.ctrl.Reload(ctx, "does-not-exist"); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Reload unknown service error = %v, want ErrInvalidRequest", err)
	}
	if err := fx.ctrl.Restart(ctx, "does-not-exist"); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Errorf("Restart unknown service error = %v, want ErrInvalidRequest", err)
	}
}

func TestUninstallDeleteDataBacksUpBeforeRemoving(t *testing.T) {
	fx := newControllerFixture(t)
	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fx.runner.reset()
	fx.sim.setContainer(services.IDNginx, "running", "healthy")

	files := map[string]string{
		"services/nginx/config/nginx.conf": "server {}",
		"services/nginx/data/db.bin":       "database",
		"services/nginx/logs/access.log":   "log line",
		"secrets/nginx/password":           "secret",
	}
	for rel, content := range files {
		path := filepath.Join(fx.root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := fx.ctrl.Uninstall(context.Background(), services.IDNginx, true); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	archivePath := filepath.Join(fx.root, "backups", "services", "nginx", "20261025T123456.000000000Z.tar.gz")
	if info, err := os.Stat(archivePath); err != nil || info.Size() == 0 {
		t.Fatalf("backup archive missing or empty: %v", err)
	}
	entries := readTarGzEntries(t, archivePath)
	for _, want := range []string{
		"services/nginx/config/nginx.conf",
		"services/nginx/data/db.bin",
		"services/nginx/logs/access.log",
		"secrets/nginx/password",
	} {
		if !entries[want] {
			t.Errorf("backup archive missing %q; entries = %v", want, entries)
		}
	}

	if _, err := os.Stat(filepath.Join(fx.root, "services", "nginx")); !os.IsNotExist(err) {
		t.Errorf("service tree still exists after delete-data uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "secrets", "nginx")); !os.IsNotExist(err) {
		t.Errorf("secret tree still exists after delete-data uninstall: %v", err)
	}

	want := [][]string{
		fx.composeCall("stop", "nginx"),
		fx.composeCall("rm", "-f", "nginx"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("uninstall calls = %v, want %v", got, want)
	}
}

func TestUninstallKeepsDataByDefault(t *testing.T) {
	fx := newControllerFixture(t)
	dataFile := filepath.Join(fx.root, "services", "nginx", "data", "db.bin")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fx.ctrl.Uninstall(context.Background(), services.IDNginx, false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(dataFile); err != nil {
		t.Errorf("data file was removed without delete-data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "backups")); !os.IsNotExist(err) {
		t.Errorf("backup directory created without delete-data: %v", err)
	}
}

func TestUninstallDeleteDataAbortsWhenBackupFails(t *testing.T) {
	fx := newControllerFixture(t)
	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fx.runner.reset()

	dataFile := filepath.Join(fx.root, "services", "nginx", "data", "db.bin")
	if err := os.WriteFile(dataFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(fx.root, "secrets", "nginx", "password")
	if err := os.MkdirAll(filepath.Dir(secretFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupsPath := filepath.Join(fx.root, "backups")
	if err := os.WriteFile(backupsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := fx.ctrl.Uninstall(context.Background(), services.IDNginx, true)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "backup") {
		t.Fatalf("Uninstall error = %v, want backup failure", err)
	}
	if _, err := os.Stat(dataFile); err != nil {
		t.Errorf("data file was deleted after backup failure: %v", err)
	}
	if _, err := os.Stat(secretFile); err != nil {
		t.Errorf("secret file was deleted after backup failure: %v", err)
	}
	for _, call := range fx.runner.snapshot() {
		if len(call) > 6 && call[6] == "rm" {
			t.Errorf("container was removed after backup failure: %v", call)
		}
	}
}

func TestUninstallPropagatesStopFailureAndKeepsData(t *testing.T) {
	fx := newControllerFixture(t)
	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fx.runner.reset()

	dataFile := filepath.Join(fx.root, "services", "nginx", "data", "db.bin")
	if err := os.WriteFile(dataFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.sim.stopErr = errors.New("permission denied")

	err := fx.ctrl.Uninstall(context.Background(), services.IDNginx, true)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Uninstall error = %v, want stop failure", err)
	}
	if _, err := os.Stat(dataFile); err != nil {
		t.Errorf("data file was removed after stop failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "backups")); !os.IsNotExist(err) {
		t.Errorf("backup directory exists after stop failure: %v", err)
	}
	want := [][]string{fx.composeCall("stop", "nginx")}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stop-failure calls = %v, want %v", got, want)
	}
}

func TestUninstallToleratesAbsentContainerOnStop(t *testing.T) {
	fx := newControllerFixture(t)
	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fx.runner.reset()
	fx.sim.stopErr = errors.New("No such container: devstack-nginx")

	if err := fx.ctrl.Uninstall(context.Background(), services.IDNginx, false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	want := [][]string{
		fx.composeCall("stop", "nginx"),
		fx.composeCall("rm", "-f", "nginx"),
	}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("absent-container calls = %v, want %v", got, want)
	}
}

func TestUninstallAbortsBeforeMutationOnCanceledContext(t *testing.T) {
	fx := newControllerFixture(t)
	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fx.runner.reset()

	dataFile := filepath.Join(fx.root, "services", "nginx", "data", "db.bin")
	if err := os.WriteFile(dataFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(fx.root, "secrets", "nginx", "password")
	if err := os.MkdirAll(filepath.Dir(secretFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := fx.ctrl.Uninstall(ctx, services.IDNginx, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Uninstall error = %v, want context.Canceled", err)
	}
	if calls := fx.runner.snapshot(); len(calls) != 0 {
		t.Errorf("commands executed with canceled context: %v", calls)
	}
	if _, err := os.Stat(dataFile); err != nil {
		t.Errorf("data file was removed with canceled context: %v", err)
	}
	if _, err := os.Stat(secretFile); err != nil {
		t.Errorf("secret file was removed with canceled context: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "backups")); !os.IsNotExist(err) {
		t.Errorf("backup directory exists with canceled context: %v", err)
	}
}

func TestUninstallStopsBeforeBackupWhenContextCanceledDuringStop(t *testing.T) {
	fx := newControllerFixture(t)
	if err := fx.ctrl.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fx.runner.reset()

	dataFile := filepath.Join(fx.root, "services", "nginx", "data", "db.bin")
	if err := os.WriteFile(dataFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	fx.sim.onStop = cancel
	err := fx.ctrl.Uninstall(ctx, services.IDNginx, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Uninstall error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(dataFile); err != nil {
		t.Errorf("data file was removed after cancellation during stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "backups")); !os.IsNotExist(err) {
		t.Errorf("backup directory exists after cancellation during stop: %v", err)
	}
	want := [][]string{fx.composeCall("stop", "nginx")}
	if got := fx.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("canceled-stop calls = %v, want %v", got, want)
	}
}

func readTarGzEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	entries := make(map[string]bool)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		entries[name] = true
	}
	return entries
}
