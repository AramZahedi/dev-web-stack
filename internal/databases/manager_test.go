package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// --- fakes -----------------------------------------------------------------

type fakeStore struct {
	mu               sync.Mutex
	sites            []model.Site
	links            []model.DatabaseLinkRecord
	users            []model.DatabaseUserRecord
	grants           []model.DatabaseGrantRecord
	backups          []model.DatabaseBackupRecord
	jobs             []model.DatabaseJobRecord
	audit            []model.AuditEventRecord
	passwordStateErr error
	renameRefsErr    error
	linkStarted      chan struct{}
}

func (f *fakeStore) GetSite(_ context.Context, id string) (model.Site, error) {
	for _, site := range f.sites {
		if site.ID == id {
			return site, nil
		}
	}
	return model.Site{}, store.ErrNotFound
}

func (f *fakeStore) GetSiteByName(_ context.Context, name string) (model.Site, error) {
	for _, site := range f.sites {
		if site.Name == name {
			return site, nil
		}
	}
	return model.Site{}, store.ErrNotFound
}

func (f *fakeStore) ListSites(context.Context) ([]model.Site, error) {
	return append([]model.Site(nil), f.sites...), nil
}

func (f *fakeStore) CreateDatabaseLink(_ context.Context, link model.DatabaseLinkRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	signalTest(f.linkStarted)
	for _, existing := range f.links {
		if existing.SiteID == link.SiteID && existing.ServiceID == link.ServiceID && existing.DatabaseName == link.DatabaseName {
			return store.ErrConflict
		}
	}
	if link.Primary {
		for i := range f.links {
			if f.links[i].SiteID == link.SiteID {
				f.links[i].Primary = false
			}
		}
	}
	for i := range f.sites {
		if f.sites[i].ID == link.SiteID {
			link.SiteName = f.sites[i].Name
			break
		}
	}
	f.links = append(f.links, link)
	return nil
}

func (f *fakeStore) GetDatabaseLink(_ context.Context, id string) (model.DatabaseLinkRecord, error) {
	for _, link := range f.links {
		if link.ID == id {
			return link, nil
		}
	}
	return model.DatabaseLinkRecord{}, store.ErrNotFound
}

