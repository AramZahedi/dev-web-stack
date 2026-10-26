package performance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunLoadMeasuresLocalHTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	result, raw, err := RunLoad(context.Background(), LoadOptions{
		URL:         server.URL + "/",
		HostHeader:  "bench.test",
		Duration:    400 * time.Millisecond,
		Connections: 4,
	})
	if err != nil {
		t.Fatalf("RunLoad: %v", err)
	}
	if result.Tool != LoadToolName || result.Version != LoadToolVersion {
		t.Fatalf("tool metadata = %q/%q", result.Tool, result.Version)
	}
	if result.Requests <= 0 || result.RPS <= 0 {
		t.Fatalf("measured requests = %d rps = %f", result.Requests, result.RPS)
	}
	if result.Errors != 0 {
		t.Fatalf("errors = %d", result.Errors)
	}
	if result.P50MS < 0 || result.P95MS < result.P50MS || result.P99MS < result.P95MS {
		t.Fatalf("percentiles = %f/%f/%f", result.P50MS, result.P95MS, result.P99MS)
	}
	if result.Duration <= 0 || result.StartedAt.IsZero() {
		t.Fatalf("duration metadata = %+v", result)
	}
	if len(result.Limitations) == 0 {
		t.Fatal("load generator did not report its limitations")
	}
	text := string(raw)
	if !strings.Contains(text, LoadToolName) || !strings.Contains(text, "requests=") {
		t.Fatalf("raw output = %q", text)
	}
}

func TestRunLoadCountsRedirectsAsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()

	result, _, err := RunLoad(context.Background(), LoadOptions{
		URL:         server.URL + "/",
		Duration:    200 * time.Millisecond,
		Connections: 2,
	})
	if err == nil {
		t.Fatalf("redirect-only run succeeded with rps=%f errors=%d", result.RPS, result.Errors)
	}
	if result.Requests == 0 || result.Errors != result.Requests {
		t.Fatalf("redirect result = requests=%d errors=%d, want all failures", result.Requests, result.Errors)
	}
}

func TestRunLoadRejectsAllFailedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	result, _, err := RunLoad(context.Background(), LoadOptions{
		URL:         server.URL + "/",
		Duration:    200 * time.Millisecond,
		Connections: 2,
	})
	if err == nil {
		t.Fatalf("all-error run succeeded with rps=%f", result.RPS)
	}
	if result.Requests == 0 || result.Errors != result.Requests || result.RPS != 0 {
		t.Fatalf("all-error result = %+v", result)
	}
}

func TestRunLoadHandlesLoopbackTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	result, _, err := RunLoad(context.Background(), LoadOptions{
		URL:         server.URL + "/",
		Duration:    250 * time.Millisecond,
		Connections: 2,
		InsecureTLS: true,
	})
	if err != nil {
		t.Fatalf("RunLoad(https): %v", err)
	}
	if result.Requests <= 0 || result.Errors != 0 {
		t.Fatalf("https result = requests=%d errors=%d", result.Requests, result.Errors)
	}
}

func TestRunLoadRejectsUnsafeOptions(t *testing.T) {
	tests := []struct {
		name string
		opts LoadOptions
	}{
		{"empty url", LoadOptions{}},
		{"non loopback", LoadOptions{URL: "http://example.com/", Duration: time.Second, Connections: 1}},
		{"zero duration", LoadOptions{URL: "http://127.0.0.1:1/", Connections: 1}},
		{"negative connections", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: -1}},
		{"too many connections", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: 100001}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := RunLoad(context.Background(), tt.opts); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRunLoadStopsOnCanceledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, _, err := RunLoad(ctx, LoadOptions{
		URL:         server.URL + "/",
		Duration:    5 * time.Second,
		Connections: 2,
	})
	if err == nil {
		t.Fatal("RunLoad ignored context cancellation")
	}
	if result.RPS != 0 {
		t.Fatalf("canceled run reported RPS %f", result.RPS)
	}
}
