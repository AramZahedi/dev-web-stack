//go:build windows

package service

import (
	"context"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

type memorySettingsStore struct {
	data map[string]string
}

func newMemorySettingsStore() *memorySettingsStore {
	return &memorySettingsStore{data: make(map[string]string)}
}

func (m *memorySettingsStore) GetSetting(_ context.Context, key string) (string, error) {
	val, ok := m.data[key]
	if !ok {
		return "", nil
	}
	return val, nil
}

func (m *memorySettingsStore) SetSetting(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memorySettingsStore) DeleteSetting(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

type fakeWebServices struct{}

func (f fakeWebServices) List(context.Context) ([]services.View, error) {
	return []services.View{{ID: services.IDNginx, Active: true, State: "running"}}, nil
}
func (f fakeWebServices) Validate(context.Context, services.ID) error { return nil }
func (f fakeWebServices) Reload(context.Context, services.ID) error   { return nil }

type fakeSitesService struct{}

func (f fakeSitesService) List(context.Context) ([]model.Site, error) {
	return []model.Site{{ID: "site-1", Name: "blog", Domain: "blog.test", Kind: model.SitePHP, Status: model.StatusRunning}}, nil
}
func (f fakeSitesService) ApplyPHP(context.Context, string, sites.PHPChange) (model.Site, error) {
	return model.Site{}, nil
}

func TestWindowsHostTuningDisabledInStatusAndPreview(t *testing.T) {
	root := t.TempDir()
	st := newMemorySettingsStore()
	svc := NewService(ServiceConfig{
		Root:     root,
		Store:    st,
		Services: fakeWebServices{},
		Sites:    fakeSitesService{},
		Now:      func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
	})

	ctx := context.Background()
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if status.HostTuning.Available {
		t.Errorf("status.HostTuning.Available = true on Windows, want false")
	}
	if status.HostTuning.Error == "" {
		t.Errorf("status.HostTuning.Error is empty on Windows, want clear disabled message")
	}
	hasWarning := false
	for _, w := range status.Warnings {
		if w == "Host sysctl and limits tuning are disabled on Windows." || w == "Host sysctl tuning is disabled on Windows." {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("status.Warnings = %v, want Windows host sysctl tuning banner", status.Warnings)
	}

	preview, err := svc.Preview(ctx)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	hasPreviewWarning := false
	for _, w := range preview.Warnings {
		if w == "Host sysctl and limits tuning are disabled on Windows." || w == "Host sysctl tuning is disabled on Windows." {
			hasPreviewWarning = true
			break
		}
	}
	if !hasPreviewWarning {
		t.Errorf("preview.Warnings = %v, want Windows host sysctl tuning banner", preview.Warnings)
	}
}

func TestWindowsHostTuningSkippedOnApply(t *testing.T) {
	root := t.TempDir()
	st := newMemorySettingsStore()
	svc := NewService(ServiceConfig{
		Root:     root,
		Store:    st,
		Services: fakeWebServices{},
		Sites:    fakeSitesService{},
		Now:      func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
	})

	ctx := context.Background()
	result, err := svc.Apply(ctx, ApplyRequest{Confirm: true, HostTuning: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if result.HostTuning {
		t.Errorf("result.HostTuning = true on Windows, want false")
	}

	foundStep := false
	for _, step := range result.Steps {
		if step.Name == "host-tuning" {
			foundStep = true
			if step.Status != "skipped" {
				t.Errorf("step 'host-tuning' status = %q, want 'skipped'", step.Status)
			}
		}
	}
	if !foundStep {
		t.Errorf("result.Steps = %+v, missing 'host-tuning' step", result.Steps)
	}
}

func TestWindowsBenchmarkTargetPublicRoute(t *testing.T) {
	root := t.TempDir()
	st := newMemorySettingsStore()
	svc := NewService(ServiceConfig{
		Root:     root,
		Store:    st,
		Services: fakeWebServices{},
		Sites:    fakeSitesService{},
		Now:      func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
	})

	ctx := context.Background()
	site := model.Site{Name: "blog", Domain: "blog.test", Status: model.StatusRunning}
	target, insecure, err := svc.benchmarkTargetLocked(ctx, site)
	if err != nil {
		t.Fatalf("benchmarkTargetLocked: %v", err)
	}
	if target != "https://blog.test/" {
		t.Errorf("benchmark target = %q, want https://blog.test/", target)
	}
	if !insecure {
		t.Errorf("benchmark insecure = false, want true")
	}
}

func TestWindowsRunBenchmarkStoresResult(t *testing.T) {
	root := t.TempDir()
	st := newMemorySettingsStore()
	svc := NewService(ServiceConfig{
		Root:     root,
		Store:    st,
		Services: fakeWebServices{},
		Sites:    fakeSitesService{},
		Now:      func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
		LoadRun: func(ctx context.Context, opts LoadOptions) (LoadResult, []byte, error) {
			return LoadResult{
				Tool:     LoadToolName,
				Version:  LoadToolVersion,
				Requests: 100,
				RPS:      50,
				URL:      opts.URL,
			}, []byte("tool=" + LoadToolName + "\n"), nil
		},
	})

	ctx := context.Background()
	res, err := svc.RunBenchmark(ctx, BenchmarkRequest{
		Site:            "blog",
		DurationSeconds: 1,
		Connections:     2,
	})
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}

	if res.Target != "https://blog.test/" {
		t.Errorf("res.Target = %q, want https://blog.test/", res.Target)
	}
	if res.Status != BenchmarkCompleted {
		t.Errorf("res.Status = %q, want %q", res.Status, BenchmarkCompleted)
	}

	// Verify benchmark is stored and can be listed
	list, err := svc.ListBenchmarks(ctx)
	if err != nil {
		t.Fatalf("ListBenchmarks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListBenchmarks returned %d results, want 1", len(list))
	}
	if list[0].Target != "https://blog.test/" {
		t.Errorf("stored target = %q, want https://blog.test/", list[0].Target)
	}

	// Verify GetBenchmark by ID
	got, err := svc.GetBenchmark(ctx, res.ID)
	if err != nil {
		t.Fatalf("GetBenchmark: %v", err)
	}
	if got.ID != res.ID || got.Target != "https://blog.test/" {
		t.Errorf("GetBenchmark = %+v, want ID %s and target https://blog.test/", got, res.ID)
	}
}


