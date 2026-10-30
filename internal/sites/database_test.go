package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type fakeDatabaseProvisioner struct {
	provisioned   databases.ProvisionedDatabase
	provisionErr  error
	linkErr       error
	provisionedAt []string
	linkedAt      []string
}

func (f *fakeDatabaseProvisioner) ProvisionSiteDatabase(_ context.Context, req databases.SiteDatabaseRequest) (databases.ProvisionedDatabase, error) {
	f.provisionedAt = append(f.provisionedAt, req.ServiceID)
	if f.provisionErr != nil {
		return databases.ProvisionedDatabase{}, f.provisionErr
	}
	if f.provisioned.Database.Name == "" {
		f.provisioned = databases.ProvisionedDatabase{
			Database: model.Database{Name: "app", ServiceID: req.ServiceID, Engine: model.DatabaseMySQL},
			User:     model.DatabaseUser{ID: "usr_app", ServiceID: req.ServiceID, Username: "app"},
			Request:  req,
		}
	}
	return f.provisioned, nil
}

func (f *fakeDatabaseProvisioner) LinkProvisioned(_ context.Context, siteID string, _ databases.ProvisionedDatabase) (databases.SiteDatabaseResult, error) {
	f.linkedAt = append(f.linkedAt, siteID)
	if f.linkErr != nil {
		return databases.SiteDatabaseResult{}, f.linkErr
	}
	return databases.SiteDatabaseResult{}, nil
}

func TestBackendSpecKeepsResolvedDatabaseEnvOutOfSiteEnv(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.DatabaseEnv = func(context.Context, model.Site) (map[string]string, error) {
		return map[string]string{"DB_DATABASE": "app", "DB_PASSWORD": "s3cret"}, nil
	}
	site := model.Site{
		ID: "site", Name: "blog", Kind: model.SiteStatic,
		HostPath: fakes.root, BackendPath: fakes.root,
		Env: map[string]string{"DB_DATABASE": "old", "APP_ENV": "local"},
	}
	spec := svc.backendSpecFor(context.Background(), site)
	if spec.RuntimeEnv["DB_DATABASE"] != "app" || spec.RuntimeEnv["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("runtime env = %+v", spec.RuntimeEnv)
	}
	if spec.Env["DB_DATABASE"] != "old" {
		t.Fatalf("resolved database env leaked into spec.Env: %+v", spec.Env)
	}
	if _, ok := spec.Env["DB_PASSWORD"]; ok {
		t.Fatalf("resolved database password leaked into spec.Env: %+v", spec.Env)
	}
	if spec.Env["APP_ENV"] != "local" {
		t.Fatalf("site env lost: %+v", spec.Env)
	}
	if site.Env["DB_DATABASE"] != "old" {
		t.Fatalf("backendSpecFor mutated site.Env: %+v", site.Env)
	}
}

func TestBackendSpecSendsDatabaseLinksWithoutSecrets(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.DatabaseEnv = func(context.Context, model.Site) (map[string]string, error) {
		return map[string]string{"DB_PASSWORD": "s3cret"}, nil
	}
	svc.deps.DatabaseLinks = func(context.Context, model.Site) ([]model.DatabaseLink, error) {
		return []model.DatabaseLink{{
			ID: "lnk_1", ServiceID: "mysql", Engine: model.DatabaseMySQL,
			Database: "app", UserID: "usr_app", Username: "app", Primary: true,
			PasswordManaged: true,
			Connection:      model.DatabaseConnection{Host: "127.0.0.1", Port: 3306},
		}}, nil
	}
	site := model.Site{ID: "site", Name: "blog", Kind: model.SiteNode, HostPath: fakes.root, BackendPath: fakes.root}
	spec := svc.backendSpecFor(context.Background(), site)
	if len(spec.DatabaseLinks) != 1 || spec.DatabaseLinks[0].UserID != "usr_app" {
		t.Fatalf("database links = %+v", spec.DatabaseLinks)
	}
	if spec.DatabaseLinks[0].Port != 3306 || !spec.DatabaseLinks[0].PasswordManaged {
		t.Fatalf("database link = %+v", spec.DatabaseLinks[0])
	}
	if _, ok := spec.Env["DB_PASSWORD"]; ok {
		t.Fatalf("resolved database password leaked into spec.Env: %+v", spec.Env)
	}
}

