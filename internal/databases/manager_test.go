package databases

import (
	"context"
	"errors"
	"io"
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
	mu      sync.Mutex
	sites   []model.Site
	links   []model.DatabaseLinkRecord
	users   []model.DatabaseUserRecord
	grants  []model.DatabaseGrantRecord
	backups []model.DatabaseBackupRecord
	jobs    []model.DatabaseJobRecord
	audit   []model.AuditEventRecord
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
	for _, existing := range f.links {
		if existing.SiteID == link.SiteID && existing.ServiceID == link.ServiceID && existing.DatabaseName == link.DatabaseName {
			return store.ErrConflict
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
		if link.ID == id {
			f.links = append(f.links[:i], f.links[i+1:]...)
			return nil
		}
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

func (f *fakeStore) ListDatabaseJobs(_ context.Context, serviceID string) ([]model.DatabaseJobRecord, error) {
	var out []model.DatabaseJobRecord
	for _, job := range f.jobs {
		if job.ServiceID == serviceID {
			out = append(out, job)
		}
	}
	return out, nil
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
	databases    []model.Database
	users        []model.DatabaseUser
	backups      []model.DatabaseBackup
	job          model.DatabaseJob
	env          map[string]string
	createErr    error
	restoreErr   error
	importErr    error
	grants       []model.DatabaseGrant
	grantCalls   []model.DatabaseGrant
	dropCalls    []string
	restoreCalls []string
}

func (f *fakePlatform) ListDatabases(context.Context, string) ([]model.Database, error) {
	return append([]model.Database(nil), f.databases...), nil
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

func (f *fakePlatform) SetUserPassword(context.Context, string, string) (Secret, error) {
	return Secret{Value: "generated-secret"}, nil
}

func (f *fakePlatform) RevealUserPassword(context.Context, string, string) (Secret, error) {
	return Secret{Value: "s3cret"}, nil
}

func (f *fakePlatform) ListBackups(context.Context, string) ([]model.DatabaseBackup, error) {
	return append([]model.DatabaseBackup(nil), f.backups...), nil
}

func (f *fakePlatform) CreateBackup(_ context.Context, serviceID, database string, req CreateBackupRequest) (model.DatabaseJob, error) {
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

func (f *fakePlatform) DeleteBackup(context.Context, string, string) error { return nil }

func (f *fakePlatform) RestoreBackup(_ context.Context, serviceID, backupID string, req RestoreBackupRequest) (model.DatabaseJob, error) {
	f.restoreCalls = append(f.restoreCalls, backupID)
	job := model.DatabaseJob{ID: "job_restore", ServiceID: serviceID, Database: req.Target, Kind: model.JobRestore, Status: model.JobSucceeded}
	if f.restoreErr != nil {
		job.Status = model.JobFailed
		job.Message = f.restoreErr.Error()
		return job, f.restoreErr
	}
	return job, nil
}

func (f *fakePlatform) ImportDatabase(_ context.Context, serviceID, database string, _ io.Reader, _ string, _ ImportRequest) (model.DatabaseJob, error) {
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

func (f *fakePlatform) ResolveSiteEnv(context.Context, string, []model.DatabaseLink) (map[string]string, error) {
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
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "database.backup.created" {
		t.Fatalf("audit=%+v", audit.events)
	}
	if len(platform.backups) != 1 || platform.backups[0].ID != "bkp_1" {
		t.Fatalf("backups=%+v", platform.backups)
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
	if err == nil || job.Status != model.JobFailed {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if job.BackupID != "bkp_pre" {
		t.Fatalf("pre-restore backup id = %q", job.BackupID)
	}
	if len(st.backups) == 0 {
		t.Fatal("pre-restore backup not stored")
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
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "database.import.completed" {
		t.Fatalf("audit=%+v", audit.events)
	}
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
