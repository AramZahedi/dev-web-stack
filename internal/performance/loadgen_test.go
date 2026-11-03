package performance

import (
	"context"
	"errors"
	"io"
	"net"
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

func TestRunLoadSendsMethodPathHeadersAndBody(t *testing.T) {
	type observed struct {
		method      string
		path        string
		query       string
		host        string
		header      string
		contentType string
		body        string
	}
	got := make(chan observed, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case got <- observed{
			method:      r.Method,
			path:        r.URL.Path,
			query:       r.URL.RawQuery,
			host:        r.Host,
			header:      r.Header.Get("X-Bench"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		}:
		default:
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	result, raw, err := RunLoad(context.Background(), LoadOptions{
		URL:         server.URL + "/api/orders?trace=1",
		HostHeader:  "bench.test",
		Method:      http.MethodPost,
		Headers:     map[string]string{"X-Bench": "yes", "Content-Type": "application/json"},
		Body:        []byte(`{"hello":"world"}`),
		Duration:    250 * time.Millisecond,
		Connections: 2,
	})
	if err != nil {
		t.Fatalf("RunLoad: %v", err)
	}
	if result.Method != http.MethodPost || result.BodyBytes != len(`{"hello":"world"}`) {
		t.Fatalf("result method/body = %q/%d", result.Method, result.BodyBytes)
	}
	if !strings.Contains(string(raw), "method=POST") || !strings.Contains(string(raw), "body_bytes=17") {
		t.Fatalf("raw output = %q", raw)
	}
	select {
	case o := <-got:
		if o.method != http.MethodPost || o.path != "/api/orders" || o.query != "trace=1" {
			t.Fatalf("request line = %s %s?%s", o.method, o.path, o.query)
		}
		if o.host != "bench.test" || o.header != "yes" || o.contentType != "application/json" {
			t.Fatalf("request headers = host=%q x-bench=%q content-type=%q", o.host, o.header, o.contentType)
		}
		if o.body != `{"hello":"world"}` {
			t.Fatalf("request body = %q", o.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive a request")
	}
}

func TestRunLoadSendsTestHostsToLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := RunLoad(context.Background(), LoadOptions{
		URL:         "http://bench.test:" + port + "/",
		HostHeader:  "bench.test",
		Duration:    250 * time.Millisecond,
		Connections: 2,
	})
	if err != nil {
		t.Fatalf("RunLoad(.test): %v", err)
	}
	if result.Requests == 0 || result.Errors != 0 {
		t.Fatalf("result = requests=%d errors=%d", result.Requests, result.Errors)
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
		{"bad method", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: 1, Method: "TRACE"}},
		{"get with body", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: 1, Method: http.MethodGet, Body: []byte("x")}},
		{"bad header name", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: 1, Method: http.MethodGet, Headers: map[string]string{"Bad Header": "x"}}},
		{"header newline", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: 1, Method: http.MethodGet, Headers: map[string]string{"X-Test": "a\r\nB: c"}}},
		{"body too large", LoadOptions{URL: "http://127.0.0.1:1/", Duration: time.Second, Connections: 1, Method: http.MethodPost, Body: make([]byte, MaxBenchmarkBodyBytes+1)}},
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
