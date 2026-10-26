package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/performance"
)

type fakePerformanceService struct {
	mu sync.Mutex

	status     performance.Status
	preview    performance.Preview
	apply      performance.ApplyResult
	applyErr   error
	reset      performance.ResetResult
	resetErr   error
	profile    performance.ProfileRequest
	benchmark  performance.BenchmarkResult
	benchErr   error
	history    []performance.BenchmarkResult
	appliedReq performance.ApplyRequest
	resetReq   performance.ResetRequest
	benchReq   performance.BenchmarkRequest
}

func (f *fakePerformanceService) Status(context.Context) (performance.Status, error) {
	return f.status, nil
}

func (f *fakePerformanceService) SetProfile(_ context.Context, req performance.ProfileRequest) (performance.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profile = req
	return f.status, nil
}

func (f *fakePerformanceService) Preview(context.Context) (performance.Preview, error) {
	return f.preview, nil
}

func (f *fakePerformanceService) Apply(_ context.Context, req performance.ApplyRequest) (performance.ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedReq = req
	return f.apply, f.applyErr
}

func (f *fakePerformanceService) Reset(_ context.Context, req performance.ResetRequest) (performance.ResetResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetReq = req
	return f.reset, f.resetErr
}

func (f *fakePerformanceService) RunBenchmark(_ context.Context, req performance.BenchmarkRequest) (performance.BenchmarkResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.benchReq = req
	return f.benchmark, f.benchErr
}

func (f *fakePerformanceService) ListBenchmarks(context.Context) ([]performance.BenchmarkResult, error) {
	return f.history, nil
}

func (f *fakePerformanceService) GetBenchmark(_ context.Context, id string) (performance.BenchmarkResult, error) {
	for _, result := range f.history {
		if result.ID == id {
			return result, nil
		}
	}
	return performance.BenchmarkResult{}, performance.ErrBenchmarkNotFound
}

func TestPerformanceAPICSFRFAndReads(t *testing.T) {
	fake := &fakePerformanceService{
		status: performance.Status{Profile: performance.Balanced, Values: performance.Values{Profile: performance.Balanced}},
	}
	fx := newFixture(t, Config{Performance: fake})

	// Mutations without a session/CSRF token are rejected before the service.
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "http://127.0.0.1/api/v1/performance/profile",
		strings.NewReader(`{"profile":"balanced"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("profile without CSRF = %d, want 403", rec.Code)
	}

	// Read-only GETs do not require a session.
	rec = fx.request(t, http.MethodGet, "/api/v1/performance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET performance = %d body=%s", rec.Code, rec.Body.String())
	}
	var status performance.Status
	decode(t, rec, &status)
	if status.Profile != performance.Balanced {
		t.Fatalf("status = %+v", status)
	}

	// A valid CSRF-protected profile update reaches the service.
	rec = fx.request(t, http.MethodPut, "/api/v1/performance/profile", strings.NewReader(`{"profile":"throughput"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT profile = %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.profile.Profile != "throughput" {
		t.Fatalf("profile request = %+v", fake.profile)
	}
}

func TestPerformanceAPIPreviewAndApplyConfirmation(t *testing.T) {
	fake := &fakePerformanceService{
		preview: performance.Preview{Profile: performance.Balanced},
		apply:   performance.ApplyResult{Applied: true, Profile: performance.Balanced},
	}
	fx := newFixture(t, Config{Performance: fake})

	rec := fx.request(t, http.MethodGet, "/api/v1/performance/preview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/performance/apply", strings.NewReader(`{"confirm":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d body=%s", rec.Code, rec.Body.String())
	}
	if !fake.appliedReq.Confirm || fake.appliedReq.HostTuning {
		t.Fatalf("apply request = %+v", fake.appliedReq)
	}

	rec = fx.request(t, http.MethodPost, "/api/v1/performance/apply", strings.NewReader(`{"confirm":true,"host_tuning":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply with host tuning = %d body=%s", rec.Code, rec.Body.String())
	}
	if !fake.appliedReq.HostTuning {
		t.Fatal("host_tuning was not passed through")
	}
}

func TestPerformanceAPIApplyFailureReturnsSteps(t *testing.T) {
	failure := &performance.ApplyError{
		Err: errors.New("reload failed"),
		Result: performance.ApplyResult{
			Applied:       false,
			BackupDir:     "/home/dev/.devstack/backups/performance/run",
			RollbackError: "",
			Steps:         []performance.Step{{Name: "reload", Status: "failed", Detail: "reload failed"}},
		},
	}
	fake := &fakePerformanceService{applyErr: failure}
	fx := newFixture(t, Config{Performance: fake})

	rec := fx.request(t, http.MethodPost, "/api/v1/performance/apply", strings.NewReader(`{"confirm":true}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply failure = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	decode(t, rec, &body)
	if body.Code != codeInternal {
		t.Fatalf("code = %q", body.Code)
	}
	if _, ok := body.Details["result"]; !ok {
		t.Fatalf("failure details missing result: %+v", body.Details)
	}
}

func TestPerformanceAPIBenchmarks(t *testing.T) {
	result := performance.BenchmarkResult{
		ID: "20261026T120000.123456789Z", Site: "blog", Status: performance.BenchmarkCompleted,
		Tool: performance.LoadToolName, ToolVersion: performance.LoadToolVersion,
		StartedAt: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}
	fake := &fakePerformanceService{benchmark: result, history: []performance.BenchmarkResult{result}}
	fx := newFixture(t, Config{Performance: fake})

	rec := fx.request(t, http.MethodPost, "/api/v1/performance/benchmarks",
		strings.NewReader(`{"site":"blog","duration_s":5,"connections":20}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("benchmark = %d body=%s", rec.Code, rec.Body.String())
	}
	if fake.benchReq.Site != "blog" || fake.benchReq.DurationSeconds != 5 || fake.benchReq.Connections != 20 {
		t.Fatalf("benchmark request = %+v", fake.benchReq)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/performance/benchmarks", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history = %d body=%s", rec.Code, rec.Body.String())
	}
	var history []performance.BenchmarkResult
	decode(t, rec, &history)
	if len(history) != 1 || history[0].ID != result.ID {
		t.Fatalf("history = %+v", history)
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/performance/benchmarks/"+result.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get benchmark = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = fx.request(t, http.MethodGet, "/api/v1/performance/benchmarks/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing benchmark = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPerformanceAPIServiceUnavailable(t *testing.T) {
	fx := newFixture(t, Config{})
	rec := fx.request(t, http.MethodGet, "/api/v1/performance", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func decodeBenchmark(t *testing.T, rec *httptest.ResponseRecorder) performance.BenchmarkResult {
	t.Helper()
	var result performance.BenchmarkResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
