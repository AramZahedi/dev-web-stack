package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestSiteContractsRoundTrip(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)

	site := model.Site{
		ID:                  "s-contract-1",
		Name:                "contract-site",
		Domain:              "contract-site.test",
		Kind:                model.SitePHP,
		Backend:             model.BackendLocal,
		HostPath:            "/data/contract-site",
		BackendPath:         "/data/contract-site",
		DocumentRoot:        "public",
		FrontControllerFile: "bootstrap.php",
		FrontControllerMode: "generated",
		ApacheAllowOverride: "fileinfo",
		SPAFallbackFile:     "app.html",
		Build: &model.BuildProfile{
			NodeVersion: "20",
			Command:     "npm run build",
			Output:      "dist",
			Env:         map[string]string{"NODE_ENV": "production"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatalf("CreateSite failed: %v", err)
	}

	// 1. GetSite
	got, err := s.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("GetSite failed: %v", err)
	}
	if got.FrontControllerFile != "bootstrap.php" {
		t.Errorf("FrontControllerFile = %q, want bootstrap.php", got.FrontControllerFile)
	}
	if got.FrontControllerMode != "generated" {
		t.Errorf("FrontControllerMode = %q, want generated", got.FrontControllerMode)
	}
	if got.ApacheAllowOverride != "fileinfo" {
		t.Errorf("ApacheAllowOverride = %q, want fileinfo", got.ApacheAllowOverride)
	}
	if got.SPAFallbackFile != "app.html" {
		t.Errorf("SPAFallbackFile = %q, want app.html", got.SPAFallbackFile)
	}
	if got.Build == nil || !reflect.DeepEqual(got.Build, site.Build) {
		t.Errorf("Build = %+v, want %+v", got.Build, site.Build)
	}

	// 2. GetSiteByName
	byName, err := s.GetSiteByName(ctx, site.Name)
	if err != nil {
		t.Fatalf("GetSiteByName failed: %v", err)
	}
	if byName.Build == nil || !reflect.DeepEqual(byName.Build, site.Build) {
		t.Errorf("GetSiteByName Build = %+v, want %+v", byName.Build, site.Build)
	}

	// 3. ListSites
	list, err := s.ListSites(ctx)
	if err != nil {
		t.Fatalf("ListSites failed: %v", err)
	}
	var found *model.Site
	for i := range list {
		if list[i].ID == site.ID {
			found = &list[i]
			break
		}
	}
	if found == nil || found.Build == nil || !reflect.DeepEqual(found.Build, site.Build) {
		t.Fatalf("ListSites build profile missing or mismatch: %+v", found)
	}

	// 4. UpdateSite
	site.FrontControllerFile = "index.php"
	site.FrontControllerMode = "htaccess"
	site.ApacheAllowOverride = "none"
	site.SPAFallbackFile = "index.html"
	site.Build = &model.BuildProfile{
		NodeVersion: "22",
		Command:     "npm run build:prod",
		Output:      "build",
		Env:         map[string]string{"KEY": "VAL"},
	}
	if err := s.UpdateSite(ctx, site); err != nil {
		t.Fatalf("UpdateSite failed: %v", err)
	}
	updated, err := s.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("GetSite after update failed: %v", err)
	}
	if updated.FrontControllerFile != "index.php" ||
		updated.FrontControllerMode != "htaccess" ||
		updated.ApacheAllowOverride != "none" ||
		updated.SPAFallbackFile != "index.html" ||
		updated.Build == nil ||
		!reflect.DeepEqual(updated.Build, site.Build) {
		t.Fatalf("updated site contracts mismatch: %+v", updated)
	}
}

func TestSiteContractsDefaultValues(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)

	site := model.Site{
		ID:          "s-contract-def",
		Name:        "contract-def",
		Domain:      "contract-def.test",
		Kind:        model.SitePHP,
		Backend:     model.BackendLocal,
		HostPath:    "/data/contract-def",
		BackendPath: "/data/contract-def",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatalf("CreateSite failed: %v", err)
	}

	got, err := s.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("GetSite failed: %v", err)
	}
	if got.FrontControllerFile != "index.php" {
		t.Errorf("FrontControllerFile = %q, want default index.php", got.FrontControllerFile)
	}
	if got.FrontControllerMode != "auto" {
		t.Errorf("FrontControllerMode = %q, want default auto", got.FrontControllerMode)
	}
	if got.ApacheAllowOverride != "all" {
		t.Errorf("ApacheAllowOverride = %q, want default all", got.ApacheAllowOverride)
	}
	if got.SPAFallbackFile != "index.html" {
		t.Errorf("SPAFallbackFile = %q, want default index.html", got.SPAFallbackFile)
	}
	if got.Build != nil {
		t.Errorf("Build = %+v, want default nil", got.Build)
	}
}

func TestMigration0005UpgradesExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version > 4 {
			continue
		}
		if _, err := db.ExecContext(ctx, m.script); err != nil {
			t.Fatalf("apply migration %04d: %v", m.version, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sites (id, name, domain, kind) VALUES ('s1', 'legacy-site', 'legacy.test', 'php')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open store with migration 5: %v", err)
	}
	defer s.Close()

	site, err := s.GetSite(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSite legacy site: %v", err)
	}
	if site.FrontControllerFile != "index.php" {
		t.Errorf("migrated site FrontControllerFile = %q, want index.php", site.FrontControllerFile)
	}
	if site.FrontControllerMode != "auto" {
		t.Errorf("migrated site FrontControllerMode = %q, want auto", site.FrontControllerMode)
	}
	if site.ApacheAllowOverride != "all" {
		t.Errorf("migrated site ApacheAllowOverride = %q, want all", site.ApacheAllowOverride)
	}
	if site.SPAFallbackFile != "index.html" {
		t.Errorf("migrated site SPAFallbackFile = %q, want index.html", site.SPAFallbackFile)
	}
	if site.Build != nil {
		t.Errorf("migrated site Build = %+v, want nil", site.Build)
	}
}
