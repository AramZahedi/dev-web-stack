package servicecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

var (
	testNow     = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	nginxImage  = "nginx@sha256:" + strings.Repeat("a", 64)
	mysqlImage  = "mysql@sha256:" + strings.Repeat("b", 64)
	redisImage  = "redis@sha256:" + strings.Repeat("c", 64)
	errTestBoom = errors.New("boom")
)

func testCatalog() services.Catalog {
	return services.Catalog{Services: []services.Service{
		{
			ID:          services.IDNginx,
			DisplayName: "Nginx Web Server",
			Group:       services.GroupWeb,
			Image:       nginxImage,
			NetworkMode: "host",
			Ports:       []services.Port{{Host: 80, Container: 80, Protocol: "http"}},
		},
		{
			ID:          services.IDMySQL,
			DisplayName: "MySQL Database",
			Group:       services.GroupMySQLFamily,
			Image:       mysqlImage,
			Ports:       []services.Port{{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true}},
		},
		{
			ID:          services.IDRedis,
			DisplayName: "Redis In-Memory Store",
			Group:       services.GroupStandalone,
			Image:       redisImage,
			Ports:       []services.Port{{Host: 6379, Container: 6379, Protocol: "tcp", Loopback: true}},
		},
	}}
}

type fakeStore struct {
	records   map[services.ID]services.Record
	saved     []services.Record
	deleted   []services.ID
	listErr   error
	getErr    error
	saveErr   error
	deleteErr error
}

func newFakeStore(records ...services.Record) *fakeStore {
	store := &fakeStore{records: map[services.ID]services.Record{}}
	for _, rec := range records {
		store.records[rec.ID] = rec
	}
	return store
}

func (s *fakeStore) GetServiceState(_ context.Context, id services.ID) (services.Record, error) {
	if s.getErr != nil {
		return services.Record{}, s.getErr
	}
	rec, ok := s.records[id]
	if !ok {
		return services.Record{}, services.ErrNotFound
	}
	return rec, nil
}

func (s *fakeStore) ListServiceStates(_ context.Context) ([]services.Record, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]services.Record, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	return out, nil
}

func (s *fakeStore) SaveServiceState(_ context.Context, rec services.Record) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.records[rec.ID] = rec
	s.saved = append(s.saved, rec)
	return nil
}

func (s *fakeStore) DeleteServiceState(_ context.Context, id services.ID) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.records[id]; !ok {
		return services.ErrNotFound
	}
	delete(s.records, id)
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *fakeStore) last(t *testing.T) services.Record {
	t.Helper()
	if len(s.saved) == 0 {
		t.Fatal("no service state was saved")
	}
	return s.saved[len(s.saved)-1]
}

type fakeHelper struct {
	statuses []helperproto.ServiceStatus
	statusAt int

	statusErr     error
	installErr    error
	activateErr   error
	deactivateErr error
	restartErr    error
	uninstallErr  error
	logsErr       error

	logs        string
	tail        int
	deleteData  bool
	uninstallID string
	calls       []string
	trace       *[]string
}

func (h *fakeHelper) record(call string) {
	h.calls = append(h.calls, call)
	if h.trace != nil {
		*h.trace = append(*h.trace, call)
	}
}

func (h *fakeHelper) Status(context.Context) ([]helperproto.ServiceStatus, error) {
	h.statusAt++
	if h.statusErr != nil {
		return nil, h.statusErr
	}
	return h.statuses, nil
}

func (h *fakeHelper) Install(_ context.Context, id string) error {
	h.record("helper_install:" + id)
	return h.installErr
}

func (h *fakeHelper) Activate(_ context.Context, id string) error {
	h.record("helper_activate:" + id)
	return h.activateErr
}

func (h *fakeHelper) Deactivate(_ context.Context, id string) error {
	h.record("helper_deactivate:" + id)
	return h.deactivateErr
}

func (h *fakeHelper) Restart(_ context.Context, id string) error {
	h.record("helper_restart:" + id)
	return h.restartErr
}

