package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/performance"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type fakePerfHelper struct {
	mu sync.Mutex

	statusCalls int
	applyCalls  []helperproto.PerformanceApplyRequest
	resetCalls  int
	statusErr   error
	applyErr    error
	resetErr    error
	status      helperproto.PerformanceStatusResponse
}

func (h *fakePerfHelper) PerformanceStatus(context.Context) (helperproto.PerformanceStatusResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statusCalls++
	return h.status, h.statusErr
}

func (h *fakePerfHelper) PerformanceApply(_ context.Context, req helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.applyCalls = append(h.applyCalls, req)
	if h.applyErr != nil {
		return helperproto.PerformanceApplyResponse{}, h.applyErr
	}
	return helperproto.PerformanceApplyResponse{
		Applied:   true,
		Profile:   req.Profile,
		BackupDir: "/root/backups/performance/fake",
		AppliedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		Steps:     []helperproto.PerformanceStep{{Name: "write-sysctl", Status: "ok"}},
	}, nil
}

func (h *fakePerfHelper) PerformanceReset(context.Context, helperproto.PerformanceResetRequest) (helperproto.PerformanceResetResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resetCalls++
	if h.resetErr != nil {
		return helperproto.PerformanceResetResponse{}, h.resetErr
	}
	return helperproto.PerformanceResetResponse{Restored: true, BackupDir: "/root/backups/performance/fake"}, nil
}

type fakePerfServices struct {
	mu        sync.Mutex
	views     []services.View
	validate  []services.ID
	reload    []services.ID
	validateE error
	reloadE   error
	listE     error
}

func (s *fakePerfServices) List(context.Context) ([]services.View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]services.View(nil), s.views...), s.listE
}

func (s *fakePerfServices) Validate(_ context.Context, id services.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validate = append(s.validate, id)
	return s.validateE
}

func (s *fakePerfServices) Reload(_ context.Context, id services.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload = append(s.reload, id)
	return s.reloadE
}

type fakePerfSites struct {
	mu       sync.Mutex
	sites    []model.Site
	applied  []perfAppliedPHP
	applyErr error
}

type perfAppliedPHP struct {
	Ref    string
	Change sites.PHPChange
}

func (s *fakePerfSites) List(context.Context) ([]model.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Site(nil), s.sites...), nil
}

func (s *fakePerfSites) ApplyPHP(_ context.Context, ref string, change sites.PHPChange) (model.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, perfAppliedPHP{Ref: ref, Change: change})
	if s.applyErr != nil {
		return model.Site{}, s.applyErr
	}
	for _, site := range s.sites {
		if site.ID == ref || site.Name == ref {
			return site, nil
		}
	}
	return model.Site{}, errors.New("site not found")
}

