package servicecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/serviceconfig"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeConfigLifecycle struct {
	views map[services.ID]services.View
	err   error
}

func (f *fakeConfigLifecycle) Get(_ context.Context, id services.ID) (services.View, error) {
	if f.err != nil {
		return services.View{}, f.err
	}
	if view, ok := f.views[id]; ok {
		return view, nil
	}
	return services.View{ID: id, Installed: true}, nil
}

type fakeConfigValidator struct {
	calls       []string
	validateErr error
	reloadErr   error
}

func (f *fakeConfigValidator) Validate(_ context.Context, id string) error {
	f.calls = append(f.calls, "validate:"+id)
	return f.validateErr
}

func (f *fakeConfigValidator) Reload(_ context.Context, id string) error {
	f.calls = append(f.calls, "reload:"+id)
	return f.reloadErr
}

type fakeConfigEvents struct {
	events []model.Event
	err    error
}

func (f *fakeConfigEvents) AddEvent(_ context.Context, e model.Event) error {
	f.events = append(f.events, e)
	return f.err
}

type configFixture struct {
	t          *testing.T
	root       string
	service    *ConfigService
	lifecycle  *fakeConfigLifecycle
	validator  *fakeConfigValidator
	events     *fakeConfigEvents
	logs       *bytes.Buffer
	catalog    services.Catalog
	serviceIDs []services.ID
}

