package store

import (
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func testSite(id, name string) model.Site {
	return model.Site{
		ID:     id,
		Name:   name,
		Domain: name + ".test",
		Kind:   model.SiteStatic,
	}
}

func TestDatabaseLinkRoundTripAndCascade(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	link := model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: user.ID, Primary: true}
	if err := s.CreateDatabaseLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListDatabaseLinksBySite(ctx, site.ID)
	if err != nil || len(got) != 1 || got[0].DatabaseName != "app" {
		t.Fatalf("links=%v err=%v", got, err)
	}
	if got[0].SiteName != "blog" {
		t.Fatalf("site name = %q, want blog", got[0].SiteName)
	}
	if err := s.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListDatabaseLinksBySite(ctx, site.ID)
	if len(got) != 0 {
		t.Fatalf("links survived site delete: %v", got)
	}
	// The database user is service-wide and must survive site deletion.
	if _, err := s.GetDatabaseUser(ctx, user.ID); err != nil {
		t.Fatalf("user did not survive site delete: %v", err)
	}
}

func TestDatabaseLinkPrimaryUniquePerSite(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"usr_app", "usr_ops"} {
		if err := s.UpsertDatabaseUser(ctx, model.DatabaseUserRecord{ID: id, ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: id, Host: "%"}); err != nil {
			t.Fatal(err)
		}
	}
	first := model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app", Primary: true}
	if err := s.CreateDatabaseLink(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := model.DatabaseLinkRecord{ID: "lnk_2", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "blog", UserID: "usr_ops"}
	if err := s.CreateDatabaseLink(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrimaryDatabaseLink(ctx, site.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	links, err := s.ListDatabaseLinksBySite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	primaries := 0
	for _, link := range links {
		if link.Primary {
			primaries++
			if link.ID != second.ID {
				t.Fatalf("primary = %q, want %q", link.ID, second.ID)
			}
		}
	}
	if primaries != 1 {
		t.Fatalf("primary links = %d, want 1", primaries)
	}
}

func TestRenameDatabaseReferences(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "postgresql", Engine: model.DatabasePostgreSQL, Username: "app"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseLink(ctx, model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "postgresql", DatabaseName: "old", UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDatabaseGrant(ctx, model.DatabaseGrantRecord{UserID: user.ID, DatabaseName: "old", Preset: string(model.GrantReadOnly)}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameDatabaseReferences(ctx, "postgresql", "old", "new"); err != nil {
		t.Fatal(err)
	}
	links, _ := s.ListDatabaseLinksBySite(ctx, site.ID)
	if len(links) != 1 || links[0].DatabaseName != "new" {
		t.Fatalf("links = %+v", links)
	}
	grants, _ := s.ListDatabaseGrants(ctx, user.ID)
	if len(grants) != 1 || grants[0].DatabaseName != "new" {
		t.Fatalf("grants = %+v", grants)
	}
}

func TestCreateDatabaseLinkPrimaryIsAtomicAndDuplicatesConflict(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"usr_app", "usr_ops"} {
		if err := s.UpsertDatabaseUser(ctx, model.DatabaseUserRecord{ID: id, ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: id, Host: "%"}); err != nil {
			t.Fatal(err)
		}
	}
	first := model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app", Primary: true}
	if err := s.CreateDatabaseLink(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := model.DatabaseLinkRecord{ID: "lnk_2", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "blog", UserID: "usr_ops", Primary: true}
	if err := s.CreateDatabaseLink(ctx, second); err != nil {
		t.Fatal(err)
	}
	links, _ := s.ListDatabaseLinksBySite(ctx, site.ID)
	primaries := 0
	for _, link := range links {
		if link.Primary {
			primaries++
			if link.ID != second.ID {
				t.Fatalf("primary = %q, want %q", link.ID, second.ID)
			}
		}
	}
	if primaries != 1 {
		t.Fatalf("primary links = %d", primaries)
	}
	duplicate := model.DatabaseLinkRecord{ID: "lnk_3", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: "usr_app"}
	if err := s.CreateDatabaseLink(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate = %v, want ErrConflict", err)
	}
}

func TestDeletePrimaryLinkPromotesNext(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	first := model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: user.ID, Primary: true, CreatedAt: 1}
	second := model.DatabaseLinkRecord{ID: "lnk_2", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "blog", UserID: user.ID, CreatedAt: 2}
	for _, link := range []model.DatabaseLinkRecord{first, second} {
		if err := s.CreateDatabaseLink(ctx, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteDatabaseLink(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	links, _ := s.ListDatabaseLinksBySite(ctx, site.ID)
	if len(links) != 1 || !links[0].Primary || links[0].ID != second.ID {
		t.Fatalf("links = %+v", links)
	}
}

func TestDatabaseUserDeleteBlockedWhileLinked(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	link := model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: user.ID, Primary: true}
	if err := s.CreateDatabaseLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDatabaseUser(ctx, user.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete linked user = %v, want ErrConflict", err)
	}
	if err := s.DeleteDatabaseLink(ctx, link.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDatabaseUser(ctx, user.ID); err != nil {
		t.Fatalf("delete unlinked user: %v", err)
	}
}

func TestDatabaseUserUpsertAndPasswordState(t *testing.T) {
	s := openTemp(t)
	user := model.DatabaseUserRecord{
		ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		Username: "app", Host: "%", ManagedByDevstack: true, Enabled: true,
	}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDatabaseUserPasswordState(ctx, user.ID, true, 1234); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDatabaseUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PasswordManaged || got.LastPasswordRotatedAt != 1234 {
		t.Fatalf("user = %+v", got)
	}
	user.Username = "app2"
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDatabaseUser(ctx, user.ID)
	if err != nil || got.Username != "app2" {
		t.Fatalf("updated user = %+v err=%v", got, err)
	}
	users, err := s.ListDatabaseUsers(ctx, "mysql")
	if err != nil || len(users) != 1 {
		t.Fatalf("users = %+v err=%v", users, err)
	}
}

func TestDatabaseGrantsRoundTrip(t *testing.T) {
	s := openTemp(t)
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	grant := model.DatabaseGrantRecord{UserID: user.ID, DatabaseName: "app", Preset: string(model.GrantReadWriteSchema), CustomJSON: `["SELECT"]`, UpdatedAt: 99}
	if err := s.SaveDatabaseGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	grants, err := s.ListDatabaseGrants(ctx, user.ID)
	if err != nil || len(grants) != 1 || grants[0].Preset != grant.Preset {
		t.Fatalf("grants = %+v err=%v", grants, err)
	}
	if err := s.DeleteDatabaseGrant(ctx, user.ID, "app"); err != nil {
		t.Fatal(err)
	}
	grants, _ = s.ListDatabaseGrants(ctx, user.ID)
	if len(grants) != 0 {
		t.Fatalf("grants survived delete: %+v", grants)
	}
}

func TestDatabaseBackupAndJobRoundTrip(t *testing.T) {
	s := openTemp(t)
	backup := model.DatabaseBackupRecord{
		ID: "bkp_1", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		DatabaseName: "app", Path: "/data/backups/app.sql.gz", SizeBytes: 42,
		SHA256: "abc", Kind: string(model.BackupManual), Status: string(model.BackupReady),
	}
	if err := s.CreateDatabaseBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDatabaseBackup(ctx, backup.ID)
	if err != nil || got.SizeBytes != 42 || got.DatabaseName != "app" {
		t.Fatalf("backup = %+v err=%v", got, err)
	}
	backup.Status = string(model.BackupFailed)
	if err := s.UpdateDatabaseBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDatabaseBackup(ctx, backup.ID)
	if got.Status != string(model.BackupFailed) {
		t.Fatalf("backup status = %q", got.Status)
	}
	list, err := s.ListDatabaseBackups(ctx, "mysql")
	if err != nil || len(list) != 1 {
		t.Fatalf("backups = %+v err=%v", list, err)
	}
	if err := s.DeleteDatabaseBackup(ctx, backup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDatabaseBackup(ctx, backup.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted backup lookup = %v", err)
	}

	job := model.DatabaseJobRecord{ID: "job_1", ServiceID: "mysql", DatabaseName: "app", Kind: string(model.JobBackup), Status: string(model.JobQueued)}
	if err := s.CreateDatabaseJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status = string(model.JobSucceeded)
	job.BackupID = backup.ID
	if err := s.UpdateDatabaseJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	gotJob, err := s.GetDatabaseJob(ctx, job.ID)
	if err != nil || gotJob.Status != string(model.JobSucceeded) || gotJob.BackupID != backup.ID {
		t.Fatalf("job = %+v err=%v", gotJob, err)
	}
	jobs, err := s.ListDatabaseJobs(ctx, "mysql")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v err=%v", jobs, err)
	}
}

func TestDeleteServiceMetadataRemovesLinksUsersBackupsAndJobsKeepsAudit(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDatabaseGrant(ctx, model.DatabaseGrantRecord{UserID: user.ID, DatabaseName: "app", Preset: string(model.GrantReadOnly)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseBackup(ctx, model.DatabaseBackupRecord{ID: "bkp_1", ServiceID: "mysql", Engine: model.DatabaseMySQL, DatabaseName: "app", Kind: string(model.BackupManual), Status: string(model.BackupReady)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseJob(ctx, model.DatabaseJobRecord{ID: "job_1", ServiceID: "mysql", DatabaseName: "app", Kind: string(model.JobBackup), Status: string(model.JobSucceeded)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseLink(ctx, model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuditEvent(ctx, model.AuditEventRecord{Action: "database.created", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteServiceMetadata(ctx, "mysql"); err != nil {
		t.Fatal(err)
	}
	if users, _ := s.ListDatabaseUsers(ctx, "mysql"); len(users) != 0 {
		t.Fatalf("users survived: %+v", users)
	}
	if grants, _ := s.ListDatabaseGrants(ctx, user.ID); len(grants) != 0 {
		t.Fatalf("grants survived: %+v", grants)
	}
	if backups, _ := s.ListDatabaseBackups(ctx, "mysql"); len(backups) != 0 {
		t.Fatalf("backups survived: %+v", backups)
	}
	if jobs, _ := s.ListDatabaseJobs(ctx, "mysql"); len(jobs) != 0 {
		t.Fatalf("jobs survived: %+v", jobs)
	}
	if links, _ := s.ListDatabaseLinksBySite(ctx, site.ID); len(links) != 0 {
		t.Fatalf("links survived metadata cleanup: %+v", links)
	}
	if count, _ := s.CountAuditEvents(ctx); count != 1 {
		t.Fatalf("audit rows = %d, want durable 1", count)
	}
}

func TestDeleteServiceMetadataRollsBackEverythingOnFailure(t *testing.T) {
	s := openTemp(t)
	site := testSite("site_1", "blog")
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
	if err := s.UpsertDatabaseUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDatabaseGrant(ctx, model.DatabaseGrantRecord{UserID: user.ID, DatabaseName: "app", Preset: string(model.GrantReadOnly)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseBackup(ctx, model.DatabaseBackupRecord{ID: "bkp_1", ServiceID: "mysql", Engine: model.DatabaseMySQL, DatabaseName: "app", Kind: string(model.BackupManual), Status: string(model.BackupReady)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseJob(ctx, model.DatabaseJobRecord{ID: "job_1", ServiceID: "mysql", DatabaseName: "app", Kind: string(model.JobBackup), Status: string(model.JobSucceeded)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDatabaseLink(ctx, model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuditEvent(ctx, model.AuditEventRecord{Action: "database.created", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_job_delete BEFORE DELETE ON database_jobs
		BEGIN SELECT RAISE(ABORT, 'job delete blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteServiceMetadata(ctx, "mysql"); err == nil {
		t.Fatal("DeleteServiceMetadata succeeded despite trigger failure")
	}
	if users, _ := s.ListDatabaseUsers(ctx, "mysql"); len(users) != 1 {
		t.Fatalf("users were not rolled back: %+v", users)
	}
	if grants, _ := s.ListDatabaseGrants(ctx, user.ID); len(grants) != 1 {
		t.Fatalf("grants were not rolled back: %+v", grants)
	}
	if backups, _ := s.ListDatabaseBackups(ctx, "mysql"); len(backups) != 1 {
		t.Fatalf("backups were not rolled back: %+v", backups)
	}
	if jobs, _ := s.ListDatabaseJobs(ctx, "mysql"); len(jobs) != 1 {
		t.Fatalf("jobs were not rolled back: %+v", jobs)
	}
	if links, _ := s.ListDatabaseLinksBySite(ctx, site.ID); len(links) != 1 {
		t.Fatalf("links were not rolled back: %+v", links)
	}
	if count, _ := s.CountAuditEvents(ctx); count != 1 {
		t.Fatalf("audit rows = %d, want durable 1", count)
	}
}

func TestAuditEventRetention(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 10005; i++ {
		if err := s.AddAuditEvent(ctx, model.AuditEventRecord{Action: "test", Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CountAuditEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10000 {
		t.Fatalf("audit count=%d", n)
	}
}

func TestAuditEventsNewestFirst(t *testing.T) {
	s := openTemp(t)
	for i, action := range []string{"first", "second", "third"} {
		if err := s.AddAuditEvent(ctx, model.AuditEventRecord{Action: action, Result: "ok", CreatedAt: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListAuditEvents(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Action != "third" || events[1].Action != "second" {
		t.Fatalf("events = %+v", events)
	}
}