func newPerformanceTestService(t *testing.T) (*Service, *store.Store, *fakePerfHelper, *fakePerfServices, *fakePerfSites, string) {
	t.Helper()
	root := t.TempDir()
	stateDir := t.TempDir()
	st, err := store.Open(filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	helper := &fakePerfHelper{}
	servicesFake := &fakePerfServices{views: []services.View{{
		ID: services.IDNginx, Group: services.GroupWeb, Active: true, State: services.StateActive,
	}}}
	sitesFake := &fakePerfSites{}
	now := func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }

	svc := NewService(ServiceConfig{
		Root:      root,
		Store:     st,
		Helper:    helper,
		Services:  servicesFake,
		Sites:     sitesFake,
		HTTPPort:  8080,
		HTTPSPort: 8443,
		Now:       now,
		Resources: func() (Resources, error) {
			return testResources(), nil
		},
		Logger: discardLogger(),
	})
	return svc, st, helper, servicesFake, sitesFake, root
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestServiceProfilePersistsAndRevalidatesCustom(t *testing.T) {
	ctx := context.Background()
	svc, st, _, _, _, _ := newPerformanceTestService(t)

	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Profile != Balanced {
		t.Fatalf("default profile = %q, want balanced", status.Profile)
	}
	if status.Resources.CPUCount == 0 {
		t.Fatal("status did not report measured resources")
	}

	custom := mustCompute(t, testResources(), Balanced, "a")
	custom.Profile = Custom
	status, err = svc.SetProfile(ctx, ProfileRequest{Profile: string(Custom), Custom: &custom})
	if err != nil {
		t.Fatalf("SetProfile(custom): %v", err)
	}
	if status.Profile != Custom || status.Custom == nil {
		t.Fatalf("custom status = %+v", status)
	}

	// Persisted values survive a fresh read.
	raw, err := st.GetSetting(ctx, settingCustom)
	if err != nil || raw == "" {
		t.Fatalf("custom setting = %q, %v", raw, err)
	}
	status, err = svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Profile != Custom || status.Custom == nil || status.Custom.Nginx.Workers != custom.Nginx.Workers {
		t.Fatalf("reloaded custom status = %+v", status)
	}

	// Every preview revalidates the stored custom values.
	bad := custom
	bad.Nginx.Workers = 10_000
	data, _ := json.Marshal(bad)
	if err := st.SetSetting(ctx, settingCustom, string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Preview(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("preview error = %v, want ErrInvalid", err)
	}
}

func TestServicePreviewDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	svc, _, helper, _, _, root := newPerformanceTestService(t)

	preview, err := svc.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Profile != Balanced {
		t.Fatalf("profile = %q", preview.Profile)
	}
	if preview.Nginx.Main == "" || preview.Nginx.Events == "" || preview.Nginx.HTTP == "" {
		t.Fatal("preview did not include nginx include contents")
	}
	if preview.Apache.Config == "" {
		t.Fatal("preview did not include Apache contents")
	}
	if len(preview.Host.Files) != 2 {
		t.Fatalf("host tuning files = %d, want 2", len(preview.Host.Files))
	}
	if preview.Host.Files[0].Path != SysctlTarget || preview.Host.Files[1].Path != LimitsTarget {
		t.Fatalf("host paths = %q/%q", preview.Host.Files[0].Path, preview.Host.Files[1].Path)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("preview wrote under root: %v", entries)
	}
	if len(helper.applyCalls) != 0 || helper.resetCalls != 0 {
		t.Fatal("preview applied helper host tuning")
	}
}

func TestServiceApplyRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, root := newPerformanceTestService(t)

	if _, err := svc.Apply(ctx, ApplyRequest{}); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("apply error = %v, want ErrConfirmationRequired", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unconfirmed apply wrote under root: %v", entries)
	}
}

func TestServiceApplyWritesOnlyActiveServer(t *testing.T) {
	ctx := context.Background()
	svc, st, helper, servicesFake, _, root := newPerformanceTestService(t)

	result, err := svc.Apply(ctx, ApplyRequest{Confirm: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !result.Applied || result.ActiveServer != string(services.IDNginx) {
		t.Fatalf("apply result = %+v", result)
	}
	nginxMain := filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "main.conf")
	if _, err := os.Stat(nginxMain); err != nil {
		t.Fatalf("nginx include missing: %v", err)
	}
	apacheConf := filepath.Join(root, "services", "apache", "config", "conf.d", "performance.conf")
	if _, err := os.Stat(apacheConf); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive apache include written: %v", err)
	}
	if len(helper.applyCalls) != 0 {
		t.Fatal("host tuning was applied without host_tuning:true")
	}
	if len(servicesFake.validate) != 1 || servicesFake.validate[0] != services.IDNginx {
		t.Fatalf("validate calls = %v", servicesFake.validate)
	}
	if len(servicesFake.reload) != 1 || servicesFake.reload[0] != services.IDNginx {
		t.Fatalf("reload calls = %v", servicesFake.reload)
	}
	raw, err := st.GetSetting(ctx, settingApplied)
	if err != nil || raw == "" {
		t.Fatalf("applied state = %q, %v", raw, err)
	}
}

func TestServiceApplyDatabaseOnlySkipsWebIncludes(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, _, root := newPerformanceTestService(t)
	servicesFake.views = nil

	result, err := svc.Apply(ctx, ApplyRequest{Confirm: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !result.Applied {
		t.Fatal("database-only apply was not recorded")
	}
	if result.ActiveServer != "" {
		t.Fatalf("active server = %q, want empty", result.ActiveServer)
	}
	for _, path := range []string{
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance"),
		filepath.Join(root, "services", "apache", "config", "conf.d", "performance.conf"),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("database-only apply wrote %s", path)
		}
	}
	if len(servicesFake.validate) != 0 || len(servicesFake.reload) != 0 {
		t.Fatal("database-only apply reloaded a service")
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), "no active") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing database-only warning: %v", result.Warnings)
	}
}