func newConfigFixture(t *testing.T) *configFixture {
	t.Helper()
	root := t.TempDir()
	catalog := services.DefaultCatalog()
	lifecycle := &fakeConfigLifecycle{views: map[services.ID]services.View{
		services.IDNginx:  {ID: services.IDNginx, Installed: true, Active: false},
		services.IDApache: {ID: services.IDApache, Installed: true, Active: false},
		services.IDMySQL:  {ID: services.IDMySQL, Installed: true, Active: false},
	}}
	validator := &fakeConfigValidator{}
	events := &fakeConfigEvents{}
	logs := &bytes.Buffer{}
	service, err := NewConfigService(ConfigDeps{
		Root:      root,
		Catalog:   catalog,
		Services:  lifecycle,
		Validator: validator,
		Events:    events,
		Logger:    slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &configFixture{
		t:          t,
		root:       root,
		service:    service,
		lifecycle:  lifecycle,
		validator:  validator,
		events:     events,
		logs:       logs,
		catalog:    catalog,
		serviceIDs: []services.ID{services.IDNginx, services.IDApache, services.IDMySQL},
	}
}

func (f *configFixture) install(id services.ID) {
	f.t.Helper()
	for _, sub := range []string{"config", "data", "logs"} {
		if err := os.MkdirAll(filepath.Join(f.root, "services", string(id), sub), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *configFixture) configRoot(id services.ID) string {
	return filepath.Join(f.root, "services", string(id), "config")
}

func (f *configFixture) write(id services.ID, rel, content string) {
	f.t.Helper()
	full := filepath.Join(f.configRoot(id), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *configFixture) read(id services.ID, rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.configRoot(id), filepath.FromSlash(rel)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func (f *configFixture) manager(id services.ID) *serviceconfig.Manager {
	f.t.Helper()
	m, err := serviceconfig.New(f.configRoot(id))
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *configFixture) conflictFile(id services.ID) {
	f.t.Helper()
	m := f.manager(id)
	if _, err := m.WriteGenerated("conf.d/generated/app.conf", []byte("server { listen 80; }\n"), 0o644, "test"); err != nil {
		f.t.Fatal(err)
	}
	f.write(id, "conf.d/generated/app.conf", "server { listen 80; # edited\n}\n")
}

func (f *configFixture) setActive(id services.ID, active bool) {
	view := f.lifecycle.views[id]
	view.ID = id
	view.Installed = true
	view.Active = active
	f.lifecycle.views[id] = view
}

func TestConfigListReturnsMetadataNotContent(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	m := f.manager(services.IDNginx)
	if _, err := m.SeedBase("nginx.conf", []byte("worker_processes 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.WriteGenerated("conf.d/generated/site.conf", []byte("server { listen 80; }\n"), 0o644, "test"); err != nil {
		t.Fatal(err)
	}
	f.write(services.IDNginx, "conf.d/generated/site.conf", "server { listen 8080; }\n")
	f.write(services.IDNginx, "conf.d/custom/extra.conf", "client_max_body_size 4m;\n")
	f.write(services.IDNginx, "conf.d/big.conf", strings.Repeat("x", MaxConfigFileBytes+1))

	listing, err := f.service.List(context.Background(), services.IDNginx)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Service != string(services.IDNginx) {
		t.Fatalf("service = %q", listing.Service)
	}
	if len(listing.Files) != 3 {
		t.Fatalf("files = %d, want 3: %+v", len(listing.Files), listing.Files)
	}
	byPath := map[string]ConfigStatus{}
	for _, file := range listing.Files {
		byPath[file.Path] = file
	}
	if _, ok := byPath["conf.d/big.conf"]; ok {
		t.Fatal("oversized file was listed")
	}
	base, ok := byPath["nginx.conf"]
	if !ok || base.Class != serviceconfig.ClassBase || base.Generated || base.Modified || base.Detached {
		t.Fatalf("base file = %+v", base)
	}
	generated, ok := byPath["conf.d/generated/site.conf"]
	if !ok {
		t.Fatal("generated file missing from listing")
	}
	if generated.Class != serviceconfig.ClassGenerated || !generated.Generated || !generated.Modified || generated.Detached {
		t.Fatalf("generated file = %+v", generated)
	}
	if !generated.Conflict.Active || !generated.Conflict.ResolutionRequired {
		t.Fatalf("generated conflict = %+v", generated.Conflict)
	}
	custom, ok := byPath["conf.d/custom/extra.conf"]
	if !ok || custom.Class != serviceconfig.ClassCustom || custom.Generated {
		t.Fatalf("custom file = %+v", custom)
	}
	encoded, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"worker_processes", "client_max_body_size", "server {"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("listing leaked file content %q: %s", secret, encoded)
		}
	}
}

func TestConfigContentPolicy(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")
	f.write(services.IDNginx, "notes.txt", "safe existing file\n")
	f.write(services.IDNginx, "conf.d/big.conf", strings.Repeat("x", MaxConfigFileBytes+1))
	f.write(services.IDNginx, "conf.d/.hidden", "hidden\n")
	f.write(services.IDNginx, ".env", "DB_PASSWORD=secret\n")
	f.write(services.IDNginx, "secrets/db.conf", "password=secret\n")
	f.write(services.IDNginx, "certs/site.pem", "cert\n")
	f.write(services.IDNginx, "backups/config/old.conf", "old\n")
	if err := os.Symlink(filepath.Join(f.configRoot(services.IDNginx), "nginx.conf"),
		filepath.Join(f.configRoot(services.IDNginx), "conf.d", "link.conf")); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]string{
		"nginx.conf": "worker_processes 1;\n",
		"notes.txt":  "safe existing file\n",
	}
	for rel, want := range allowed {
		content, err := f.service.Content(context.Background(), services.IDNginx, rel)
		if err != nil {
			t.Fatalf("Content(%q): %v", rel, err)
		}
		if content.Content != want {
			t.Fatalf("Content(%q) = %q, want %q", rel, content.Content, want)
		}
		if content.Path != rel {
			t.Fatalf("Content(%q).Path = %q", rel, content.Path)
		}
	}

	denied := []string{
		"../data/x", "/etc/passwd", "..", ".", `conf.d\..\nginx.conf`,
		".env", "secrets/db.conf", "certs/site.pem", "keys/server.key",
		"backups/config/old.conf", ".config-manifest.json", "conf.d/.hidden",
		"conf.d/foo.sock", "conf.d/link.conf", "conf.d",
	}
	for _, rel := range denied {
		if _, err := f.service.Content(context.Background(), services.IDNginx, rel); err == nil {
			t.Errorf("Content(%q) succeeded, want rejection", rel)
		}
	}

	if _, err := f.service.Content(context.Background(), services.IDNginx, "conf.d/big.conf"); !errors.Is(err, ErrConfigTooLarge) {
		t.Fatalf("oversized Content error = %v, want ErrConfigTooLarge", err)
	}
}

func TestConfigListSkipsSymlinksAndInternalState(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")
	m := f.manager(services.IDNginx)
	if _, err := m.SeedBase("nginx.conf", []byte("worker_processes 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.configRoot(services.IDNginx), "backups", "config", "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.configRoot(services.IDNginx), "backups", "config", "old", "nginx.conf"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.configRoot(services.IDNginx), "nginx.conf"),
		filepath.Join(f.configRoot(services.IDNginx), "linked.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(f.configRoot(services.IDNginx), "linked-dir")); err != nil {
		t.Fatal(err)
	}

	listing, err := f.service.List(context.Background(), services.IDNginx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Files) != 1 || listing.Files[0].Path != "nginx.conf" {
		t.Fatalf("listing = %+v, want only nginx.conf", listing.Files)
	}
	if _, err := f.service.Content(context.Background(), services.IDNginx, "linked-dir/nginx.conf"); !errors.Is(err, ErrConfigPathDenied) {
		t.Fatalf("symlinked directory content error = %v, want ErrConfigPathDenied", err)
	}
}

func TestConfigDiffReturnsUnifiedDiffAndConflict(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	m := f.manager(services.IDNginx)
	if _, err := m.SeedBase("nginx.conf", []byte("worker_processes 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.conflictFile(services.IDNginx)

	diff, err := f.service.Diff(context.Background(), services.IDNginx, "conf.d/generated/app.conf",
		"server { listen 8090; }\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Diff, "-server { listen 80; # edited") || !strings.Contains(diff.Diff, "+server { listen 8090; }") {
		t.Fatalf("diff = %q", diff.Diff)
	}
	if !diff.Conflict.Active || !diff.Conflict.ResolutionRequired {
		t.Fatalf("conflict = %+v", diff.Conflict)
	}
	if diff.CurrentHash == "" || diff.ProposedHash == "" || diff.CurrentHash == diff.ProposedHash {
		t.Fatalf("hashes = %q/%q", diff.CurrentHash, diff.ProposedHash)
	}
	if len(diff.Conflict.AllowedResolutions) != 3 {
		t.Fatalf("allowed resolutions = %+v", diff.Conflict.AllowedResolutions)
	}

	baseDiff, err := f.service.Diff(context.Background(), services.IDNginx, "nginx.conf", "worker_processes 2;\n")
	if err != nil {
		t.Fatal(err)
	}
	if baseDiff.Conflict.Active {
		t.Fatalf("base diff conflict = %+v", baseDiff.Conflict)
	}

	if _, err := f.service.Diff(context.Background(), services.IDNginx, "conf.d/generated/app.conf",
		strings.Repeat("x", MaxConfigFileBytes+1)); !errors.Is(err, ErrConfigTooLarge) {
		t.Fatalf("oversized diff error = %v", err)
	}
}

func TestConfigApplyWritesBacksUpAndReloadsInOrder(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.setActive(services.IDNginx, true)
	m := f.manager(services.IDNginx)
	if _, err := m.SeedBase("nginx.conf", []byte("worker_processes 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path:    "nginx.conf",
		Content: "worker_processes 2;\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Live || !result.Reloaded || result.ValidationPending || result.RolledBack {
		t.Fatalf("result = %+v", result)
	}
	if result.BackupPath == "" {
		t.Fatal("result has no backup path")
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "worker_processes 1;\n" {
		t.Fatalf("backup = %q", backup)
	}
	if got := f.read(services.IDNginx, "nginx.conf"); got != "worker_processes 2;\n" {
		t.Fatalf("disk content = %q", got)
	}
	wantCalls := []string{"validate:nginx", "reload:nginx"}
	if strings.Join(f.validator.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("validator calls = %v, want %v", f.validator.calls, wantCalls)
	}
	if len(f.events.events) == 0 {
		t.Fatal("expected an audit event")
	}
	eventJSON, _ := json.Marshal(f.events.events)
	if bytes.Contains(eventJSON, []byte("worker_processes 2")) {
		t.Fatalf("audit event leaked content: %s", eventJSON)
	}
}

func TestConfigApplyConflictResolutions(t *testing.T) {
	t.Run("requires resolution", func(t *testing.T) {
		f := newConfigFixture(t)
		f.install(services.IDNginx)
		f.setActive(services.IDNginx, true)
		f.conflictFile(services.IDNginx)
		before := f.read(services.IDNginx, "conf.d/generated/app.conf")

		_, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
			Path: "conf.d/generated/app.conf", Content: "server { listen 8090; }\n",
		})
		if !errors.Is(err, ErrConfigConflict) {
			t.Fatalf("error = %v, want ErrConfigConflict", err)
		}
		var conflict *ConfigConflictError
		if !errors.As(err, &conflict) || conflict.Diff == "" || conflict.CurrentHash == "" {
			t.Fatalf("conflict error = %#v", conflict)
		}
		if got := f.read(services.IDNginx, "conf.d/generated/app.conf"); got != before {
			t.Fatal("conflict rejection modified the file")
		}
		if len(f.validator.calls) != 0 {
			t.Fatalf("validator calls = %v, want none", f.validator.calls)
		}
	})

	t.Run("invalid resolution", func(t *testing.T) {
		f := newConfigFixture(t)
		f.install(services.IDNginx)
		f.conflictFile(services.IDNginx)
		_, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
			Path: "conf.d/generated/app.conf", Content: "x\n", Resolution: "overwrite",
		})
		if !errors.Is(err, ErrConfigResolutionInvalid) {
			t.Fatalf("error = %v, want ErrConfigResolutionInvalid", err)
		}
	})

	t.Run("keep detaches without writing", func(t *testing.T) {
		f := newConfigFixture(t)
		f.install(services.IDNginx)
		f.conflictFile(services.IDNginx)
		before := f.read(services.IDNginx, "conf.d/generated/app.conf")
		result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
			Path: "conf.d/generated/app.conf", Content: "server { listen 8090; }\n", Resolution: "keep",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Changed || !result.Detached || result.Live {
			t.Fatalf("result = %+v", result)
		}
		if got := f.read(services.IDNginx, "conf.d/generated/app.conf"); got != before {
			t.Fatal("keep wrote the proposed content")
		}
		status, err := f.manager(services.IDNginx).Status("conf.d/generated/app.conf")
		if err != nil {
			t.Fatal(err)
		}
		if !status.Detached {
			t.Fatalf("status = %+v, want detached", status)
		}
	})

	t.Run("use replaces after backup", func(t *testing.T) {
		f := newConfigFixture(t)
		f.install(services.IDNginx)
		f.setActive(services.IDNginx, true)
		f.conflictFile(services.IDNginx)
		before := f.read(services.IDNginx, "conf.d/generated/app.conf")
		result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
			Path: "conf.d/generated/app.conf", Content: "server { listen 8090; }\n", Resolution: "use",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || !result.Live || result.Detached || result.Resolution != "use" {
			t.Fatalf("result = %+v", result)
		}
		backup, err := os.ReadFile(result.BackupPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(backup) != before {
			t.Fatalf("backup = %q, want %q", backup, before)
		}
		if got := f.read(services.IDNginx, "conf.d/generated/app.conf"); got != "server { listen 8090; }\n" {
			t.Fatalf("disk = %q", got)
		}
	})

	t.Run("merge writes merged content and detaches", func(t *testing.T) {
		f := newConfigFixture(t)
		f.install(services.IDNginx)
		f.setActive(services.IDNginx, true)
		f.conflictFile(services.IDNginx)
		result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
			Path: "conf.d/generated/app.conf", Content: "server { listen 80; # merged\n}\n", Resolution: "merge",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || !result.Detached || result.Resolution != "merge" {
			t.Fatalf("result = %+v", result)
		}
		if got := f.read(services.IDNginx, "conf.d/generated/app.conf"); got != "server { listen 80; # merged\n}\n" {
			t.Fatalf("disk = %q", got)
		}
		status, err := f.manager(services.IDNginx).Status("conf.d/generated/app.conf")
		if err != nil {
			t.Fatal(err)
		}
		if !status.Detached {
			t.Fatalf("status = %+v, want detached", status)
		}
	})

	t.Run("resolution without conflict is rejected", func(t *testing.T) {
		f := newConfigFixture(t)
		f.install(services.IDNginx)
		f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")
		_, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
			Path: "nginx.conf", Content: "worker_processes 2;\n", Resolution: "keep",
		})
		if !errors.Is(err, ErrConfigResolutionInvalid) {
			t.Fatalf("error = %v, want ErrConfigResolutionInvalid", err)
		}
	})
}

func TestConfigManifestStaysInsideConfigRoot(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.conflictFile(services.IDNginx)

	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "conf.d/generated/app.conf", Content: "x\n", Resolution: "keep",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.configRoot(services.IDNginx), ".config-manifest.json")); err != nil {
		t.Fatalf("manifest missing from config root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "services", "nginx", ".config-manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest was written outside the config root: %v", err)
	}
}

func TestConfigApplyValidationFailureRollsBack(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.setActive(services.IDNginx, true)
	m := f.manager(services.IDNginx)
	if _, err := m.SeedBase("nginx.conf", []byte("worker_processes 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.validator.validateErr = errors.New("nginx: [emerg] invalid config password=supersecret")

	_, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\n",
	})
	if !errors.Is(err, ErrConfigValidationFailed) {
		t.Fatalf("error = %v, want ErrConfigValidationFailed", err)
	}
	var failure *ConfigApplyFailure
	if !errors.As(err, &failure) || !failure.RolledBack || failure.BackupPath == "" {
		t.Fatalf("failure = %#v", failure)
	}
	if got := f.read(services.IDNginx, "nginx.conf"); got != "worker_processes 1;\n" {
		t.Fatalf("rollback content = %q", got)
	}
	for _, call := range f.validator.calls {
		if strings.HasPrefix(call, "reload:") {
			t.Fatalf("reload was called before successful validation: %v", f.validator.calls)
		}
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("validation error leaked secret: %v", err)
	}
	if strings.Contains(f.logs.String(), "supersecret") {
		t.Fatalf("audit log leaked secret: %s", f.logs.String())
	}
	eventJSON, _ := json.Marshal(f.events.events)
	if bytes.Contains(eventJSON, []byte("supersecret")) {
		t.Fatalf("audit event leaked secret: %s", eventJSON)
	}
}

func TestConfigApplyReloadFailureRollsBack(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.setActive(services.IDNginx, true)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")
	f.validator.reloadErr = errors.New("reload failed")

	_, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\n",
	})
	if !errors.Is(err, ErrConfigReloadFailed) {
		t.Fatalf("error = %v, want ErrConfigReloadFailed", err)
	}
	var failure *ConfigApplyFailure
	if !errors.As(err, &failure) || !failure.RolledBack {
		t.Fatalf("failure = %#v", failure)
	}
	if got := f.read(services.IDNginx, "nginx.conf"); got != "worker_processes 1;\n" {
		t.Fatalf("rollback content = %q", got)
	}
	if len(f.validator.calls) < 2 || !strings.HasPrefix(f.validator.calls[0], "validate:") {
		t.Fatalf("validator calls = %v", f.validator.calls)
	}
}

func TestConfigApplyInactiveMarksValidationPending(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")

	result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Live || !result.ValidationPending || result.Active {
		t.Fatalf("result = %+v", result)
	}
	if len(f.validator.calls) != 0 {
		t.Fatalf("validator calls = %v, want none for an inactive service", f.validator.calls)
	}
}

func TestConfigApplyActiveWithoutValidationCommandMarksPending(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDMySQL)
	f.setActive(services.IDMySQL, true)
	f.write(services.IDMySQL, "devstack.cnf", "[mysqld]\nmax_connections=256\n")

	result, err := f.service.Apply(context.Background(), services.IDMySQL, ConfigApplyRequest{
		Path: "devstack.cnf", Content: "[mysqld]\nmax_connections=512\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Live || !result.ValidationPending || !result.RestartRequired || !result.Active {
		t.Fatalf("result = %+v", result)
	}
	if len(f.validator.calls) != 0 {
		t.Fatalf("validator calls = %v, want none without a catalog validation command", f.validator.calls)
	}
}

func TestConfigApplyRejectsNewArbitraryFile(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)

	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "new.txt", Content: "arbitrary\n",
	}); !errors.Is(err, ErrConfigPathDenied) {
		t.Fatalf("new arbitrary file error = %v, want ErrConfigPathDenied", err)
	}
	if _, err := os.Stat(filepath.Join(f.configRoot(services.IDNginx), "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("arbitrary file was created")
	}
	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf/evil.conf", Content: "x\n",
	}); !errors.Is(err, ErrConfigPathDenied) {
		t.Fatalf("file-mount child error = %v, want ErrConfigPathDenied", err)
	}
	if _, err := os.Stat(filepath.Join(f.configRoot(services.IDNginx), "nginx.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a directory was created for a file mount")
	}

	result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "conf.d/custom/new.conf", Content: "client_max_body_size 4m;\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.ValidationPending != true {
		t.Fatalf("catalog-dir creation result = %+v", result)
	}
}