func TestStartCallsDatabaseGuard(t *testing.T) {
	svc, fakes := newTestService(t)
	site := model.Site{
		ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
		HostPath: fakes.root, BackendPath: fakes.root, DocumentRoot: ".",
		Status: model.StatusStopped, DesiredState: model.DesiredStopped,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	guardErr := errors.New("mysql linked, mariadb active")
	svc.deps.DatabaseGuard = func(context.Context, model.Site) error { return guardErr }
	if err := svc.Start(context.Background(), "blog"); !errors.Is(err, guardErr) {
		t.Fatalf("start error = %v, want guard error", err)
	}
}

func TestStartPersistsNoResolvedDatabasePassword(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.DatabaseEnv = func(context.Context, model.Site) (map[string]string, error) {
		return map[string]string{"DB_DATABASE": "app", "DB_PASSWORD": "s3cret"}, nil
	}
	site := model.Site{
		ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
		HostPath: fakes.root, BackendPath: fakes.root, DocumentRoot: ".",
		Env:    map[string]string{"APP_ENV": "local"},
		Status: model.StatusStopped, DesiredState: model.DesiredStopped,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(context.Background(), "blog"); err != nil {
		t.Fatal(err)
	}
	stored, err := fakes.store.GetSiteByName(context.Background(), "blog")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stored.Env["DB_PASSWORD"]; ok {
		t.Fatalf("resolved database password persisted with the site: %+v", stored.Env)
	}
	if stored.Env["APP_ENV"] != "local" {
		t.Fatalf("site env lost: %+v", stored.Env)
	}
	var prepared *backend.SiteSpec
	for i := range fakes.backend.prepared {
		if fakes.backend.prepared[i].ID == "site" {
			prepared = &fakes.backend.prepared[i]
			break
		}
	}
	if prepared == nil {
		t.Fatal("backend did not receive a spec")
	}
	if prepared.Env["DB_PASSWORD"] != "" {
		t.Fatalf("resolved password entered spec.Env: %+v", prepared.Env)
	}
	if prepared.RuntimeEnv["DB_PASSWORD"] != "s3cret" || prepared.RuntimeEnv["DB_DATABASE"] != "app" {
		t.Fatalf("runtime env = %+v", prepared.RuntimeEnv)
	}
}

func TestCreateProvisionsDatabaseBeforeSiteRowAndLinksAfter(t *testing.T) {
	svc, fakes := newTestService(t)
	provisioner := &fakeDatabaseProvisioner{}
	svc.deps.Database = provisioner
	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static",
		Database: &databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.Name != "blog" {
		t.Fatalf("site = %+v", site)
	}
	if len(provisioner.provisionedAt) != 1 || provisioner.provisionedAt[0] != "mysql" {
		t.Fatalf("provision calls = %v", provisioner.provisionedAt)
	}
	if len(provisioner.linkedAt) != 1 || provisioner.linkedAt[0] != site.ID {
		t.Fatalf("link calls = %v, site id = %q", provisioner.linkedAt, site.ID)
	}
	if _, err := fakes.store.GetSiteByName(context.Background(), "blog"); err != nil {
		t.Fatalf("site row missing: %v", err)
	}
}

func TestCreatePostProvisionFailureWarnsAndKeepsDatabase(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Database = &fakeDatabaseProvisioner{}
	output := filepath.Join(fakes.root, "dist")
	if err := os.WriteFile(output, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static",
		Build:    &model.BuildProfile{NodeVersion: "22", Command: "npm run build", Output: "dist"},
		Database: &databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"},
	})
	if err == nil || !strings.Contains(err.Error(), "were created and kept") {
		t.Fatalf("create error = %v, want provisioning warning", err)
	}
}

func TestCreateDatabaseProvisionFailureLeavesNoSiteRow(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Database = &fakeDatabaseProvisioner{provisionErr: errors.New("provision failed")}
	_, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static",
		Database: &databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"},
	})
	if err == nil {
		t.Fatal("create succeeded despite provisioning failure")
	}
	if _, err := fakes.store.GetSiteByName(context.Background(), "blog"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("site row exists after provisioning failure: %v", err)
	}
}

func TestCreateLinkFailureKeepsSiteAndWarns(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Database = &fakeDatabaseProvisioner{linkErr: errors.New("link failed")}
	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static",
		Database: &databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"},
	})
	if err != nil {
		t.Fatalf("create should keep the site and warn: %v", err)
	}
	if site.Name != "blog" {
		t.Fatalf("site = %+v", site)
	}
	if len(site.Warnings) == 0 || !strings.Contains(strings.Join(site.Warnings, " "), "link failed") {
		t.Fatalf("site warnings = %+v", site.Warnings)
	}
	if _, err := fakes.store.GetSiteByName(context.Background(), "blog"); err != nil {
		t.Fatalf("site row missing: %v", err)
	}
}