func TestServiceApplyDatabaseOnlySkipsHostTuning(t *testing.T) {
	ctx := context.Background()
	svc, _, helper, servicesFake, _, _ := newPerformanceTestService(t)
	servicesFake.views = nil

	result, err := svc.Apply(ctx, ApplyRequest{Confirm: true, HostTuning: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.HostTuning {
		t.Fatal("database-only apply claimed host tuning")
	}
	if len(helper.applyCalls) != 0 {
		t.Fatalf("host apply calls = %d, want 0", len(helper.applyCalls))
	}
	found := false
	for _, step := range result.Steps {
		if step.Name == "host-tuning" && step.Status == "skipped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("host tuning skip step missing: %+v", result.Steps)
	}
}

func TestServiceApplyHostTuningIsExplicit(t *testing.T) {
	ctx := context.Background()
	svc, _, helper, _, _, _ := newPerformanceTestService(t)

	if _, err := svc.Apply(ctx, ApplyRequest{Confirm: true, HostTuning: true}); err != nil {
		t.Fatalf("apply with host tuning: %v", err)
	}
	if len(helper.applyCalls) != 1 {
		t.Fatalf("host apply calls = %d, want 1", len(helper.applyCalls))
	}
	req := helper.applyCalls[0]
	if req.Profile != string(Balanced) || req.Host.FileMax == 0 || req.Host.NoFileSoft == 0 {
		t.Fatalf("host apply request = %+v", req)
	}
}

func TestServiceApplyRollsBackManagedFilesOnReloadFailure(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, _, root := newPerformanceTestService(t)
	mainPath := filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "main.conf")
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte("old-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	servicesFake.reloadE = errors.New("reload failed")

	_, err := svc.Apply(ctx, ApplyRequest{Confirm: true})
	if err == nil {
		t.Fatal("apply succeeded despite reload failure")
	}
	var failure *ApplyError
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T, want *ApplyError", err)
	}
	if failure.Result.BackupDir == "" {
		t.Fatal("failure result has no backup directory")
	}
	if failure.Result.RollbackError != "" {
		t.Fatalf("rollback error = %q", failure.Result.RollbackError)
	}
	data, readErr := os.ReadFile(mainPath)
	if readErr != nil || string(data) != "old-main\n" {
		t.Fatalf("main.conf after rollback = %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "events.conf")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rollback left new include behind: %v", statErr)
	}
}

func TestServiceResetRestoresSafeIncludes(t *testing.T) {
	ctx := context.Background()
	svc, st, helper, _, _, root := newPerformanceTestService(t)
	if _, err := svc.Apply(ctx, ApplyRequest{Confirm: true, HostTuning: true}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Reset(ctx, ResetRequest{Confirm: true})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !result.Reset {
		t.Fatal("reset result is false")
	}
	if helper.resetCalls != 1 {
		t.Fatalf("host reset calls = %d, want 1", helper.resetCalls)
	}
	for _, file := range []string{
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "main.conf"),
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "events.conf"),
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "http.conf"),
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(data), "reset") {
			t.Fatalf("%s does not contain the safe reset marker: %q", file, data)
		}
		if strings.Contains(string(data), "worker_processes") || strings.Contains(string(data), "worker_connections") {
			t.Fatalf("%s still contains tuning: %q", file, data)
		}
	}
	if _, err := st.GetSetting(ctx, settingApplied); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("applied setting after reset = %v", err)
	}
}