func (h *fakeHelper) Uninstall(_ context.Context, id string, deleteData bool) error {
	h.record("helper_uninstall:" + id)
	h.uninstallID = id
	h.deleteData = deleteData
	return h.uninstallErr
}

func (h *fakeHelper) Logs(_ context.Context, id string, tail int) (string, error) {
	h.record("helper_logs:" + id)
	h.tail = tail
	if h.logsErr != nil {
		return "", h.logsErr
	}
	return h.logs, nil
}

type fakePreparer struct {
	prepareInstallErr  error
	prepareActivateErr error
	commitErr          error
	cleanupErr         error
	credentialsErr     error
	rotateErr          error

	credentials Credentials
	rotated     Credentials

	cleanupCause error
	calls        []string
	trace        *[]string
}

func (p *fakePreparer) record(call string) {
	p.calls = append(p.calls, call)
	if p.trace != nil {
		*p.trace = append(*p.trace, call)
	}
}

func (p *fakePreparer) PrepareInstall(_ context.Context, svc services.Service) error {
	p.record("prepare_install:" + string(svc.ID))
	return p.prepareInstallErr
}

func (p *fakePreparer) PrepareActivate(_ context.Context, svc services.Service) error {
	p.record("prepare_activate:" + string(svc.ID))
	return p.prepareActivateErr
}

func (p *fakePreparer) CommitActivate(_ context.Context, svc services.Service) error {
	p.record("commit_activate:" + string(svc.ID))
	return p.commitErr
}

func (p *fakePreparer) CleanupActivate(_ context.Context, svc services.Service, cause error) error {
	p.record("cleanup_activate:" + string(svc.ID))
	p.cleanupCause = cause
	return p.cleanupErr
}

func (p *fakePreparer) Credentials(_ context.Context, id services.ID) (Credentials, error) {
	p.record("credentials:" + string(id))
	if p.credentialsErr != nil {
		return Credentials{}, p.credentialsErr
	}
	return p.credentials, nil
}

func (p *fakePreparer) RotateCredentials(_ context.Context, id services.ID) (Credentials, error) {
	p.record("rotate_credentials:" + string(id))
	if p.rotateErr != nil {
		return Credentials{}, p.rotateErr
	}
	return p.rotated, nil
}

func newTestManager(t *testing.T, st Store, helper Helper, preparers map[services.ID]Preparer) *Manager {
	t.Helper()
	manager := NewManager(t.TempDir(), st, helper, testCatalog(), preparers)
	manager.now = func() time.Time { return testNow }
	return manager
}

func TestManagerListMergesCatalogHelperAndStore(t *testing.T) {
	root := t.TempDir()
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{
			ID:          string(services.IDNginx),
			State:       string(services.StateActive),
			Active:      true,
			Installed:   true,
			ContainerID: "container-nginx",
			Health:      "healthy",
			Image:       nginxImage,
			Ports: []helperproto.ServicePortStatus{
				{Host: 80, Container: 80, Protocol: "http"},
			},
		},
		{
			ID:        string(services.IDRedis),
			State:     string(services.StateNotInstalled),
			Installed: false,
		},
	}}
	store := newFakeStore(services.Record{
		ID:        services.IDMySQL,
		State:     services.StateInstalled,
		Image:     mysqlImage,
		LastError: "previous failure",
	})
	manager := NewManager(root, store, helper, testCatalog(), nil)

	views, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 3 {
		t.Fatalf("views = %d, want 3", len(views))
	}

	nginx := views[0]
	if nginx.ID != services.IDNginx ||
		nginx.State != services.StateActive ||
		!nginx.Active ||
		!nginx.Installed ||
		nginx.ContainerID != "container-nginx" ||
		nginx.Health != "healthy" {
		t.Fatalf("nginx view = %+v", nginx)
	}
	if len(nginx.Ports) != 1 || nginx.Ports[0].Host != 80 {
		t.Fatalf("nginx ports = %+v", nginx.Ports)
	}
	if nginx.ConfigPath != filepath.Join(root, "services", "nginx", "config") {
		t.Fatalf("nginx config path = %q", nginx.ConfigPath)
	}

	mysql := views[1]
	if mysql.State != services.StateInstalled || !mysql.Installed || mysql.Active || mysql.LastError != "previous failure" {
		t.Fatalf("mysql view = %+v", mysql)
	}

	redis := views[2]
	if redis.State != services.StateNotInstalled || redis.Installed {
		t.Fatalf("redis view = %+v", redis)
	}
}