func TestConfigInstalledGating(t *testing.T) {
	f := newConfigFixture(t)

	if _, err := f.service.List(context.Background(), services.IDNginx); !errors.Is(err, ErrConfigNotInstalled) {
		t.Fatalf("List error = %v, want ErrConfigNotInstalled", err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "services", "nginx", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.List(context.Background(), services.IDNginx); !errors.Is(err, ErrConfigNotInstalled) {
		t.Fatalf("partial scaffold List error = %v, want ErrConfigNotInstalled", err)
	}
	if _, err := f.service.List(context.Background(), services.ID("unknown")); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("unknown service error = %v, want services.ErrNotFound", err)
	}

	f.install(services.IDApache)
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(f.root, "services", "apache", "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.root, "services", "apache", "data")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.List(context.Background(), services.IDApache); !errors.Is(err, ErrConfigNotInstalled) {
		t.Fatalf("symlinked scaffold List error = %v, want ErrConfigNotInstalled", err)
	}
}

func TestConfigApplyTooLargeAndBinaryContent(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")

	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: strings.Repeat("x", MaxConfigFileBytes+1),
	}); !errors.Is(err, ErrConfigTooLarge) {
		t.Fatalf("oversized apply error = %v", err)
	}
	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\x00\n",
	}); !errors.Is(err, ErrConfigBinary) {
		t.Fatalf("binary apply error = %v", err)
	}

	f.write(services.IDNginx, "conf.d/binary.conf", string([]byte{0xff, 0xfe, 0xfd}))
	if _, err := f.service.Content(context.Background(), services.IDNginx, "conf.d/binary.conf"); !errors.Is(err, ErrConfigBinary) {
		t.Fatalf("invalid UTF-8 content error = %v, want ErrConfigBinary", err)
	}
}