func TestServiceBenchmarkStoresMeasuredResults(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, sitesFake, root := newPerformanceTestService(t)
	servicesFake.views = nil
	sitesFake.sites = []model.Site{{ID: "site1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Status: model.StatusRunning, TLS: true}}
	svc.cfg.LoadRun = func(_ context.Context, opts LoadOptions) (LoadResult, []byte, error) {
		if !strings.HasPrefix(opts.URL, "https://") {
			t.Fatalf("benchmark target = %q, want https", opts.URL)
		}
		return LoadResult{
			Tool: LoadToolName, Version: LoadToolVersion,
			Requests: 1000, Errors: 0, RPS: 123.4,
			P50MS: 2.1, P95MS: 4.2, P99MS: 7.3,
			Duration: time.Second, Connections: opts.Connections,
			StartedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		}, []byte("raw load output\n"), nil
	}

	result, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog", DurationSeconds: 5, Connections: 20})
	if err != nil {
		t.Fatalf("benchmark: %v", err)
	}
	if result.Measured == nil {
		t.Fatal("benchmark has no measured result")
	}
	if result.Measured.RPS != 123.4 || result.Measured.Requests != 1000 || result.Measured.P99MS != 7.3 {
		t.Fatalf("measured = %+v", result.Measured)
	}
	if result.Tool != LoadToolName || result.ToolVersion != LoadToolVersion || len(result.Limitations) == 0 {
		t.Fatalf("tool metadata = %+v", result)
	}
	dir := filepath.Join(root, "logs", "performance", result.ID)
	for _, name := range []string{"result.json", "raw.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	history, err := svc.ListBenchmarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID != result.ID {
		t.Fatalf("history = %+v", history)
	}
	loaded, err := svc.GetBenchmark(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Measured, result.Measured) {
		t.Fatalf("loaded benchmark = %+v", loaded.Measured)
	}
}

func TestServiceBenchmarkFailureStoresNoMeasurement(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, sitesFake, root := newPerformanceTestService(t)
	servicesFake.views = nil
	sitesFake.sites = []model.Site{{ID: "site1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Status: model.StatusRunning}}
	svc.cfg.LoadRun = func(context.Context, LoadOptions) (LoadResult, []byte, error) {
		return LoadResult{Tool: LoadToolName, Version: LoadToolVersion}, []byte("partial\n"), errors.New("target refused")
	}

	result, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog"})
	if err == nil {
		t.Fatal("benchmark succeeded despite load failure")
	}
	if result.Measured != nil {
		t.Fatalf("failed benchmark has synthesized measurement: %+v", result.Measured)
	}
	if result.Status != BenchmarkFailed || result.Error == "" {
		t.Fatalf("failed benchmark result = %+v", result)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "logs", "performance", result.ID, "result.json"))
	if readErr != nil {
		t.Fatalf("read failed benchmark result: %v", readErr)
	}
	var stored BenchmarkResult
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Measured != nil {
		t.Fatalf("stored failed benchmark has measurement: %+v", stored.Measured)
	}
}

func TestServiceBenchmarkTreatsAllFailedRequestsAsFailure(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, sitesFake, _ := newPerformanceTestService(t)
	servicesFake.views = nil
	sitesFake.sites = []model.Site{{ID: "site1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Status: model.StatusRunning}}
	svc.cfg.LoadRun = func(context.Context, LoadOptions) (LoadResult, []byte, error) {
		return LoadResult{
			Tool: LoadToolName, Version: LoadToolVersion,
			Requests: 100, Errors: 100, RPS: 1000,
			Duration: time.Second,
		}, []byte("all failed\n"), nil
	}

	result, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog"})
	if err == nil {
		t.Fatalf("benchmark succeeded with all requests failed: %+v", result)
	}
	if result.Measured != nil || result.Status != BenchmarkFailed {
		t.Fatalf("failed result = %+v", result)
	}
}

func TestIncludeWarningIgnoresCommentedDirectives(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(base, []byte("# include /etc/nginx/conf.d/performance/main.conf;\nevents {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := includeWarning(base, []string{"/conf.d/performance/main.conf"}); warning == "" {
		t.Fatal("commented include was accepted")
	}
	if err := os.WriteFile(base, []byte("include /etc/nginx/conf.d/performance/main.conf;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := includeWarning(base, []string{"/conf.d/performance/main.conf"}); warning != "" {
		t.Fatalf("real include produced warning: %s", warning)
	}
	if err := os.WriteFile(base, []byte("include /etc/nginx/conf.d/performance/*.conf;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := includeWarning(base, []string{"/conf.d/performance/main.conf"}); warning != "" {
		t.Fatalf("glob include produced warning: %s", warning)
	}
}

func TestServiceBenchmarkUsesLastAppliedProfile(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, sitesFake, _ := newPerformanceTestService(t)
	servicesFake.views = nil
	sitesFake.sites = []model.Site{{ID: "site1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Status: model.StatusRunning}}
	if err := svc.cfg.Store.SetSetting(ctx, settingProfile, string(Throughput)); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(AppliedState{Profile: Balanced})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.cfg.Store.SetSetting(ctx, settingApplied, string(data)); err != nil {
		t.Fatal(err)
	}
	svc.cfg.LoadRun = func(context.Context, LoadOptions) (LoadResult, []byte, error) {
		return LoadResult{Tool: LoadToolName, Version: LoadToolVersion, Requests: 10, RPS: 10, Duration: time.Second}, []byte("raw"), nil
	}

	result, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog", DurationSeconds: 1, Connections: 1})
	if err != nil {
		t.Fatalf("benchmark: %v", err)
	}
	if result.Profile != Balanced {
		t.Fatalf("benchmark profile = %q, want applied profile %q", result.Profile, Balanced)
	}
	joined := strings.Join(result.Limitations, " ")
	if !strings.Contains(joined, "selected profile throughput") || !strings.Contains(joined, "last applied profile is balanced") {
		t.Fatalf("profile attribution warning missing: %v", result.Limitations)
	}
}

func TestServiceBenchmarkValidatesSiteAndBounds(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, sitesFake, _ := newPerformanceTestService(t)
	servicesFake.views = nil
	sitesFake.sites = []model.Site{{ID: "site1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Status: model.StatusRunning}}
	svc.cfg.LoadRun = func(context.Context, LoadOptions) (LoadResult, []byte, error) {
		t.Fatal("load generator ran for an invalid request")
		return LoadResult{}, nil, nil
	}

	if _, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "missing"}); !errors.Is(err, ErrBenchmarkSite) {
		t.Fatalf("unknown site error = %v, want ErrBenchmarkSite", err)
	}
	if _, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog", DurationSeconds: 3601}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duration error = %v, want ErrInvalid", err)
	}
	if _, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog", Connections: 100001}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("connections error = %v, want ErrInvalid", err)
	}
}

func TestServiceResetRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _ := newPerformanceTestService(t)
	if _, err := svc.Reset(ctx, ResetRequest{}); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("reset error = %v, want ErrConfirmationRequired", err)
	}
}

func TestServiceApplyMergesPHPIniAndResetRestores(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, sitesFake, _ := newPerformanceTestService(t)
	originalFPM := &model.PHPFPMBudget{Mode: "static", MaxChildren: 6, ListenBacklog: 128}
	sitesFake.sites = []model.Site{{
		ID: "php1", Name: "app", Domain: "app.test", Kind: model.SitePHP,
		Status: model.StatusRunning, PHPVersion: "8.4",
		PHPIni: map[string]string{"memory_limit": "256M"},
		PHPFPM: originalFPM,
	}}

	result, err := svc.Apply(ctx, ApplyRequest{Confirm: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(sitesFake.applied) != 1 {
		t.Fatalf("PHP applies = %d, want 1", len(sitesFake.applied))
	}
	merged := sitesFake.applied[0].Change.Ini
	if merged["memory_limit"] != "256M" {
		t.Fatalf("user PHP ini setting was lost: %+v", merged)
	}
	if merged["opcache.enable"] != "1" || merged["opcache.memory_consumption"] == "" || merged["realpath_cache_size"] == "" {
		t.Fatalf("performance PHP ini settings missing: %+v", merged)
	}
	pool := sitesFake.applied[0].Change.FPM
	if pool == nil || pool.MaxChildren < 1 || pool.Mode == "" {
		t.Fatalf("performance pool budget missing: %+v", pool)
	}
	applied, err := svc.loadAppliedLocked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil || applied.PHPFPM["php1"] != *originalFPM {
		t.Fatalf("applied FPM snapshot = %+v, want %+v", applied.PHPFPM, originalFPM)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "keep their current pool sizing") {
			t.Fatalf("stale pool sizing warning: %v", result.Warnings)
		}
	}

	sitesFake.applied = nil
	if _, err := svc.Reset(ctx, ResetRequest{Confirm: true}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(sitesFake.applied) != 1 {
		t.Fatalf("reset PHP applies = %d, want 1", len(sitesFake.applied))
	}
	restored := sitesFake.applied[0].Change.Ini
	if len(restored) != 1 || restored["memory_limit"] != "256M" {
		t.Fatalf("reset PHP ini = %+v, want only memory_limit", restored)
	}
	if used := sitesFake.applied[0].Change.FPM; used == nil || *used != *originalFPM {
		t.Fatalf("reset FPM = %+v, want %+v", used, originalFPM)
	}
}

func TestServiceApplyRollbackRestoresFPMBudget(t *testing.T) {
	ctx := context.Background()
	svc, _, helper, _, sitesFake, _ := newPerformanceTestService(t)
	original := &model.PHPFPMBudget{Mode: "static", MaxChildren: 6, ListenBacklog: 128}
	sitesFake.sites = []model.Site{{
		ID: "php1", Name: "app", Domain: "app.test", Kind: model.SitePHP,
		Status: model.StatusRunning, PHPVersion: "8.4", PHPFPM: original,
	}}
	helper.applyErr = errors.New("sysctl rejected")

	if _, err := svc.Apply(ctx, ApplyRequest{Confirm: true, HostTuning: true}); err == nil {
		t.Fatal("apply succeeded despite host tuning failure")
	}
	if len(sitesFake.applied) != 2 {
		t.Fatalf("PHP applies = %d, want apply and rollback", len(sitesFake.applied))
	}
	appliedBudget := sitesFake.applied[0].Change.FPM
	if appliedBudget == nil || *appliedBudget == *original {
		t.Fatalf("apply budget = %+v, want a changed pool", appliedBudget)
	}
	restored := sitesFake.applied[1].Change.FPM
	if restored == nil || *restored != *original {
		t.Fatalf("rollback FPM = %+v, want %+v", restored, original)
	}
}

func TestServiceApplyHostFailureReloadsRestoredIncludes(t *testing.T) {
	ctx := context.Background()
	svc, _, helper, servicesFake, _, root := newPerformanceTestService(t)
	helper.applyErr = errors.New("sysctl rejected")
	mainPath := filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "main.conf")
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte("old-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Apply(ctx, ApplyRequest{Confirm: true, HostTuning: true})
	if err == nil {
		t.Fatal("apply succeeded despite host tuning failure")
	}
	data, readErr := os.ReadFile(mainPath)
	if readErr != nil || string(data) != "old-main\n" {
		t.Fatalf("main.conf after rollback = %q, %v", data, readErr)
	}
	if len(servicesFake.reload) != 2 {
		t.Fatalf("reload calls = %v, want apply and rollback reloads", servicesFake.reload)
	}
}

func TestServiceBenchmarkRejectsUnsafeID(t *testing.T) {
	svc, _, _, _, _, _ := newPerformanceTestService(t)
	for _, id := range []string{"../state.db", "20261026T120000.000000000Z/../x", "not-a-run", ""} {
		if _, err := svc.GetBenchmark(context.Background(), id); !errors.Is(err, ErrBenchmarkNotFound) {
			t.Fatalf("GetBenchmark(%q) error = %v, want ErrBenchmarkNotFound", id, err)
		}
	}
}

func TestServiceBenchmarkBusy(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, sitesFake, _ := newPerformanceTestService(t)
	servicesFake.views = nil
	sitesFake.sites = []model.Site{{ID: "site1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Status: model.StatusRunning}}
	started := make(chan struct{})
	release := make(chan struct{})
	svc.cfg.LoadRun = func(context.Context, LoadOptions) (LoadResult, []byte, error) {
		close(started)
		<-release
		return LoadResult{Tool: LoadToolName, Version: LoadToolVersion, Requests: 1, RPS: 1, Duration: time.Second}, []byte("raw"), nil
	}
	done := make(chan struct{})
	go func() {
		_, _ = svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog"})
		close(done)
	}()
	<-started
	if _, err := svc.RunBenchmark(ctx, BenchmarkRequest{Site: "blog"}); !errors.Is(err, ErrBenchmarkBusy) {
		t.Fatalf("second benchmark error = %v, want ErrBenchmarkBusy", err)
	}
	close(release)
	<-done
}

// Local copies of the pure-engine test fixtures. The parent package's _test.go
// helpers are not importable from this subpackage.
func testResources() performance.Resources {
	return performance.Resources{
		Hostname:             "devbox",
		Arch:                 "amd64",
		CPUCount:             8,
		MemoryTotalBytes:     16 << 30,
		MemoryAvailableBytes: 8 << 30,
		PageSizeBytes:        4096,
		NoFileSoft:           1024,
		NoFileHard:           1 << 20,
		Sysctl: performance.Sysctl{
			FileMax:          1 << 20,
			Somaxconn:        4096,
			NetdevMaxBacklog: 4096,
			IPLocalPortRange: performance.PortRange{Min: 32768, Max: 60999},
		},
	}
}

func mustCompute(t *testing.T, res performance.Resources, profile performance.Profile, sites ...string) performance.Values {
	t.Helper()
	v, err := performance.Compute(res, performance.Options{Profile: profile, PHPSites: sites})
	if err != nil {
		t.Fatalf("Compute(%s): %v", profile, err)
	}
	return v
}

func TestServiceApplyFailsWhenServiceStatusIsUnavailable(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, _, root := newPerformanceTestService(t)
	servicesFake.listE = errors.New("helper unreachable")
	if _, err := svc.Apply(ctx, ApplyRequest{Confirm: true}); err == nil {
		t.Fatal("apply succeeded without service status")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed status apply wrote files: %v", entries)
	}
}

func TestServiceApplyReportsProfileConflictWhenBaseLacksInclude(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, root := newPerformanceTestService(t)
	base := filepath.Join(root, "services", "nginx", "config", "nginx.conf")
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte("events { worker_connections 1024; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Apply(ctx, ApplyRequest{Confirm: true})
	if !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("apply error = %v, want ErrProfileConflict", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "main.conf")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("apply wrote includes despite include conflict: %v", statErr)
	}
}

func TestServiceApplyWritesApacheWhenActive(t *testing.T) {
	ctx := context.Background()
	svc, _, _, servicesFake, _, root := newPerformanceTestService(t)
	servicesFake.views = []services.View{{
		ID: services.IDApache, Group: services.GroupWeb, Active: true, State: services.StateActive,
	}}
	result, err := svc.Apply(ctx, ApplyRequest{Confirm: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.ActiveServer != string(services.IDApache) {
		t.Fatalf("active server = %q", result.ActiveServer)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "apache", "config", "conf.d", "performance.conf")); err != nil {
		t.Fatalf("apache include missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "nginx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive nginx include written: %v", err)
	}
	if len(servicesFake.validate) != 1 || servicesFake.validate[0] != services.IDApache {
		t.Fatalf("validate calls = %v", servicesFake.validate)
	}
}

func TestServiceRepeatedApplyPreservesPHPOriginalSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, sitesFake, _ := newPerformanceTestService(t)
	sitesFake.sites = []model.Site{{
		ID: "php1", Name: "app", Domain: "app.test", Kind: model.SitePHP,
		Status: model.StatusRunning, PHPVersion: "8.4",
		PHPIni: map[string]string{"memory_limit": "256M"},
	}}
	if _, err := svc.Apply(ctx, ApplyRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	// Simulate the applied site row carrying the merged performance keys, and
	// a user edit made between applies.
	applied := sitesFake.applied[len(sitesFake.applied)-1]
	sitesFake.mu.Lock()
	sitesFake.sites[0].PHPIni = cloneStringMap(applied.Change.Ini)
	sitesFake.sites[0].PHPIni["max_execution_time"] = "60"
	sitesFake.mu.Unlock()

	if _, err := svc.Apply(ctx, ApplyRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	sitesFake.applied = nil
	if _, err := svc.Reset(ctx, ResetRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	restored := sitesFake.applied[0].Change.Ini
	if restored["memory_limit"] != "256M" || restored["max_execution_time"] != "60" {
		t.Fatalf("reset lost user PHP settings: %+v", restored)
	}
	for _, key := range managedPHPIniKeys() {
		if _, ok := restored[key]; ok {
			t.Fatalf("reset left performance key %q in %+v", key, restored)
		}
	}
}