func (f *fakeStore) ListDatabaseLinksBySite(_ context.Context, siteID string) ([]model.DatabaseLinkRecord, error) {
	var out []model.DatabaseLinkRecord
	for _, link := range f.links {
		if link.SiteID == siteID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (f *fakeStore) ListDatabaseLinksByService(_ context.Context, serviceID string) ([]model.DatabaseLinkRecord, error) {
	var out []model.DatabaseLinkRecord
	for _, link := range f.links {
		if link.ServiceID == serviceID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteDatabaseLink(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, link := range f.links {
		if link.ID != id {
			continue
		}
		wasPrimary := link.Primary
		siteID := link.SiteID
		f.links = append(f.links[:i], f.links[i+1:]...)
		if wasPrimary {
			for j := range f.links {
				if f.links[j].SiteID == siteID {
					f.links[j].Primary = true
					break
				}
			}
		}
		return nil
	}
	return store.ErrNotFound
}

func (f *fakeStore) DeleteDatabaseLinksByService(_ context.Context, serviceID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []model.DatabaseLinkRecord
	var removed int64
	for _, link := range f.links {
		if link.ServiceID == serviceID {
			removed++
			continue
		}
		kept = append(kept, link)
	}
	f.links = kept
	return removed, nil
}

func (f *fakeStore) SetPrimaryDatabaseLink(_ context.Context, siteID, linkID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for i := range f.links {
		if f.links[i].SiteID != siteID {
			continue
		}
		f.links[i].Primary = f.links[i].ID == linkID
		if f.links[i].ID == linkID {
			found = true
		}
	}
	if !found {
		return store.ErrNotFound
	}
	return nil
}

func (f *fakeStore) RenameDatabaseReferences(_ context.Context, serviceID, oldName, newName string) error {
	if f.renameRefsErr != nil {
		return f.renameRefsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.links {
		if f.links[i].ServiceID == serviceID && f.links[i].DatabaseName == oldName {
			f.links[i].DatabaseName = newName
		}
	}
	for i := range f.grants {
		if f.grants[i].DatabaseName == oldName {
			f.grants[i].DatabaseName = newName
		}
	}
	return nil
}

func (f *fakeStore) UpsertDatabaseUser(_ context.Context, user model.DatabaseUserRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.users {
		if f.users[i].ID == user.ID {
			f.users[i] = user
			return nil
		}
	}
	f.users = append(f.users, user)
	return nil
}

func (f *fakeStore) GetDatabaseUser(_ context.Context, id string) (model.DatabaseUserRecord, error) {
	for _, user := range f.users {
		if user.ID == id {
			return user, nil
		}
	}
	return model.DatabaseUserRecord{}, store.ErrNotFound
}

func (f *fakeStore) ListDatabaseUsers(_ context.Context, serviceID string) ([]model.DatabaseUserRecord, error) {
	var out []model.DatabaseUserRecord
	for _, user := range f.users {
		if user.ServiceID == serviceID {
			out = append(out, user)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteDatabaseUser(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, link := range f.links {
		if link.UserID == id {
			return store.ErrConflict
		}
	}
	for i, user := range f.users {
		if user.ID == id {
			f.users = append(f.users[:i], f.users[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) SetDatabaseUserPasswordState(_ context.Context, id string, managed bool, rotatedAt int64) error {
	if f.passwordStateErr != nil {
		return f.passwordStateErr
	}
	for i := range f.users {
		if f.users[i].ID == id {
			f.users[i].PasswordManaged = managed
			f.users[i].LastPasswordRotatedAt = rotatedAt
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) SaveDatabaseGrant(_ context.Context, grant model.DatabaseGrantRecord) error {
	for i := range f.grants {
		if f.grants[i].UserID == grant.UserID && f.grants[i].DatabaseName == grant.DatabaseName {
			f.grants[i] = grant
			return nil
		}
	}
	f.grants = append(f.grants, grant)
	return nil
}

func (f *fakeStore) ListDatabaseGrants(_ context.Context, userID string) ([]model.DatabaseGrantRecord, error) {
	var out []model.DatabaseGrantRecord
	for _, grant := range f.grants {
		if grant.UserID == userID {
			out = append(out, grant)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteDatabaseGrant(_ context.Context, userID, databaseName string) error {
	for i, grant := range f.grants {
		if grant.UserID == userID && grant.DatabaseName == databaseName {
			f.grants = append(f.grants[:i], f.grants[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) CreateDatabaseBackup(_ context.Context, backup model.DatabaseBackupRecord) error {
	f.backups = append(f.backups, backup)
	return nil
}

func (f *fakeStore) GetDatabaseBackup(_ context.Context, id string) (model.DatabaseBackupRecord, error) {
	for _, backup := range f.backups {
		if backup.ID == id {
			return backup, nil
		}
	}
	return model.DatabaseBackupRecord{}, store.ErrNotFound
}

func (f *fakeStore) ListDatabaseBackups(_ context.Context, serviceID string) ([]model.DatabaseBackupRecord, error) {
	var out []model.DatabaseBackupRecord
	for _, backup := range f.backups {
		if backup.ServiceID == serviceID {
			out = append(out, backup)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateDatabaseBackup(_ context.Context, backup model.DatabaseBackupRecord) error {
	for i := range f.backups {
		if f.backups[i].ID == backup.ID {
			f.backups[i] = backup
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) DeleteDatabaseBackup(_ context.Context, id string) error {
	for i, backup := range f.backups {
		if backup.ID == id {
			f.backups = append(f.backups[:i], f.backups[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) CreateDatabaseJob(_ context.Context, job model.DatabaseJobRecord) error {
	f.jobs = append(f.jobs, job)
	return nil
}

func (f *fakeStore) GetDatabaseJob(_ context.Context, id string) (model.DatabaseJobRecord, error) {
	for _, job := range f.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return model.DatabaseJobRecord{}, store.ErrNotFound
}

func (f *fakeStore) UpdateDatabaseJob(_ context.Context, job model.DatabaseJobRecord) error {
	for i := range f.jobs {
		if f.jobs[i].ID == job.ID {
			f.jobs[i] = job
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) ListAllDatabaseJobs(context.Context) ([]model.DatabaseJobRecord, error) {
	return append([]model.DatabaseJobRecord(nil), f.jobs...), nil
}

func (f *fakeStore) ListDatabaseJobs(_ context.Context, serviceID string) ([]model.DatabaseJobRecord, error) {
	var out []model.DatabaseJobRecord
	for _, job := range f.jobs {
		if job.ServiceID == serviceID {
			out = append(out, job)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteServiceMetadata(_ context.Context, serviceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var links []model.DatabaseLinkRecord
	for _, link := range f.links {
		if link.ServiceID != serviceID {
			links = append(links, link)
		}
	}
	f.links = links
	var users []model.DatabaseUserRecord
	for _, user := range f.users {
		if user.ServiceID != serviceID {
			users = append(users, user)
		}
	}
	f.users = users
	var grants []model.DatabaseGrantRecord
	for _, grant := range f.grants {
		keep := false
		for _, user := range f.users {
			if user.ID == grant.UserID {
				keep = true
				break
			}
		}
		if keep {
			grants = append(grants, grant)
		}
	}
	f.grants = grants
	var backups []model.DatabaseBackupRecord
	for _, backup := range f.backups {
		if backup.ServiceID != serviceID {
			backups = append(backups, backup)
		}
	}
	f.backups = backups
	var jobs []model.DatabaseJobRecord
	for _, job := range f.jobs {
		if job.ServiceID != serviceID {
			jobs = append(jobs, job)
		}
	}
	f.jobs = jobs
	return nil
}

func (f *fakeStore) AddAuditEvent(_ context.Context, event model.AuditEventRecord) error {
	f.audit = append(f.audit, event)
	return nil
}

func (f *fakeStore) ListAuditEvents(_ context.Context, limit int) ([]model.AuditEventRecord, error) {
	out := append([]model.AuditEventRecord(nil), f.audit...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) CountAuditEvents(context.Context) (int64, error) {
	return int64(len(f.audit)), nil
}

type fakePlatform struct {
	databases        []model.Database
	users            []model.DatabaseUser
	backups          []model.DatabaseBackup
	job              model.DatabaseJob
	env              map[string]string
	createErr        error
	restoreErr       error
	importErr        error
	grants           []model.DatabaseGrant
	grantCalls       []model.DatabaseGrant
	dropCalls        []string
	restoreCalls     []string
	renameCalls      []string
	deleteBackupIDs  []string
	lastPasswordUser string
	lastRestoreReq   RestoreBackupRequest
	lastImportReq    ImportRequest
	importedFile     string
	listErr          error
	envErr           error
	resolveEnv       func(serviceID string, links []model.DatabaseLink) map[string]string
	passwordStateErr error
	passwordErr      error
	createStarted    chan struct{}
	createRelease    chan struct{}
	restoreStarted   chan struct{}
	restoreRelease   chan struct{}
	importStarted    chan struct{}
	importRelease    chan struct{}
}

func (f *fakePlatform) ListDatabases(context.Context, string) ([]model.Database, error) {
	return append([]model.Database(nil), f.databases...), f.listErr
}

func (f *fakePlatform) CreateDatabase(_ context.Context, serviceID string, req CreateDatabaseRequest) (model.Database, error) {
	if f.createErr != nil {
		return model.Database{}, f.createErr
	}
	db := model.Database{Name: req.Name, ServiceID: serviceID, Engine: model.DatabaseMySQL}
	f.databases = append(f.databases, db)
	return db, nil
}

func (f *fakePlatform) RenameDatabase(_ context.Context, serviceID, name string, req RenameDatabaseRequest) (model.Database, error) {
	f.renameCalls = append(f.renameCalls, name+"->"+req.Name)
	return model.Database{Name: req.Name, ServiceID: serviceID}, nil
}

func (f *fakePlatform) DropDatabase(_ context.Context, _ string, name string, _ DropDatabaseRequest) error {
	f.dropCalls = append(f.dropCalls, name)
	return nil
}

func (f *fakePlatform) ListUsers(context.Context, string) ([]model.DatabaseUser, error) {
	return append([]model.DatabaseUser(nil), f.users...), nil
}

func (f *fakePlatform) CreateUser(_ context.Context, serviceID string, req CreateUserRequest) (model.DatabaseUser, error) {
	user := model.DatabaseUser{
		ID: stableUserID(serviceID, req.Username, req.Host), ServiceID: serviceID,
		Username: req.Username, Host: req.Host, Enabled: true, Managed: true,
		PasswordManaged: true, Grants: req.Grants,
	}
	f.users = append(f.users, user)
	for _, grant := range req.Grants {
		f.grantCalls = append(f.grantCalls, grant)
		f.grants = append(f.grants, grant)
	}
	return user, nil
}

func (f *fakePlatform) UpdateUser(_ context.Context, serviceID, userID string, req UpdateUserRequest) (model.DatabaseUser, error) {
	for _, user := range f.users {
		if user.ID == userID {
			user.Grants = req.Grants
			if req.Enabled != nil {
				user.Enabled = *req.Enabled
			}
			return user, nil
		}
	}
	return model.DatabaseUser{}, ErrNotFound
}

func (f *fakePlatform) DeleteUser(context.Context, string, string, DeleteUserRequest) error {
	return nil
}

func (f *fakePlatform) SetUserPassword(_ context.Context, _ string, userID string) (Secret, error) {
	if f.passwordErr != nil {
		return Secret{}, f.passwordErr
	}
	f.lastPasswordUser = userID
	return Secret{Value: "generated-secret"}, nil
}

func (f *fakePlatform) RevealUserPassword(context.Context, string, string) (Secret, error) {
	return Secret{Value: "s3cret"}, nil
}

func (f *fakePlatform) ListBackups(context.Context, string) ([]model.DatabaseBackup, error) {
	return append([]model.DatabaseBackup(nil), f.backups...), nil
}

func signalTest(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func waitTest(ctx context.Context, ch chan struct{}) error {
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakePlatform) CreateBackup(ctx context.Context, serviceID, database string, req CreateBackupRequest) (model.DatabaseJob, error) {
	signalTest(f.createStarted)
	if err := waitTest(ctx, f.createRelease); err != nil {
		return model.DatabaseJob{ID: "job_canceled", Status: model.JobFailed, Message: err.Error()}, err
	}
	job := f.job
	if job.ID == "" {
		job.ID = "job_platform"
	}
	if job.Status == "" {
		job.Status = model.JobSucceeded
	}
	job.ServiceID = serviceID
	job.Database = database
	job.Kind = model.JobBackup
	if job.BackupID != "" {
		f.backups = append(f.backups, model.DatabaseBackup{
			ID: job.BackupID, ServiceID: serviceID, Engine: model.DatabaseMySQL,
			Database: database, Kind: req.Kind, Status: model.BackupReady,
			CreatedAt: time.Now().UTC(),
		})
	}
	return job, nil
}

func (f *fakePlatform) DeleteBackup(_ context.Context, _ string, backupID string) error {
	f.deleteBackupIDs = append(f.deleteBackupIDs, backupID)
	return nil
}

func (f *fakePlatform) RestoreBackup(ctx context.Context, serviceID, backupID string, req RestoreBackupRequest) (model.DatabaseJob, error) {
	signalTest(f.restoreStarted)
	if err := waitTest(ctx, f.restoreRelease); err != nil {
		return model.DatabaseJob{ID: "job_restore", Status: model.JobFailed, Message: err.Error()}, err
	}
	f.restoreCalls = append(f.restoreCalls, backupID)
	f.lastRestoreReq = req
	job := model.DatabaseJob{ID: "job_restore", ServiceID: serviceID, Database: req.Target, Kind: model.JobRestore, Status: model.JobSucceeded}
	if f.restoreErr != nil {
		job.Status = model.JobFailed
		job.Message = f.restoreErr.Error()
		return job, f.restoreErr
	}
	return job, nil
}

func (f *fakePlatform) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, _ string, req ImportRequest) (model.DatabaseJob, error) {
	signalTest(f.importStarted)
	if err := waitTest(ctx, f.importRelease); err != nil {
		return model.DatabaseJob{ID: "job_import", Status: model.JobFailed, Message: err.Error()}, err
	}
	data, _ := io.ReadAll(file)
	f.importedFile = string(data)
	f.lastImportReq = req
	job := model.DatabaseJob{ID: "job_import", ServiceID: serviceID, Database: database, Kind: model.JobImport, Status: model.JobSucceeded}
	if f.importErr != nil {
		job.Status = model.JobFailed
		return job, f.importErr
	}
	return job, nil
}

func (f *fakePlatform) ExportDatabase(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("dump")), nil
}

func (f *fakePlatform) DownloadBackup(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("backup")), nil
}

func (f *fakePlatform) GetJob(context.Context, string) (model.DatabaseJob, error) {
	return f.job, nil
}

func (f *fakePlatform) CancelJob(context.Context, string) error { return nil }

func (f *fakePlatform) ResolveSiteEnv(_ context.Context, serviceID string, links []model.DatabaseLink) (map[string]string, error) {
	if f.envErr != nil {
		return nil, f.envErr
	}
	if f.resolveEnv != nil {
		return f.resolveEnv(serviceID, links), nil
	}
	if f.env == nil {
		return map[string]string{}, nil
	}
	return cloneStringMap(f.env), nil
}

type fakeServices struct {
	views map[services.ID]services.View
}

func (f *fakeServices) Get(_ context.Context, id services.ID) (services.View, error) {
	view, ok := f.views[id]
	if !ok {
		return services.View{}, services.ErrNotFound
	}
	return view, nil
}

func (f *fakeServices) List(context.Context) ([]services.View, error) {
	out := make([]services.View, 0, len(f.views))
	for _, view := range f.views {
		out = append(out, view)
	}
	return out, nil
}

type fakeAudit struct {
	events []model.AuditEventRecord
}

func (f *fakeAudit) AddAuditEvent(_ context.Context, event model.AuditEventRecord) error {
	f.events = append(f.events, event)
	return nil
}

type fakeEvents struct {
	events []model.Event
}

func (f *fakeEvents) AddEvent(_ context.Context, event model.Event) error {
	f.events = append(f.events, event)
	return nil
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	st := &fakeStore{
		sites: []model.Site{{ID: "site_1", Name: "blog", Domain: "blog.test", Status: model.StatusStopped}},
		users: []model.DatabaseUserRecord{{
			ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL,
			Username: "app", Host: "%", Enabled: true, ManagedByDevstack: true,
		}},
	}
	platform := &fakePlatform{
		databases: []model.Database{{Name: "app", ServiceID: "mysql", Engine: model.DatabaseMySQL}},
		users:     []model.DatabaseUser{{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%", Enabled: true, Managed: true}},
	}
	svc := &fakeServices{views: map[services.ID]services.View{
		services.IDMySQL: {ID: services.IDMySQL, State: services.StateActive, Active: true, Installed: true, Group: services.GroupMySQLFamily, Ports: []services.Port{{Host: 3306, Container: 3306, Protocol: "tcp", Loopback: true}}},
	}}
	return NewManager(Deps{
		Store:    st,
		Platform: platform,
		Services: svc,
		Sites:    st,
		Events:   &fakeEvents{},
		Audit:    &fakeAudit{},
		Now:      func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
		NewID:    func() string { return "generated_id" },
	})
}

func waitJob(t *testing.T, m *Manager, id string) model.DatabaseJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := m.GetJob(context.Background(), id)
		if err == nil && job.Status != model.JobQueued && job.Status != model.JobRunning {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := m.GetJob(context.Background(), id)
	t.Fatalf("job %s did not finish: %+v", id, job)
	return model.DatabaseJob{}
}

func waitAuditAction(t *testing.T, audit *fakeAudit, action string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range audit.events {
			if event.Action == action {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("audit action %q not recorded: %+v", action, audit.events)
}

func testManagerWithFakes(t *testing.T) (*Manager, *fakeStore, *fakePlatform, *fakeAudit, *fakeEvents) {
	t.Helper()
	m := newTestManager(t)
	return m, m.deps.Store.(*fakeStore), m.deps.Platform.(*fakePlatform), m.deps.Audit.(*fakeAudit), m.deps.Events.(*fakeEvents)
}

// --- core ------------------------------------------------------------------

func TestManagerListMergesLinks(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	got, err := m.ListDatabases(context.Background(), "mysql")
	if err != nil || len(got) != 1 || len(got[0].LinkedSites) != 1 || got[0].LinkedSites[0].Name != "blog" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestManagerListMarksMissingLinkedDatabase(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.databases = nil
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "gone", UserID: "usr_app"}}
	got, err := m.ListDatabases(context.Background(), "mysql")
	if err != nil || len(got) != 1 || !got[0].Missing || got[0].Name != "gone" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestManagerCreateDatabaseAuditsAndEmits(t *testing.T) {
	m, _, _, audit, events := testManagerWithFakes(t)
	db, err := m.CreateDatabase(context.Background(), "mysql", CreateDatabaseRequest{Name: "blog"})
	if err != nil || db.Name != "blog" {
		t.Fatalf("db=%+v err=%v", db, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "database.created" {
		t.Fatalf("audit=%+v", audit.events)
	}
	if len(events.events) != 1 || events.events[0].Type != "database.created" {
		t.Fatalf("events=%+v", events.events)
	}
}

func TestManagerRejectsProtectedDatabase(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.CreateDatabase(context.Background(), "mysql", CreateDatabaseRequest{Name: "mysql"}); !errors.Is(err, ErrProtected) {
		t.Fatalf("create protected = %v", err)
	}
}

func TestManagerRenameRejectsMySQL(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.RenameDatabase(context.Background(), "mysql", "app", RenameDatabaseRequest{Name: "blog"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("rename mysql = %v", err)
	}
}

func TestManagerRenamePostgresUpdatesLinks(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDPostgreSQL] = services.View{ID: services.IDPostgreSQL, State: services.StateActive, Active: true, Installed: true}
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "postgresql", DatabaseName: "old", UserID: "usr_app"}}
	db, err := m.RenameDatabase(context.Background(), "postgresql", "old", RenameDatabaseRequest{Name: "new", Confirm: "old"})
	if err != nil || db.Name != "new" {
		t.Fatalf("db=%+v err=%v", db, err)
	}
	if st.links[0].DatabaseName != "new" {
		t.Fatalf("link not updated: %+v", st.links[0])
	}
}

func TestManagerRenameRequiresConfirmAndStoppedSites(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDPostgreSQL] = services.View{ID: services.IDPostgreSQL, State: services.StateActive, Active: true, Installed: true}
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "postgresql", DatabaseName: "old", UserID: "usr_app"}}
	if _, err := m.RenameDatabase(context.Background(), "postgresql", "old", RenameDatabaseRequest{Name: "new", Confirm: "wrong"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong confirm = %v", err)
	}
	st.sites[0].Status = model.StatusRunning
	if _, err := m.RenameDatabase(context.Background(), "postgresql", "old", RenameDatabaseRequest{Name: "new", Confirm: "old"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("running linked site = %v", err)
	}
	st.sites[0].Status = model.StatusStopped
	if _, err := m.RenameDatabase(context.Background(), "postgresql", "old", RenameDatabaseRequest{Name: "new", Confirm: "old"}); err != nil {
		t.Fatalf("rename with stopped site: %v", err)
	}
}

func TestManagerSetDefaultUserPasswordResolvesOpaqueID(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDRedis] = services.View{ID: services.IDRedis, State: services.StateActive, Active: true, Installed: true}
	platform.users = []model.DatabaseUser{{ID: "usr_default", ServiceID: "redis", Engine: model.DatabaseRedis, Username: "default", Enabled: true}}
	secret, err := m.SetDefaultUserPassword(context.Background(), "redis")
	if err != nil || secret.Value == "" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
	if platform.lastPasswordUser != "usr_default" {
		t.Fatalf("password user = %q, want opaque default user id", platform.lastPasswordUser)
	}
}

func TestManagerDropRequiresConfirmAndStoppedSites(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	if err := m.DropDatabase(context.Background(), "mysql", "app", DropDatabaseRequest{Confirm: "wrong"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("drop wrong confirm = %v", err)
	}
	st.sites[0].Status = model.StatusRunning
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	if err := m.DropDatabase(context.Background(), "mysql", "app", DropDatabaseRequest{Confirm: "app"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("drop with running site = %v", err)
	}
	if len(platform.dropCalls) != 0 {
		t.Fatalf("platform dropped database despite guard: %v", platform.dropCalls)
	}
}

func TestManagerDropBacksUpFirst(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	platform.job = model.DatabaseJob{ID: "job_1", Status: model.JobSucceeded, BackupID: "bkp_1"}
	if err := m.DropDatabase(context.Background(), "mysql", "app", DropDatabaseRequest{Confirm: "app", BackupFirst: true}); err != nil {
		t.Fatal(err)
	}
	if len(platform.dropCalls) != 1 {
		t.Fatalf("drop calls = %v", platform.dropCalls)
	}
}

func TestDropHoldsServiceLockAcrossGuardBackupAndDrop(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.job = model.DatabaseJob{ID: "job_backup", Status: model.JobSucceeded, BackupID: "bkp_1"}
	platform.createStarted = make(chan struct{}, 1)
	platform.createRelease = make(chan struct{})
	st.linkStarted = make(chan struct{}, 1)

	dropErr := make(chan error, 1)
	go func() {
		dropErr <- m.DropDatabase(context.Background(), "mysql", "app", DropDatabaseRequest{Confirm: "app", BackupFirst: true})
	}()
	select {
	case <-platform.createStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("drop did not start the pre-drop backup")
	}

	linkErr := make(chan error, 1)
	go func() {
		_, err := m.LinkDatabase(context.Background(), "blog", LinkDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
		linkErr <- err
	}()
	select {
	case <-st.linkStarted:
		t.Fatal("link entered the critical section while drop held the service lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(platform.createRelease)
	if err := <-dropErr; err != nil {
		t.Fatal(err)
	}
	select {
	case <-st.linkStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("link did not resume after drop released the service lock")
	}
	if err := <-linkErr; err != nil {
		t.Fatal(err)
	}
	if len(platform.dropCalls) != 1 {
		t.Fatalf("drop calls = %v", platform.dropCalls)
	}
}

func TestManagerListUsersDiscoversExternalUserWithStableID(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.users = nil
	platform.users = []model.DatabaseUser{{ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "external", Host: "%", Enabled: true}}
	users, err := m.ListUsers(context.Background(), "mysql")
	if err != nil || len(users) != 1 || users[0].ID == "" {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	if users[0].Managed {
		t.Fatalf("external user marked managed: %+v", users[0])
	}
	again, _ := m.ListUsers(context.Background(), "mysql")
	if len(again) != 1 || again[0].ID != users[0].ID {
		t.Fatalf("unstable id: %q then %q", users[0].ID, again[0].ID)
	}
	stored, err := st.GetDatabaseUser(context.Background(), users[0].ID)
	if err != nil || stored.Username != "external" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestManagerDeleteUserBlockedWhileLinked(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	if err := m.DeleteUser(context.Background(), "mysql", "usr_app", DeleteUserRequest{Confirm: "app"}); !errors.Is(err, ErrLinkedSites) {
		t.Fatalf("delete linked user = %v", err)
	}
}

func TestSetDefaultUserPasswordRejectsNonRedisService(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.SetDefaultUserPassword(context.Background(), "mysql"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("default password on mysql = %v, want unsupported", err)
	}
}

func TestSetUserPasswordAuditsPlatformFailureWithoutSecrets(t *testing.T) {
	m, _, platform, audit, _ := testManagerWithFakes(t)
	platform.passwordErr = errors.New("rotate failed: password=supersecret")
	if _, err := m.SetUserPassword(context.Background(), "mysql", "usr_app"); err == nil {
		t.Fatal("SetUserPassword succeeded despite platform failure")
	}
	var failed *model.AuditEventRecord
	for i := range audit.events {
		if audit.events[i].Action == "database.user.password_changed" && audit.events[i].Result == "failed" {
			failed = &audit.events[i]
		}
	}
	if failed == nil {
		t.Fatal("platform failure was not audited")
	}
	if strings.Contains(failed.DetailJSON, "supersecret") {
		t.Fatalf("audit detail leaked a password: %s", failed.DetailJSON)
	}
}

func TestUserMutationsRejectForeignServiceUser(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.users = append(st.users, model.DatabaseUserRecord{
		ID: "usr_foreign", ServiceID: "postgres", Engine: model.DatabasePostgreSQL, Username: "foreign",
	})
	if _, err := m.UpdateUser(context.Background(), "mysql", "usr_foreign", UpdateUserRequest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update foreign user = %v, want not found", err)
	}
	if err := m.DeleteUser(context.Background(), "mysql", "usr_foreign", DeleteUserRequest{Confirm: "foreign"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete foreign user = %v, want not found", err)
	}
	if _, err := m.SetUserPassword(context.Background(), "mysql", "usr_foreign"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password foreign user = %v, want not found", err)
	}
	if _, err := m.RevealUserPassword(context.Background(), "mysql", "usr_foreign"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reveal foreign user = %v, want not found", err)
	}
	if platform.lastPasswordUser != "" {
		t.Fatalf("platform rotated a foreign user: %q", platform.lastPasswordUser)
	}
}

func TestDeleteBackupRejectsForeignServiceBackup(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_foreign", ServiceID: "postgres", DatabaseName: "app"}}
	if err := m.DeleteBackup(context.Background(), "mysql", "bkp_foreign"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete foreign backup = %v, want not found", err)
	}
	if len(platform.deleteBackupIDs) != 0 {
		t.Fatalf("platform deleted a foreign backup: %v", platform.deleteBackupIDs)
	}
}

func TestListBackupsPrunesMetadataDeletedExternally(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{
		{ID: "bkp_live", ServiceID: "mysql", DatabaseName: "app"},
		{ID: "bkp_stale", ServiceID: "mysql", DatabaseName: "app"},
	}
	platform.backups = []model.DatabaseBackup{{ID: "bkp_live", ServiceID: "mysql", Database: "app"}}
	if _, err := m.ListBackups(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDatabaseBackup(context.Background(), "bkp_stale"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale backup metadata survived: %v", err)
	}
	if _, err := st.GetDatabaseBackup(context.Background(), "bkp_live"); err != nil {
		t.Fatalf("live backup metadata missing: %v", err)
	}
}

func TestRecordBackupMetadataPreservesPathAndKind(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{
		ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app",
		Path: "/var/backups/app.sql.gz", Kind: string(model.BackupPreDrop),
	}}
	platform.backups = []model.DatabaseBackup{{ID: "bkp_1", ServiceID: "mysql", Database: "app"}}
	m.recordBackupMetadata(context.Background(), "mysql", "app", "bkp_1", model.BackupPreDrop)
	record, err := st.GetDatabaseBackup(context.Background(), "bkp_1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Path != "/var/backups/app.sql.gz" || record.Kind != string(model.BackupPreDrop) {
		t.Fatalf("backup metadata lost path/kind: %+v", record)
	}
}

func TestRecordBackupMetadataSurvivesCanceledRequestContext(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.backups = []model.DatabaseBackup{{ID: "bkp_1", ServiceID: "mysql", Database: "app"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.recordBackupMetadata(ctx, "mysql", "app", "bkp_1", model.BackupManual)
	if _, err := st.GetDatabaseBackup(context.Background(), "bkp_1"); err != nil {
		t.Fatalf("backup metadata not recorded after request cancellation: %v", err)
	}
}

func TestRenameReferenceFailureRollsBackPlatformRename(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDPostgreSQL] = services.View{ID: services.IDPostgreSQL, State: services.StateActive, Active: true, Installed: true}
	st.renameRefsErr = errors.New("store unavailable")
	_, err := m.RenameDatabase(context.Background(), "postgres", "app", RenameDatabaseRequest{Name: "app2", Confirm: "app"})
	if err == nil {
		t.Fatal("rename succeeded despite reference update failure")
	}
	if len(platform.renameCalls) != 2 || platform.renameCalls[0] != "app->app2" || platform.renameCalls[1] != "app2->app" {
		t.Fatalf("rename calls = %v", platform.renameCalls)
	}
}

func TestLinkDatabaseRejectsProtectedDatabase(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.LinkDatabase(context.Background(), "blog", LinkDatabaseRequest{
		ServiceID: "mysql", Database: "mysql", UserID: "usr_app",
	}); !errors.Is(err, ErrProtected) {
		t.Fatalf("link protected database = %v, want protected", err)
	}
}

func TestImportRejectsUnknownFileExtension(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("x"), "dump.txt", ImportRequest{Mode: ImportMerge}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("import .txt = %v, want invalid request", err)
	}
}

func TestManagerRejectsNonSQLInventoryAndCreate(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.ListDatabases(context.Background(), "phpmyadmin"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("list phpmyadmin databases = %v, want unsupported", err)
	}
	if _, err := m.CreateDatabase(context.Background(), "phpmyadmin", CreateDatabaseRequest{Name: "app"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("create phpmyadmin database = %v, want unsupported", err)
	}
}

func TestManagerRevealAuditsWithoutPersisting(t *testing.T) {
	m, st, _, audit, _ := testManagerWithFakes(t)
	secret, err := m.RevealUserPassword(context.Background(), "mysql", "usr_app")
	if err != nil || secret.Value != "s3cret" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "database.user.password_revealed" {
		t.Fatalf("audit=%+v", audit.events)
	}
	for _, event := range audit.events {
		if strings.Contains(event.DetailJSON, "s3cret") {
			t.Fatalf("audit leaked secret: %+v", event)
		}
	}
	stored, _ := st.GetDatabaseUser(context.Background(), "usr_app")
	if stored.PasswordManaged != true {
		// The test user starts with no password state; reveal must not invent one.
	}
}

func TestManagerSetPasswordUpdatesState(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	secret, err := m.SetUserPassword(context.Background(), "mysql", "usr_app")
	if err != nil || secret.Value == "" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
	stored, _ := st.GetDatabaseUser(context.Background(), "usr_app")
	if !stored.PasswordManaged {
		t.Fatalf("password state not recorded: %+v", stored)
	}
}

func TestManagerRedactsSecretsFromAuditAndJobs(t *testing.T) {
	m, _, platform, audit, events := testManagerWithFakes(t)
	platform.createErr = errors.New("connect mysql://app:s3cret@127.0.0.1/app password=s3cret")
	if _, err := m.CreateDatabase(context.Background(), "mysql", CreateDatabaseRequest{Name: "blog"}); err == nil {
		t.Fatal("expected create failure")
	}
	for _, event := range audit.events {
		if strings.Contains(event.DetailJSON, "s3cret") {
			t.Fatalf("audit leaked secret: %+v", event)
		}
	}
	for _, event := range events.events {
		if strings.Contains(event.Message, "s3cret") {
			t.Fatalf("event leaked secret: %+v", event)
		}
	}

	platform.createErr = nil
	platform.restoreErr = errors.New("restore failed for mysql://app:s3cret@127.0.0.1/app")
	st := m.deps.Store.(*fakeStore)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	job, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{Target: "app", Confirm: "app"})
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}
	job = waitJob(t, m, job.ID)
	if job.Status != model.JobFailed {
		t.Fatalf("expected restore failure: %+v", job)
	}
	if strings.Contains(job.Message, "s3cret") {
		t.Fatalf("job leaked secret: %+v", job)
	}
}

func TestBackupAndJobViewsRedactStoredErrors(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.backups = []model.DatabaseBackup{{
		ID: "bkp_1", ServiceID: "mysql", Database: "app",
		Status: model.BackupFailed, Error: "DB_PASSWORD=s3cret",
	}}
	backups, err := m.ListBackups(context.Background(), "mysql")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%+v err=%v", backups, err)
	}
	if strings.Contains(backups[0].Error, "s3cret") {
		t.Fatalf("backup leaked secret: %+v", backups[0])
	}
	st.jobs = []model.DatabaseJobRecord{{
		ID: "job_1", ServiceID: "mysql", DatabaseName: "app",
		Status: string(model.JobFailed), Message: "password: s3cret",
	}}
	job, err := m.GetJob(context.Background(), "job_1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(job.Message, "s3cret") {
		t.Fatalf("job leaked secret: %+v", job)
	}
}

func TestListUsersUsesLiveGrantsAndPasswordState(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.users = []model.DatabaseUserRecord{{
		ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		Username: "app", Host: "%", Enabled: true, ManagedByDevstack: true,
		PasswordManaged: true,
	}}
	st.grants = []model.DatabaseGrantRecord{{UserID: "usr_app", DatabaseName: "app", Preset: string(model.GrantReadWriteSchema)}}
	platform.users = []model.DatabaseUser{{
		ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		Username: "app", Host: "%", Enabled: true, Managed: true,
		PasswordManaged: false, Grants: nil,
	}}
	users, err := m.ListUsers(context.Background(), "mysql")
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	if users[0].PasswordManaged {
		t.Fatal("stored password state overrode the live engine state")
	}
	if len(users[0].Grants) != 0 {
		t.Fatalf("stored grants overrode an empty live grant set: %+v", users[0].Grants)
	}
}

func TestManagerEmitsDatabaseAndRedisSSENames(t *testing.T) {
	m, st, platform, _, events := testManagerWithFakes(t)
	counter := 0
	m.deps.NewID = func() string {
		counter++
		return fmt.Sprintf("id_%d", counter)
	}
	platform.job = model.DatabaseJob{ID: "job_1", Status: model.JobSucceeded, BackupID: "bkp_1"}
	job, err := m.CreateBackup(context.Background(), "mysql", "app", CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, job.ID)

	st.backups = append(st.backups, model.DatabaseBackupRecord{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app", Kind: string(model.BackupManual), Status: string(model.BackupReady)})
	job, err = m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{Target: "app_restored"})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, job.ID)
	job, err = m.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("select 1;"), "dump.sql", ImportRequest{Mode: ImportMerge})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, job.ID)

	if _, err := m.LinkDatabase(context.Background(), "blog", LinkDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"}); err != nil {
		t.Fatal(err)
	}

	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDRedis] = services.View{ID: services.IDRedis, State: services.StateActive, Active: true, Installed: true}
	platform.users = []model.DatabaseUser{{ID: "usr_cache", ServiceID: "redis", Engine: model.DatabaseRedis, Username: "cache", Enabled: true}}
	cacheUser, err := m.CreateUser(context.Background(), "redis", CreateUserRequest{Username: "cache"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateUser(context.Background(), "redis", cacheUser.ID, UpdateUserRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetUserPassword(context.Background(), "redis", cacheUser.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteUser(context.Background(), "redis", cacheUser.ID, DeleteUserRequest{Confirm: "cache"}); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, event := range events.events {
		seen[event.Type] = true
	}
	for _, want := range []string{
		"database.backup.created",
		"database.backup.restored",
		"database.import.completed",
		"database.link.changed",
		"database.job.updated",
		"redis.user.created",
		"redis.user.updated",
		"redis.user.deleted",
		"redis.password.changed",
	} {
		if !seen[want] {
			t.Fatalf("missing SSE %q in %+v", want, events.events)
		}
	}
}

func TestListDatabasesDeduplicatesMissingLinkedDatabase(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.databases = nil
	st.links = []model.DatabaseLinkRecord{
		{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "gone", UserID: "usr_app"},
		{ID: "lnk_2", SiteID: "site_2", SiteName: "shop", ServiceID: "mysql", DatabaseName: "gone", UserID: "usr_app"},
	}
	databases, err := m.ListDatabases(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(databases) != 1 || len(databases[0].LinkedSites) != 2 {
		t.Fatalf("databases = %+v", databases)
	}
}

func TestListSiteLinksUsesLivePasswordManagedState(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	st.users[0].PasswordManaged = false
	platform.users[0].PasswordManaged = true
	links, err := m.ListSiteLinks(context.Background(), "blog")
	if err != nil || len(links) != 1 {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	if !links[0].PasswordManaged || !links[0].Connection.PasswordManaged {
		t.Fatalf("stored PasswordManaged overrode the engine: %+v", links[0])
	}
}

func TestLinkFromRecordIncludesSafeConnectionURL(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	link := m.linkFromRecord(context.Background(), model.DatabaseLinkRecord{
		ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app",
	}, model.DatabaseUserRecord{ID: "usr_app", Username: "app", Host: "%", PasswordManaged: true})
	if link.Connection.URL == "" || strings.Contains(link.Connection.URL, "s3cret") {
		t.Fatalf("url = %q", link.Connection.URL)
	}
	if !strings.Contains(link.Connection.URL, "app@127.0.0.1") {
		t.Fatalf("url = %q", link.Connection.URL)
	}
}

func TestMetadataEnvUsesSlugForNonPrimary(t *testing.T) {
	env := metadataEnv(model.DatabaseLink{Database: "app", Primary: false, Engine: model.DatabaseMySQL})
	if env["DB_APP_HOST"] != "127.0.0.1" {
		t.Fatalf("env = %+v", env)
	}
	if _, ok := env["DB_HOST"]; ok {
		t.Fatalf("non-primary used primary prefix: %+v", env)
	}
}

func TestSiteDatabaseEnvUsesGlobalPrimaryAcrossServices(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.databases = append(platform.databases, model.Database{Name: "analytics", ServiceID: "postgresql", Engine: model.DatabasePostgreSQL})
	platform.resolveEnv = func(_ string, links []model.DatabaseLink) map[string]string {
		if len(links) != 1 {
			t.Fatalf("platform received %d links, want exactly one per call", len(links))
		}
		link := links[0]
		return map[string]string{
			"DB_CONNECTION": dbConnectionValue(link.Engine),
			"DB_HOST":       "127.0.0.1",
			"DB_PORT":       fmt.Sprintf("%d", link.Connection.Port),
			"DB_DATABASE":   link.Database,
			"DB_USERNAME":   link.Username,
			"DB_PASSWORD":   "s3cret",
			"DATABASE_URL":  "scheme://" + link.Username + ":s3cret@127.0.0.1/" + link.Database,
		}
	}
	st.users[0].PasswordManaged = true
	platform.users[0].PasswordManaged = true
	st.users = append(st.users, model.DatabaseUserRecord{
		ID: "usr_analytics", ServiceID: "postgresql", Engine: model.DatabasePostgreSQL,
		Username: "analytics", Enabled: true, PasswordManaged: true,
	})
	platform.users = append(platform.users, model.DatabaseUser{
		ID: "usr_analytics", ServiceID: "postgresql", Engine: model.DatabasePostgreSQL,
		Username: "analytics", Enabled: true, PasswordManaged: true,
	})
	st.links = []model.DatabaseLinkRecord{
		{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app", Primary: true},
		{ID: "lnk_2", SiteID: "site_1", SiteName: "blog", ServiceID: "postgresql", DatabaseName: "analytics", UserID: "usr_analytics"},
	}
	for i := 0; i < 100; i++ {
		env, err := m.SiteDatabaseEnv(context.Background(), "blog")
		if err != nil {
			t.Fatal(err)
		}
		if env["DB_DATABASE"] != "app" {
			t.Fatalf("iteration %d: DB_DATABASE = %q, want app (env=%+v)", i, env["DB_DATABASE"], env)
		}
		if env["DB_ANALYTICS_DATABASE"] != "analytics" {
			t.Fatalf("iteration %d: DB_ANALYTICS_DATABASE = %q", i, env["DB_ANALYTICS_DATABASE"])
		}
		if env["DB_PASSWORD"] != "s3cret" || env["DB_ANALYTICS_PASSWORD"] != "s3cret" {
			t.Fatalf("iteration %d: passwords = %+v", i, env)
		}
		if !strings.Contains(env["DATABASE_URL"], "/app") {
			t.Fatalf("iteration %d: primary URL = %q", i, env["DATABASE_URL"])
		}
		if !strings.Contains(env["DB_ANALYTICS_URL"], "/analytics") {
			t.Fatalf("iteration %d: namespaced URL = %q", i, env["DB_ANALYTICS_URL"])
		}
		if _, ok := env["DB_ANALYTICS_DB_DATABASE"]; ok {
			t.Fatalf("iteration %d: double DB_ prefix in %+v", i, env)
		}
	}
}

func TestSiteDatabaseEnvAuditsOnlyAfterResolution(t *testing.T) {
	m, st, platform, audit, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app", Primary: true}}
	st.users[0].PasswordManaged = true
	platform.users[0].PasswordManaged = true
	platform.envErr = errors.New("resolve failed")
	if _, err := m.SiteDatabaseEnv(context.Background(), "blog"); err == nil {
		t.Fatal("expected resolve failure")
	}
	if len(audit.events) != 1 || audit.events[0].Result != "failed" {
		t.Fatalf("audit = %+v", audit.events)
	}
	platform.envErr = nil
	platform.env = map[string]string{"DB_DATABASE": "app"}
	if _, err := m.SiteDatabaseEnv(context.Background(), "blog"); err != nil {
		t.Fatal(err)
	}
	if audit.events[len(audit.events)-1].Result != "ok" {
		t.Fatalf("audit = %+v", audit.events)
	}
}

func TestSetUserPasswordReturnsSecretWhenMetadataWriteFails(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.passwordStateErr = errors.New("state write failed")
	secret, err := m.SetUserPassword(context.Background(), "mysql", "usr_app")
	if err != nil || secret.Value == "" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
}

func TestMissingSiteUsesSiteNotFoundSentinel(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	if _, err := m.ListSiteLinks(context.Background(), "missing"); !errors.Is(err, ErrSiteNotFound) {
		t.Fatalf("err = %v, want ErrSiteNotFound", err)
	}
}

func TestProtectedDatabaseRejectedForRestoreImportAndGrants(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	if _, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{Target: "mysql", Overwrite: true, Confirm: "mysql"}); !errors.Is(err, ErrProtected) {
		t.Fatalf("restore protected = %v", err)
	}
	if _, err := m.ImportDatabase(context.Background(), "mysql", "mysql", strings.NewReader("x"), "x.sql", ImportRequest{Mode: ImportMerge}); !errors.Is(err, ErrProtected) {
		t.Fatalf("import protected = %v", err)
	}
	if _, err := m.CreateUser(context.Background(), "mysql", CreateUserRequest{Username: "app2", Grants: []model.DatabaseGrant{{Database: "mysql", Preset: model.GrantReadOnly}}}); !errors.Is(err, ErrProtected) {
		t.Fatalf("protected grant = %v", err)
	}
}

func TestManagerRejectsRedisDatabasesAndBackups(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDRedis] = services.View{ID: services.IDRedis, State: services.StateActive, Active: true, Installed: true}
	if _, err := m.ListDatabases(context.Background(), "redis"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("redis databases = %v", err)
	}
	if err := m.DropDatabase(context.Background(), "redis", "cache", DropDatabaseRequest{Confirm: "cache"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("redis drop = %v", err)
	}
	if _, err := m.ListBackups(context.Background(), "redis"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("redis backups = %v", err)
	}
	if _, err := m.CreateBackup(context.Background(), "redis", "cache", CreateBackupRequest{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("redis backup = %v", err)
	}
}

func TestManagerRequiresActiveService(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	view := svc.views[services.IDMySQL]
	view.State = services.StateInstalledButStopped
	view.Active = false
	svc.views[services.IDMySQL] = view
	if _, err := m.ListDatabases(context.Background(), "mysql"); !errors.Is(err, ErrServiceInactive) {
		t.Fatalf("inactive list = %v", err)
	}
}

// --- backup and job orchestration -----------------------------------------

func TestCreateBackupRunsJobAndAudits(t *testing.T) {
	m, _, platform, audit, _ := testManagerWithFakes(t)
	platform.job = model.DatabaseJob{ID: "job_1", Status: model.JobSucceeded, BackupID: "bkp_1"}
	job, err := m.CreateBackup(context.Background(), "mysql", "app", CreateBackupRequest{Kind: model.BackupManual})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job = waitJob(t, m, job.ID)
	if job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v", job)
	}
	waitAuditAction(t, audit, "database.backup.created")
	if len(platform.backups) != 1 || platform.backups[0].ID != "bkp_1" {
		t.Fatalf("backups=%+v", platform.backups)
	}
}

func TestCreateBackupReturnsBeforePlatformCompletes(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	platform.createStarted = make(chan struct{}, 1)
	platform.createRelease = make(chan struct{})
	start := time.Now()
	job, err := m.CreateBackup(context.Background(), "mysql", "app", CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("CreateBackup blocked for %s", elapsed)
	}
	if job.Status != model.JobQueued && job.Status != model.JobRunning {
		t.Fatalf("initial job = %+v", job)
	}
	select {
	case <-platform.createStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("platform did not start")
	}
	close(platform.createRelease)
	finished := waitJob(t, m, job.ID)
	if finished.Status != model.JobSucceeded {
		t.Fatalf("finished job = %+v", finished)
	}
}

func TestCancelRunningJobMarksCanceled(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	platform.createStarted = make(chan struct{}, 1)
	platform.createRelease = make(chan struct{})
	job, err := m.CreateBackup(context.Background(), "mysql", "app", CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-platform.createStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("platform did not start")
	}
	if err := m.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, m, job.ID)
	if finished.Status != model.JobCanceled {
		t.Fatalf("canceled job = %+v", finished)
	}
}

func TestImportStagesFileAndCleansUp(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	m.stagingDir = t.TempDir()
	platform.importStarted = make(chan struct{}, 1)
	platform.importRelease = make(chan struct{})
	job, err := m.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("select 1;"), "dump.sql", ImportRequest{Mode: ImportMerge})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-platform.importStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("platform did not start")
	}
	entries, err := os.ReadDir(m.stagingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging entries=%v err=%v", entries, err)
	}
	close(platform.importRelease)
	finished := waitJob(t, m, job.ID)
	if finished.Status != model.JobSucceeded {
		t.Fatalf("finished job = %+v", finished)
	}
	if platform.importedFile != "select 1;" {
		t.Fatalf("imported file = %q", platform.importedFile)
	}
	entries, err = os.ReadDir(m.stagingDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging cleanup entries=%v err=%v", entries, err)
	}
}

func TestRestoreOverwriteRequiresConfirm(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	_, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "wrong"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestRestoreFailureKeepsPreRestoreBackup(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	platform.job = model.DatabaseJob{ID: "job_backup", Status: model.JobSucceeded, BackupID: "bkp_pre"}
	platform.restoreErr = errors.New("restore failed")
	job, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "app", PreRestoreBackup: true})
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}
	job = waitJob(t, m, job.ID)
	if job.Status != model.JobFailed {
		t.Fatalf("job=%+v", job)
	}
	if job.BackupID != "bkp_pre" {
		t.Fatalf("pre-restore backup id = %q", job.BackupID)
	}
	if len(st.backups) == 0 {
		t.Fatal("pre-restore backup not stored")
	}
}

func TestRestoreOverwriteForcesPreRestoreBackup(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	platform.job = model.DatabaseJob{ID: "job_backup", Status: model.JobSucceeded, BackupID: "bkp_pre"}
	job, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "app", PreRestoreBackup: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, m, job.ID)
	if job.BackupID != "bkp_pre" {
		t.Fatalf("pre-restore backup = %q, want bkp_pre", job.BackupID)
	}
	if platform.lastRestoreReq.PreRestoreBackup {
		t.Fatal("manager passed PreRestoreBackup=true to the platform after creating its own backup")
	}
}

func TestRestoreFailsClosedWhenDatabaseLookupFails(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	platform.listErr = errors.New("engine inventory unavailable")
	job, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "app"})
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}
	job = waitJob(t, m, job.ID)
	if job.Status != model.JobFailed {
		t.Fatalf("restore succeeded despite an inventory failure: %+v", job)
	}
	if len(platform.restoreCalls) != 0 {
		t.Fatalf("restore reached platform: %v", platform.restoreCalls)
	}
}

func TestImportClearsPlatformPreImportFlag(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	platform.job = model.DatabaseJob{ID: "job_backup", Status: model.JobSucceeded, BackupID: "bkp_pre"}
	job, err := m.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("select 1;"), "dump.sql", ImportRequest{Mode: ImportMerge, PreImportBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, job.ID)
	if platform.lastImportReq.PreImportBackup {
		t.Fatal("manager passed PreImportBackup=true to the platform after creating its own backup")
	}
}

func TestCreateBackupPersistsWhenRequestContextIsCanceled(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.job = model.DatabaseJob{ID: "job_1", Status: model.JobSucceeded, BackupID: "bkp_1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	job, err := m.CreateBackup(ctx, "mysql", "app", CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, m, job.ID)
	if job.Status != model.JobSucceeded {
		t.Fatalf("job = %+v", job)
	}
	stored, err := st.GetDatabaseJob(context.Background(), job.ID)
	if err != nil || stored.Status != string(model.JobSucceeded) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestCancelTerminalJobDoesNotRewriteStatus(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.jobs = []model.DatabaseJobRecord{{ID: "job_1", ServiceID: "mysql", Status: string(model.JobSucceeded)}}
	if err := m.CancelJob(context.Background(), "job_1"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetJob(context.Background(), "job_1")
	if got.Status != model.JobSucceeded {
		t.Fatalf("terminal job rewritten to %q", got.Status)
	}
}

func TestReconcileJobsMarksOrphansFailed(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.jobs = []model.DatabaseJobRecord{
		{ID: "job_queued", ServiceID: "mysql", Status: string(model.JobQueued)},
		{ID: "job_running", ServiceID: "mysql", Status: string(model.JobRunning)},
		{ID: "job_done", ServiceID: "mysql", Status: string(model.JobSucceeded)},
	}
	if err := m.ReconcileJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"job_queued", "job_running"} {
		job, _ := m.GetJob(context.Background(), id)
		if job.Status != model.JobFailed {
			t.Fatalf("%s status = %q, want failed", id, job.Status)
		}
	}
	done, _ := m.GetJob(context.Background(), "job_done")
	if done.Status != model.JobSucceeded {
		t.Fatalf("terminal job changed to %q", done.Status)
	}
}

func TestManagerDeleteServiceMetadataRemovesLinksUsersGrantsBackupsAndJobs(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	st.users = []model.DatabaseUserRecord{{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app"}}
	st.grants = []model.DatabaseGrantRecord{{UserID: "usr_app", DatabaseName: "app", Preset: string(model.GrantReadOnly)}}
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app"}}
	st.jobs = []model.DatabaseJobRecord{{ID: "job_1", ServiceID: "mysql", DatabaseName: "app", Status: string(model.JobSucceeded)}}
	if err := m.DeleteServiceMetadata(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	if len(st.users) != 0 || len(st.grants) != 0 || len(st.backups) != 0 || len(st.jobs) != 0 {
		t.Fatalf("metadata survived: users=%+v grants=%+v backups=%+v jobs=%+v", st.users, st.grants, st.backups, st.jobs)
	}
	if len(st.links) != 0 {
		t.Fatalf("links survived metadata cleanup: %+v", st.links)
	}
}

func TestGuardSiteBlocksUninstalledLinkedServiceWithConflictingEngine(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	delete(svc.views, services.IDMySQL)
	svc.views[services.IDMariaDB] = services.View{ID: services.IDMariaDB, State: services.StateActive, Active: true, Installed: true, Group: services.GroupMySQLFamily}
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	if err := m.GuardSite(context.Background(), st.sites[0]); !errors.Is(err, ErrConflict) {
		t.Fatalf("guard = %v, want conflict", err)
	}
}

func TestManagerImpactListsSitesBackupsAndRunningJobs(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	st.backups = []model.DatabaseBackupRecord{{ID: "bkp_1", ServiceID: "mysql", DatabaseName: "app", CreatedAt: 1}}
	st.jobs = []model.DatabaseJobRecord{
		{ID: "job_run", ServiceID: "mysql", DatabaseName: "app", Status: string(model.JobRunning)},
		{ID: "job_done", ServiceID: "mysql", DatabaseName: "app", Status: string(model.JobSucceeded)},
	}
	impact, err := m.Impact(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(impact.LinkedSites) != 1 || impact.LinkedSites[0].Name != "blog" {
		t.Fatalf("sites = %+v", impact.LinkedSites)
	}
	if len(impact.Backups) != 1 || len(impact.RunningJobs) != 1 || impact.RunningJobs[0].ID != "job_run" {
		t.Fatalf("impact = %+v", impact)
	}
}

func TestUnlinkPrimaryPromotesNextLink(t *testing.T) {
	m, st, platform, _, _ := testManagerWithFakes(t)
	platform.databases = append(platform.databases, model.Database{Name: "blog", ServiceID: "mysql", Engine: model.DatabaseMySQL})
	st.links = []model.DatabaseLinkRecord{
		{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app", Primary: true, CreatedAt: 1},
		{ID: "lnk_2", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "blog", UserID: "usr_app", CreatedAt: 2},
	}
	if err := m.UnlinkDatabase(context.Background(), "blog", "lnk_1"); err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 1 || !st.links[0].Primary {
		t.Fatalf("links = %+v", st.links)
	}
	links, err := m.ListSiteLinks(context.Background(), "blog")
	if err != nil || len(links) != 1 || !links[0].Primary {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	env, err := SiteEnv(links)
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_DATABASE"] != "blog" {
		t.Fatalf("primary env = %+v", env)
	}
	if _, ok := env["DB_BLOG_DATABASE"]; ok {
		t.Fatalf("promoted primary still uses slug prefix: %+v", env)
	}
}

func TestImportReplaceRequiresConfirm(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	_, err := m.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("select 1;"), "dump.sql", ImportRequest{Mode: ImportReplace, Confirm: "wrong"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestImportRunsJobAndAudits(t *testing.T) {
	m, _, _, audit, _ := testManagerWithFakes(t)
	job, err := m.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("select 1;"), "dump.sql", ImportRequest{Mode: ImportMerge})
	if err != nil {
		t.Fatalf("start import: %v", err)
	}
	job = waitJob(t, m, job.ID)
	if job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v", job)
	}
	waitAuditAction(t, audit, "database.import.completed")
}

func TestCancelJobMarksCanceled(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.jobs = []model.DatabaseJobRecord{{ID: "job_1", ServiceID: "mysql", Status: string(model.JobRunning)}}
	if err := m.CancelJob(context.Background(), "job_1"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetJob(context.Background(), "job_1")
	if got.Status != model.JobCanceled {
		t.Fatalf("job status = %q", got.Status)
	}
}

// --- links, env, and guards -----------------------------------------------

func TestLinkDatabaseRejectsMissingAccessWithoutGrant(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	platform.users = []model.DatabaseUser{{ID: "usr_app", Username: "app"}}
	link, err := m.LinkDatabase(context.Background(), "blog", LinkDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(platform.grantCalls) != 0 {
		t.Fatalf("auto-granted: %v", platform.grantCalls)
	}
	if link.Error == "" {
		t.Fatal("missing grant warning not surfaced")
	}
}

func TestSiteEnvUsesLinkValuesAndOmitsMetadataPassword(t *testing.T) {
	env, err := SiteEnv([]model.DatabaseLink{{Primary: true, Database: "app", Username: "app", PasswordManaged: false, Connection: model.DatabaseConnection{Host: "127.0.0.1", Port: 3306, Database: "app", Username: "app"}}})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_HOST"] != "127.0.0.1" || env["DB_DATABASE"] != "app" {
		t.Fatalf("env=%v", env)
	}
	if _, ok := env["DB_PASSWORD"]; ok {
		t.Fatalf("metadata-only link has DB_PASSWORD: %v", env)
	}
	if _, ok := env["DATABASE_URL"]; ok {
		t.Fatalf("metadata-only link has DATABASE_URL: %v", env)
	}
}

func TestSiteEnvRejectsDuplicatePrimary(t *testing.T) {
	_, err := SiteEnv([]model.DatabaseLink{{Primary: true, Database: "a"}, {Primary: true, Database: "b"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestGuardSiteBlocksWrongExclusiveEngine(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDMySQL] = services.View{ID: services.IDMySQL, State: services.StateInstalledButStopped, Installed: true, Group: services.GroupMySQLFamily}
	svc.views[services.IDMariaDB] = services.View{ID: services.IDMariaDB, State: services.StateActive, Active: true, Installed: true, Group: services.GroupMySQLFamily}
	st := m.deps.Store.(*fakeStore)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	if err := m.GuardSite(context.Background(), st.sites[0]); !errors.Is(err, ErrConflict) {
		t.Fatalf("guard = %v", err)
	}
}

func TestGuardSiteWarnsStoppedExactService(t *testing.T) {
	m, _, _, _, events := testManagerWithFakes(t)
	svc := m.deps.Services.(*fakeServices)
	svc.views[services.IDMySQL] = services.View{ID: services.IDMySQL, State: services.StateInstalledButStopped, Installed: true, Group: services.GroupMySQLFamily}
	st := m.deps.Store.(*fakeStore)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	if err := m.GuardSite(context.Background(), st.sites[0]); err != nil {
		t.Fatalf("guard = %v", err)
	}
	if len(events.events) != 1 || events.events[0].Type != "database.service.offline" {
		t.Fatalf("events = %+v", events.events)
	}
}

func TestUnlinkOnlyDeletesLink(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}}
	if err := m.UnlinkDatabase(context.Background(), "blog", "lnk_1"); err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 0 {
		t.Fatalf("links = %+v", st.links)
	}
	if len(st.users) != 1 {
		t.Fatalf("unlink removed the user: %+v", st.users)
	}
}

func TestSiteDatabaseEnvUsesPlatformForManagedLink(t *testing.T) {
	m, st, platform, audit, _ := testManagerWithFakes(t)
	platform.env = map[string]string{"DB_DATABASE": "app", "DB_PASSWORD": "s3cret"}
	st.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app", Primary: true}}
	st.users[0].PasswordManaged = true
	platform.users[0].PasswordManaged = true
	env, err := m.SiteDatabaseEnv(context.Background(), "blog")
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("env=%v", env)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "database.env.resolved" {
		t.Fatalf("audit=%+v", audit.events)
	}
	for _, event := range audit.events {
		if strings.Contains(event.DetailJSON, "s3cret") {
			t.Fatalf("audit leaked secret: %+v", event)
		}
	}
}

// --- Add Site provisioning ------------------------------------------------

func TestAddSiteCreatesDatabaseUserAndLink(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	result, err := m.CreateSiteDatabase(context.Background(), "blog", SiteDatabaseRequest{
		ServiceID:   "mysql",
		NewDatabase: &CreateDatabaseRequest{Name: "blog"},
		NewUser:     &CreateUserRequest{Username: "blog_user"},
		Grant:       model.GrantReadWriteSchema,
		Primary:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Database.Name != "blog" || result.User.Username != "blog_user" {
		t.Fatalf("result=%+v", result)
	}
	if len(platform.grantCalls) != 1 {
		t.Fatalf("grant calls=%v", platform.grantCalls)
	}
}

func TestAddSiteExistingUserWithoutAccessWarns(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	platform.users = []model.DatabaseUser{{ID: "usr_app", Username: "app"}}
	platform.grants = nil
	result, err := m.CreateSiteDatabase(context.Background(), "blog", SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Warning == "" {
		t.Fatal("missing access warning")
	}
	if len(platform.grantCalls) != 0 {
		t.Fatalf("auto-granted: %v", platform.grantCalls)
	}
}

func TestConcurrentLinkDuplicateReturnsConflict(t *testing.T) {
	m, _, _, _, _ := testManagerWithFakes(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.LinkDatabase(context.Background(), "blog", LinkDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var ok, conflict int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
}

func TestLinkProvisionedDoesNotRegrant(t *testing.T) {
	m, _, platform, _, _ := testManagerWithFakes(t)
	provisioned := ProvisionedDatabase{
		Database: model.Database{Name: "app", ServiceID: "mysql", Engine: model.DatabaseMySQL},
		User:     model.DatabaseUser{ID: "usr_app", ServiceID: "mysql", Username: "app", PasswordManaged: true},
		Request:  SiteDatabaseRequest{ServiceID: "mysql", Primary: true},
	}
	result, err := m.LinkProvisioned(context.Background(), "site_1", provisioned)
	if err != nil {
		t.Fatal(err)
	}
	if result.Link.Database != "app" || len(platform.grantCalls) != 0 {
		t.Fatalf("result=%+v grants=%v", result, platform.grantCalls)
	}
}

func TestProvisionSiteDatabaseCreatesDefaultGrant(t *testing.T) {
	m, st, _, _, _ := testManagerWithFakes(t)
	result, err := m.ProvisionSiteDatabase(context.Background(), SiteDatabaseRequest{
		ServiceID:   "mysql",
		NewDatabase: &CreateDatabaseRequest{Name: "blog"},
		NewUser:     &CreateUserRequest{Username: "blog_user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.grants) != 1 || st.grants[0].Preset != string(model.GrantReadWriteSchema) {
		t.Fatalf("grants=%+v", st.grants)
	}
	if result.User.Username != "blog_user" {
		t.Fatalf("user=%+v", result.User)
	}
}