func TestManagerListHelperFailureIsReturned(t *testing.T) {
	helper := &fakeHelper{statusErr: errors.New("helper socket unavailable")}
	store := newFakeStore()
	manager := newTestManager(t, store, helper, nil)

	views, err := manager.List(context.Background())
	if err == nil {
		t.Fatal("List succeeded, want helper error")
	}
	if !errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("error = %v, want ErrHelperUnavailable", err)
	}
	if views != nil {
		t.Fatalf("views = %+v, want nil", views)
	}
}

func TestManagerListStoreFailureIsReturned(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore()
	store.listErr = errors.New("database unavailable")
	manager := newTestManager(t, store, helper, nil)

	if _, err := manager.List(context.Background()); err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("error = %v, want store failure", err)
	}
}

func TestManagerGetUnknownService(t *testing.T) {
	helper := &fakeHelper{}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	_, err := manager.Get(context.Background(), services.ID("mystery"))
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if len(helper.calls) != 0 {
		t.Fatalf("helper calls = %v, want none", helper.calls)
	}
}

func TestManagerGetHelperFailure(t *testing.T) {
	helper := &fakeHelper{statusErr: errors.New("helper socket unavailable")}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	_, err := manager.Get(context.Background(), services.IDNginx)
	if !errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("error = %v, want ErrHelperUnavailable", err)
	}
}

