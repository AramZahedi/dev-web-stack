package sites

import (
	"context"
	"errors"
	"testing"

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

func TestBackendSpecMergesDatabaseEnvWithoutMutatingSite(t *testing.T) {
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
	if spec.Env["DB_DATABASE"] != "app" || spec.Env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("spec env = %+v", spec.Env)
	}
	if spec.Env["APP_ENV"] != "local" {
		t.Fatalf("site env lost: %+v", spec.Env)
	}
	if site.Env["DB_DATABASE"] != "old" {
		t.Fatalf("backendSpecFor mutated site.Env: %+v", site.Env)
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
	if _, err := fakes.store.GetSiteByName(context.Background(), "blog"); err != nil {
		t.Fatalf("site row missing: %v", err)
	}
}