func TestConfigResetRestoresLatestBackup(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.setActive(services.IDNginx, true)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")

	first, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.BackupPath == "" {
		t.Fatal("first apply has no backup")
	}
	f.validator.calls = nil

	result, err := f.service.Reset(context.Background(), services.IDNginx, "nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Live || result.RestoredFrom == "" {
		t.Fatalf("reset result = %+v", result)
	}
	if got := f.read(services.IDNginx, "nginx.conf"); got != "worker_processes 1;\n" {
		t.Fatalf("reset content = %q", got)
	}
	if len(f.validator.calls) != 2 {
		t.Fatalf("validator calls = %v", f.validator.calls)
	}
}

func TestConfigResetWithoutBackupPreservesCustomFile(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "conf.d/custom/extra.conf", "client_max_body_size 4m;\n")

	if _, err := f.service.Reset(context.Background(), services.IDNginx, "conf.d/custom/extra.conf"); !errors.Is(err, ErrConfigResetUnavailable) {
		t.Fatalf("reset error = %v, want ErrConfigResetUnavailable", err)
	}
	if got := f.read(services.IDNginx, "conf.d/custom/extra.conf"); got != "client_max_body_size 4m;\n" {
		t.Fatalf("custom file was removed or changed: %q", got)
	}
}

func TestConfigResetValidationFailureRollsBack(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.setActive(services.IDNginx, true)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")
	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\n",
	}); err != nil {
		t.Fatal(err)
	}
	f.validator.calls = nil
	f.validator.validateErr = errors.New("nginx: invalid")

	_, err := f.service.Reset(context.Background(), services.IDNginx, "nginx.conf")
	if !errors.Is(err, ErrConfigValidationFailed) {
		t.Fatalf("reset error = %v, want ErrConfigValidationFailed", err)
	}
	if got := f.read(services.IDNginx, "nginx.conf"); got != "worker_processes 2;\n" {
		t.Fatalf("reset rollback content = %q", got)
	}
}

func TestConfigApplyHelperFailureDoesNotWrite(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")
	f.lifecycle.err = errors.New("helper unavailable")

	if _, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 2;\n",
	}); err == nil || !strings.Contains(err.Error(), "helper unavailable") {
		t.Fatalf("error = %v", err)
	}
	if got := f.read(services.IDNginx, "nginx.conf"); got != "worker_processes 1;\n" {
		t.Fatalf("content changed despite helper failure: %q", got)
	}
}

func TestConfigNoopApplyDoesNotWrite(t *testing.T) {
	f := newConfigFixture(t)
	f.install(services.IDNginx)
	f.write(services.IDNginx, "nginx.conf", "worker_processes 1;\n")

	result, err := f.service.Apply(context.Background(), services.IDNginx, ConfigApplyRequest{
		Path: "nginx.conf", Content: "worker_processes 1;\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.BackupPath != "" || result.Live {
		t.Fatalf("no-op result = %+v", result)
	}
	if len(f.validator.calls) != 0 {
		t.Fatalf("validator calls = %v, want none", f.validator.calls)
	}
}