func TestManagerInstallPersistsInstalledState(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore()
	prep := &fakePreparer{}
	manager := newTestManager(t, store, helper, map[services.ID]Preparer{services.IDNginx: prep})

	if err := manager.Install(context.Background(), services.IDNginx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(prep.calls, ","); got != "prepare_install:nginx" {
		t.Fatalf("preparer calls = %q", got)
	}
	if got := strings.Join(helper.calls, ","); got != "helper_install:nginx" {
		t.Fatalf("helper calls = %q", got)
	}

	rec := store.last(t)
	if rec.ID != services.IDNginx ||
		rec.State != services.StateInstalled ||
		rec.Active ||
		rec.LastOperation != "install" ||
		rec.LastError != "" ||
		rec.Image != nginxImage {
		t.Fatalf("saved record = %+v", rec)
	}
	if !rec.UpdatedAt.Equal(testNow) {
		t.Fatalf("saved UpdatedAt = %s, want %s", rec.UpdatedAt, testNow)
	}
}

func TestManagerInstallHelperFailurePersistsErrorState(t *testing.T) {
	helper := &fakeHelper{installErr: errors.New("image pull failed")}
	store := newFakeStore()
	manager := newTestManager(t, store, helper, nil)

	err := manager.Install(context.Background(), services.IDMySQL)
	if err == nil || !strings.Contains(err.Error(), "image pull failed") {
		t.Fatalf("error = %v", err)
	}
	if !errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("error = %v, want ErrHelperUnavailable", err)
	}

	rec := store.last(t)
	if rec.State != services.StateError || rec.LastOperation != "install" || !strings.Contains(rec.LastError, "image pull failed") {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerInstallPreparerFailureSkipsHelper(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore()
	prep := &fakePreparer{prepareInstallErr: errors.New("scaffold failed")}
	manager := newTestManager(t, store, helper, map[services.ID]Preparer{services.IDNginx: prep})

	err := manager.Install(context.Background(), services.IDNginx)
	if err == nil || !strings.Contains(err.Error(), "scaffold failed") {
		t.Fatalf("error = %v", err)
	}
	if len(helper.calls) != 0 {
		t.Fatalf("helper calls = %v, want none", helper.calls)
	}
	rec := store.last(t)
	if rec.State != services.StateError || !strings.Contains(rec.LastError, "scaffold failed") {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerInstallInvalidRequestIsNotHelperUnavailable(t *testing.T) {
	helper := &fakeHelper{installErr: fmt.Errorf("%w: invalid service", helperproto.ErrInvalidRequest)}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	err := manager.Install(context.Background(), services.IDNginx)
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("error = %v, must not be ErrHelperUnavailable", err)
	}
}

func TestManagerActivateOrderingAndState(t *testing.T) {
	var trace []string
	helper := &fakeHelper{trace: &trace}
	store := newFakeStore()
	prep := &fakePreparer{trace: &trace}
	manager := newTestManager(t, store, helper, map[services.ID]Preparer{services.IDNginx: prep})

	if err := manager.Activate(context.Background(), services.IDNginx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"prepare_activate:nginx",
		"helper_activate:nginx",
		"commit_activate:nginx",
	}
	if strings.Join(trace, ",") != strings.Join(want, ",") {
		t.Fatalf("trace = %v, want %v", trace, want)
	}

	rec := store.last(t)
	if rec.State != services.StateActive || !rec.Active || rec.LastOperation != "activate" || rec.LastError != "" {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerActivateHelperFailureCleansUpAndPersistsError(t *testing.T) {
	cause := errors.New("health check failed")
	helper := &fakeHelper{activateErr: cause}
	store := newFakeStore()
	prep := &fakePreparer{}
	manager := newTestManager(t, store, helper, map[services.ID]Preparer{services.IDNginx: prep})

	err := manager.Activate(context.Background(), services.IDNginx)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want original cause", err)
	}
	if !errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("error = %v, want ErrHelperUnavailable", err)
	}
	if len(prep.calls) == 0 || prep.calls[len(prep.calls)-1] != "cleanup_activate:nginx" {
		t.Fatalf("preparer calls = %v, want cleanup", prep.calls)
	}
	if !errors.Is(prep.cleanupCause, cause) {
		t.Fatalf("cleanup cause = %v, want %v", prep.cleanupCause, cause)
	}

	rec := store.last(t)
	if rec.State != services.StateError || rec.Active || rec.LastOperation != "activate" || !strings.Contains(rec.LastError, "health check failed") {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerActivateCommitFailureCleansUp(t *testing.T) {
	cause := errors.New("commit failed")
	helper := &fakeHelper{}
	prep := &fakePreparer{commitErr: cause}
	store := newFakeStore()
	manager := newTestManager(t, store, helper, map[services.ID]Preparer{services.IDNginx: prep})

	err := manager.Activate(context.Background(), services.IDNginx)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want commit cause", err)
	}
	if !errors.Is(prep.cleanupCause, cause) {
		t.Fatalf("cleanup cause = %v, want %v", prep.cleanupCause, cause)
	}
	rec := store.last(t)
	if rec.State != services.StateError || rec.Active || rec.LastOperation != "activate" {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerActivatePrepareFailureCleansUp(t *testing.T) {
	cause := errors.New("prepare failed")
	helper := &fakeHelper{}
	prep := &fakePreparer{prepareActivateErr: cause}
	manager := newTestManager(t, newFakeStore(), helper, map[services.ID]Preparer{services.IDNginx: prep})

	err := manager.Activate(context.Background(), services.IDNginx)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want prepare cause", err)
	}
	if len(prep.calls) != 2 || prep.calls[1] != "cleanup_activate:nginx" {
		t.Fatalf("preparer calls = %v, want prepare then cleanup", prep.calls)
	}
}

func TestManagerDeactivatePersistsStoppedState(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore()
	manager := newTestManager(t, store, helper, nil)

	if err := manager.Deactivate(context.Background(), services.IDApache); err != nil {
		// apache is intentionally outside the small test catalog.
		if !errors.Is(err, services.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if err := manager.Deactivate(context.Background(), services.IDNginx); err != nil {
		t.Fatal(err)
	}
	rec := store.last(t)
	if rec.State != services.StateInstalledButStopped || rec.Active || rec.LastOperation != "deactivate" {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerDeactivateFailurePreservesActive(t *testing.T) {
	helper := &fakeHelper{deactivateErr: errors.New("stop failed")}
	store := newFakeStore(services.Record{
		ID:     services.IDNginx,
		State:  services.StateActive,
		Active: true,
	})
	manager := newTestManager(t, store, helper, nil)

	err := manager.Deactivate(context.Background(), services.IDNginx)
	if err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("error = %v", err)
	}
	rec := store.last(t)
	if rec.State != services.StateError || !rec.Active || !strings.Contains(rec.LastError, "stop failed") {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerRestartPersistsActiveState(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore(services.Record{ID: services.IDRedis, State: services.StateInstalledButStopped})
	manager := newTestManager(t, store, helper, nil)

	if err := manager.Restart(context.Background(), services.IDRedis); err != nil {
		t.Fatal(err)
	}
	rec := store.last(t)
	if rec.State != services.StateActive || !rec.Active || rec.LastOperation != "restart" {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerUninstallDeletesStateAndPreservesDataByDefault(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore(services.Record{ID: services.IDNginx, State: services.StateInstalled})
	manager := newTestManager(t, store, helper, nil)

	result, err := manager.Uninstall(context.Background(), services.IDNginx, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath != "" {
		t.Fatalf("backup path = %q, want empty (helper owns backups)", result.BackupPath)
	}
	if helper.deleteData {
		t.Fatal("deleteData = true, want false")
	}
	if len(store.deleted) != 1 || store.deleted[0] != services.IDNginx {
		t.Fatalf("deleted = %v", store.deleted)
	}
	if _, ok := store.records[services.IDNginx]; ok {
		t.Fatal("service record still present after uninstall")
	}
}

func TestManagerUninstallPassesDeleteData(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore(services.Record{ID: services.IDMySQL, State: services.StateInstalled})
	manager := newTestManager(t, store, helper, nil)

	if _, err := manager.Uninstall(context.Background(), services.IDMySQL, true); err != nil {
		t.Fatal(err)
	}
	if !helper.deleteData || helper.uninstallID != "mysql" {
		t.Fatalf("uninstall request = id %q deleteData %t", helper.uninstallID, helper.deleteData)
	}
}

func TestManagerUninstallMissingStateSucceeds(t *testing.T) {
	helper := &fakeHelper{}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	if _, err := manager.Uninstall(context.Background(), services.IDRedis, false); err != nil {
		t.Fatalf("Uninstall = %v, want nil for absent state", err)
	}
}

func TestManagerUninstallFailureKeepsState(t *testing.T) {
	helper := &fakeHelper{uninstallErr: errors.New("remove failed")}
	store := newFakeStore(services.Record{ID: services.IDNginx, State: services.StateActive, Active: true})
	manager := newTestManager(t, store, helper, nil)

	_, err := manager.Uninstall(context.Background(), services.IDNginx, true)
	if err == nil || !strings.Contains(err.Error(), "remove failed") {
		t.Fatalf("error = %v", err)
	}
	if _, ok := store.records[services.IDNginx]; !ok {
		t.Fatal("service record removed after failed uninstall")
	}
	rec := store.last(t)
	if rec.State != services.StateError || !rec.Active || !strings.Contains(rec.LastError, "remove failed") {
		t.Fatalf("saved record = %+v", rec)
	}
}

func TestManagerLogsPassesTail(t *testing.T) {
	helper := &fakeHelper{logs: "line one\nline two\n"}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	logs, err := manager.Logs(context.Background(), services.IDMySQL, 25)
	if err != nil {
		t.Fatal(err)
	}
	if logs != helper.logs || helper.tail != 25 {
		t.Fatalf("logs = %q tail = %d", logs, helper.tail)
	}
}

func TestManagerLogsHelperFailure(t *testing.T) {
	helper := &fakeHelper{logsErr: errors.New("logs unavailable")}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	_, err := manager.Logs(context.Background(), services.IDNginx, 10)
	if !errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("error = %v, want ErrHelperUnavailable", err)
	}
}

func TestManagerCredentialsWithoutPreparerIsUnsupported(t *testing.T) {
	helper := &fakeHelper{}
	manager := newTestManager(t, newFakeStore(), helper, nil)

	if _, err := manager.Credentials(context.Background(), services.IDMySQL); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("error = %v, want ErrCredentialsUnsupported", err)
	}
	if _, err := manager.RotateCredentials(context.Background(), services.IDMySQL); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("rotate error = %v, want ErrCredentialsUnsupported", err)
	}
	if len(helper.calls) != 0 {
		t.Fatalf("helper calls = %v, want none", helper.calls)
	}
}

func TestManagerCredentialsUsesPreparerAndNeverPersistsSecrets(t *testing.T) {
	creds := Credentials{
		Engine:   "mysql",
		Username: "devstack",
		Password: "super-secret-password",
		Database: "devstack",
		Host:     "127.0.0.1",
		Port:     3306,
	}
	rotated := creds
	rotated.Password = "rotated-secret-password"
	prep := &fakePreparer{credentials: creds, rotated: rotated}
	store := newFakeStore()
	manager := newTestManager(t, store, &fakeHelper{}, map[services.ID]Preparer{services.IDMySQL: prep})

	got, err := manager.Credentials(context.Background(), services.IDMySQL)
	if err != nil {
		t.Fatal(err)
	}
	if got != creds {
		t.Fatalf("credentials = %+v, want %+v", got, creds)
	}
	gotRotated, err := manager.RotateCredentials(context.Background(), services.IDMySQL)
	if err != nil {
		t.Fatal(err)
	}
	if gotRotated != rotated {
		t.Fatalf("rotated = %+v, want %+v", gotRotated, rotated)
	}
	if got := strings.Join(prep.calls, ","); got != "credentials:mysql,rotate_credentials:mysql" {
		t.Fatalf("preparer calls = %q", got)
	}

	data, err := json.Marshal(store.records)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{creds.Password, rotated.Password} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("persisted service state leaks credentials: %s", data)
		}
	}
}

func TestManagerUnknownServiceActions(t *testing.T) {
	helper := &fakeHelper{}
	store := newFakeStore()
	manager := newTestManager(t, store, helper, nil)
	ctx := context.Background()
	unknown := services.ID("mystery")

	tests := []struct {
		name string
		call func() error
	}{
		{"install", func() error { return manager.Install(ctx, unknown) }},
		{"activate", func() error { return manager.Activate(ctx, unknown) }},
		{"deactivate", func() error { return manager.Deactivate(ctx, unknown) }},
		{"restart", func() error { return manager.Restart(ctx, unknown) }},
		{"uninstall", func() error { _, err := manager.Uninstall(ctx, unknown, false); return err }},
		{"logs", func() error { _, err := manager.Logs(ctx, unknown, 0); return err }},
		{"credentials", func() error { _, err := manager.Credentials(ctx, unknown); return err }},
		{"rotate credentials", func() error { _, err := manager.RotateCredentials(ctx, unknown); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, services.ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
		})
	}
	if len(helper.calls) != 0 {
		t.Fatalf("helper calls = %v, want none", helper.calls)
	}
	if len(store.saved) != 0 || len(store.deleted) != 0 {
		t.Fatalf("store mutated: saved=%v deleted=%v", store.saved, store.deleted)
	}
}

func TestNoopPreparer(t *testing.T) {
	prep := NoopPreparer{}
	ctx := context.Background()
	svc, _ := testCatalog().Get(services.IDNginx)

	if err := prep.PrepareInstall(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := prep.PrepareActivate(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := prep.CommitActivate(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := prep.CleanupActivate(ctx, svc, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if _, err := prep.Credentials(ctx, svc.ID); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("Credentials error = %v, want ErrCredentialsUnsupported", err)
	}
	if _, err := prep.RotateCredentials(ctx, svc.ID); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("RotateCredentials error = %v, want ErrCredentialsUnsupported", err)
	}
}
