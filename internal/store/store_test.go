package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var ctx = context.Background()

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func TestSiteRoundTripAndUniqueDomain(t *testing.T) {
	s := openTemp(t)
	site := model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Env: map[string]string{"A": "B"}}
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSiteByDomain(ctx, "blog.test")
	if err != nil || got.Env["A"] != "B" {
		t.Fatalf("%+v %v", got, err)
	}
	site.ID, site.Name = "s2", "other"
	if err := s.CreateSite(ctx, site); err == nil {
		t.Fatal("want unique domain error")
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.db")
	s1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSiteCRUD(t *testing.T) {
	s := openTemp(t)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	site := model.Site{
		ID:              "s1",
		Name:            "blog",
		Domain:          "blog.test",
		WWW:             true,
		Backend:         model.BackendWSL,
		HostPath:        "/home/user/blog",
		BackendPath:     "/mnt/c/home/user/blog",
		Kind:            model.SitePHP,
		PHPVersion:      "8.3",
		PHPExtensions:   map[string]bool{"redis": true, "gd": false},
		PHPIni:          map[string]string{"memory_limit": "256M"},
		NodeVersion:     "22",
		Command:         "npm start",
		Env:             map[string]string{"APP_ENV": "local"},
		DocumentRoot:    "public",
		FrontController: true,
		SPAFallback:     true,
		TLS:             true,
		ForceHTTPS:      true,
		DesiredState:    model.DesiredRunning,
		Status:          model.StatusRunning,
		HTTPPort:        41000,
		FastCGIPort:     43000,
		CreatedAt:       created,
		UpdatedAt:       created.Add(time.Minute),
	}
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSiteEqual(t, site, got)

	byName, err := s.GetSiteByName(ctx, site.Name)
	if err != nil {
		t.Fatal(err)
	}
	assertSiteEqual(t, site, byName)

	sites, err := s.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("ListSites len = %d, want 1", len(sites))
	}
	assertSiteEqual(t, site, sites[0])

	updated := got
	updated.Name = "blog2"
	updated.Domain = "blog2.test"
	updated.Status = model.StatusStopped
	updated.DesiredState = model.DesiredStopped
	updated.Env = map[string]string{"APP_ENV": "production"}
	updated.UpdatedAt = created.Add(2 * time.Minute)
	if err := s.UpdateSite(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSiteEqual(t, updated, got)
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	// Updating with unchanged values must still count as a successful update.
	if err := s.UpdateSite(ctx, got); err != nil {
		t.Fatalf("no-op UpdateSite: %v", err)
	}

	if err := s.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSite(ctx, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSite after delete err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteSite(ctx, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteSite err = %v, want ErrNotFound", err)
	}
}

func TestSiteUniqueName(t *testing.T) {
	s := openTemp(t)
	base := model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic}
	if err := s.CreateSite(ctx, base); err != nil {
		t.Fatal(err)
	}
	duplicate := base
	duplicate.ID, duplicate.Domain = "s2", "other.test"
	if err := s.CreateSite(ctx, duplicate); err == nil {
		t.Fatal("want unique name error")
	}
}

func TestSiteNotFound(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetSite(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSite err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSiteByName(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSiteByName err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSiteByDomain(ctx, "missing.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSiteByDomain err = %v, want ErrNotFound", err)
	}
	missing := model.Site{ID: "missing", Name: "missing", Domain: "missing.test"}
	if err := s.UpdateSite(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateSite err = %v, want ErrNotFound", err)
	}
}

func TestDeleteSiteRemovesCert(t *testing.T) {
	s := openTemp(t)
	site := model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, TLS: true}
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	cert := model.CertRecord{
		SiteID:   site.ID,
		Serial:   "01",
		Domains:  []string{"blog.test", "www.blog.test"},
		NotAfter: time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC),
		KeyPath:  "/pki/blog.key",
		CertPath: "/pki/blog.crt",
	}
	if err := s.SaveCert(ctx, cert); err != nil {
		t.Fatal(err)
	}

	other := model.Site{ID: "s2", Name: "shop", Domain: "shop.test", Kind: model.SiteStatic, TLS: true}
	if err := s.CreateSite(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherCert := cert
	otherCert.SiteID = other.ID
	if err := s.SaveCert(ctx, otherCert); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCertBySite(ctx, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCertBySite after DeleteSite err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetCertBySite(ctx, other.ID); err != nil {
		t.Fatalf("other site certificate was deleted: %v", err)
	}
}

func TestRuntimeCRUD(t *testing.T) {
	s := openTemp(t)
	installed := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	runtime := model.RuntimeBuild{
		ID:          "php-8.3",
		Kind:        "php",
		Version:     "8.3.10",
		Backend:     model.BackendLocal,
		Path:        "/runtimes/php-8.3",
		Checksum:    "sha256:abc",
		InstalledAt: installed,
	}
	if err := s.UpsertRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRuntime(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimeEqual(t, runtime, got)

	runtime.Version = "8.3.11"
	runtime.Checksum = "sha256:def"
	if err := s.UpsertRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRuntime(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRuntimeEqual(t, runtime, got)

	list, err := s.ListRuntimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRuntimes len = %d, want 1", len(list))
	}

	if err := s.DeleteRuntime(ctx, runtime.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRuntime(ctx, runtime.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRuntime after delete err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteRuntime(ctx, runtime.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteRuntime err = %v, want ErrNotFound", err)
	}
}

func TestCertCRUD(t *testing.T) {
	s := openTemp(t)
	if err := s.CreateSite(ctx, model.Site{ID: "s1", Name: "blog", Domain: "blog.test"}); err != nil {
		t.Fatal(err)
	}
	cert := model.CertRecord{
		SiteID:   "s1",
		Serial:   "01",
		Domains:  []string{"blog.test"},
		NotAfter: time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC),
		KeyPath:  "/pki/blog.key",
		CertPath: "/pki/blog.crt",
	}
	if err := s.SaveCert(ctx, cert); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCertBySite(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	assertCertEqual(t, cert, got)

	cert.Serial = "02"
	cert.Domains = []string{"blog.test", "www.blog.test"}
	if err := s.SaveCert(ctx, cert); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetCertBySite(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	assertCertEqual(t, cert, got)

	list, err := s.ListCerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("ListCerts len = %d, want 1", len(list))
	}

	if err := s.DeleteCertBySite(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCertBySite(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCertBySite after delete err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteCertBySite(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteCertBySite err = %v, want ErrNotFound", err)
	}
}

func TestCertCanExistWithoutSite(t *testing.T) {
	s := openTemp(t)
	cert := model.CertRecord{SiteID: "missing", Serial: "01"}
	if err := s.SaveCert(ctx, cert); err != nil {
		t.Fatalf("save pre-site/dashboard certificate: %v", err)
	}
	got, err := s.GetCertBySite(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got.Serial != "01" {
		t.Fatalf("serial = %q", got.Serial)
	}
}

func TestSettings(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetSetting(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSetting missing err = %v, want ErrNotFound", err)
	}
	if err := s.SetSetting(ctx, "helper_fingerprint", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "helper_fingerprint", "def"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSetting(ctx, "helper_fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if got != "def" {
		t.Fatalf("GetSetting = %q, want %q", got, "def")
	}
}

func TestEvents(t *testing.T) {
	s := openTemp(t)
	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for i, typ := range []string{"created", "started", "stopped"} {
		event := model.Event{
			SiteID:    "s1",
			Type:      typ,
			Message:   typ + " message",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := s.AddEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("ListEvents len = %d, want 3", len(all))
	}
	if all[0].Type != "stopped" || all[2].Type != "created" {
		t.Fatalf("ListEvents order = %q, %q, %q", all[0].Type, all[1].Type, all[2].Type)
	}
	if all[0].ID == 0 {
		t.Fatal("event ID was not populated")
	}
	if !all[0].CreatedAt.Equal(base.Add(2 * time.Second)) {
		t.Fatalf("CreatedAt = %v", all[0].CreatedAt)
	}

	limited, err := s.ListEvents(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].Type != "stopped" || limited[1].Type != "started" {
		t.Fatalf("ListEvents(2) = %+v", limited)
	}
}

func TestSitePHPFieldsRoundTrip(t *testing.T) {
	s := openTemp(t)
	site := model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SitePHP,
		PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1",
		PHPIniRaw: "memory_limit = 512M\n", PHPConfigHash: "abc123",
		PHPExtensions: map[string]bool{"xdebug": true},
		PHPIni:        map[string]string{"memory_limit": "512M"}}
	if err := s.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSite(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PHPBuild != site.PHPBuild || got.PHPIniRaw != site.PHPIniRaw || got.PHPConfigHash != site.PHPConfigHash {
		t.Fatalf("PHP fields = %+v", got)
	}

	site.PHPBuild = "php@8.4.4-r2"
	site.PHPIniRaw = "memory_limit = 1G\n"
	site.PHPConfigHash = "def456"
	if err := s.UpdateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSite(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PHPBuild != site.PHPBuild || got.PHPIniRaw != site.PHPIniRaw || got.PHPConfigHash != site.PHPConfigHash {
		t.Fatalf("updated PHP fields = %+v", got)
	}
}

func TestPHPDefaultsAndAppliedConfigRoundTrip(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetPHPDefaults(ctx, "8.4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing defaults error = %v", err)
	}
	d := model.PHPDefaults{Version: "8.4", Ini: map[string]string{"memory_limit": "256M"}, Extensions: map[string]bool{"xdebug": false}}
	if err := s.SavePHPDefaults(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPHPDefaults(ctx, "8.4")
	if err != nil || got.Ini["memory_limit"] != "256M" || got.Extensions["xdebug"] != false {
		t.Fatalf("%+v %v", got, err)
	}

	d.Ini["memory_limit"] = "512M"
	d.Extensions["redis"] = true
	if err := s.SavePHPDefaults(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetPHPDefaults(ctx, "8.4")
	if err != nil || got.Ini["memory_limit"] != "512M" || !got.Extensions["redis"] {
		t.Fatalf("updated defaults %+v %v", got, err)
	}

	other := model.PHPDefaults{Version: "8.2", Ini: map[string]string{"memory_limit": "128M"}}
	if err := s.SavePHPDefaults(ctx, other); err != nil {
		t.Fatal(err)
	}
	defaults, err := s.ListPHPDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 2 || defaults[0].Version != "8.2" || defaults[1].Version != "8.4" {
		t.Fatalf("ListPHPDefaults = %+v", defaults)
	}

	if err := s.CreateSite(ctx, model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SitePHP}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSitePHPConfig(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing site config error = %v", err)
	}
	cfg := model.SitePHPConfig{SiteID: "s1", Ini: d.Ini, Extensions: d.Extensions, ConfigHash: "hash", AppliedAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	if err := s.SaveSitePHPConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	gotCfg, err := s.GetSitePHPConfig(ctx, "s1")
	if err != nil || gotCfg.ConfigHash != "hash" || !gotCfg.AppliedAt.Equal(cfg.AppliedAt) {
		t.Fatalf("%+v %v", gotCfg, err)
	}
	if gotCfg.Ini["memory_limit"] != "512M" || !gotCfg.Extensions["redis"] {
		t.Fatalf("site config JSON = %+v", gotCfg)
	}

	cfg.ConfigHash = "hash2"
	cfg.AppliedAt = cfg.AppliedAt.Add(time.Hour)
	if err := s.SaveSitePHPConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	gotCfg, err = s.GetSitePHPConfig(ctx, "s1")
	if err != nil || gotCfg.ConfigHash != "hash2" || !gotCfg.AppliedAt.Equal(cfg.AppliedAt) {
		t.Fatalf("updated site config %+v %v", gotCfg, err)
	}

	if err := s.DeleteSite(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSitePHPConfig(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("site config survived site deletion: %v", err)
	}
}

func TestMigration0003UpgradesExistingDatabase(t *testing.T) {
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
		if m.version > 2 {
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
		`INSERT INTO sites (id, name, domain, kind) VALUES ('s1', 'blog', 'blog.test', 'static')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO site_php_config (site_id, ini_overrides_json, extension_overrides_json, config_hash)
		 VALUES ('s1', '{}', '{}', 'legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open upgraded database: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})

	got, err := s.GetSite(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PHPBuild != "" || got.PHPIniRaw != "" || got.PHPConfigHash != "" {
		t.Fatalf("new columns = %q %q %q, want empty defaults",
			got.PHPBuild, got.PHPIniRaw, got.PHPConfigHash)
	}
	cfg, err := s.GetSitePHPConfig(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigHash != "legacy" {
		t.Fatalf("legacy config hash = %q, want legacy", cfg.ConfigHash)
	}
	if !cfg.AppliedAt.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("legacy applied_at = %v, want Unix epoch default", cfg.AppliedAt)
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("schema version = %d, want 3", version)
	}
}

func TestSchemaAndPragmas(t *testing.T) {
	s := openTemp(t)
	for _, table := range []string{
		"sites", "runtimes", "certs", "settings", "events",
		"php_defaults", "site_php_config", "schema_migrations",
	} {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q: %v", table, err)
		}
	}

	var fk int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}

	var journal string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}

	var busyTimeout int
	if err := s.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("schema version = %d, want 3", version)
	}
}

func assertSiteEqual(t *testing.T, want, got model.Site) {
	t.Helper()
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
	want.CreatedAt, got.CreatedAt = time.Time{}, time.Time{}
	want.UpdatedAt, got.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("site mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func assertRuntimeEqual(t *testing.T, want, got model.RuntimeBuild) {
	t.Helper()
	if !got.InstalledAt.Equal(want.InstalledAt) {
		t.Errorf("InstalledAt = %v, want %v", got.InstalledAt, want.InstalledAt)
	}
	want.InstalledAt, got.InstalledAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("runtime mismatch:\n want %+v\n  got %+v", want, got)
	}
}

func assertCertEqual(t *testing.T, want, got model.CertRecord) {
	t.Helper()
	if !got.NotAfter.Equal(want.NotAfter) {
		t.Errorf("NotAfter = %v, want %v", got.NotAfter, want.NotAfter)
	}
	want.NotAfter, got.NotAfter = time.Time{}, time.Time{}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("cert mismatch:\n want %+v\n  got %+v", want, got)
	}
}
