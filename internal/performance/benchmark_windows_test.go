package performance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateLoadOptionsAcceptsTestDomains(t *testing.T) {
	opts := LoadOptions{
		URL:         "https://mycoolsite.test/",
		Duration:    time.Second,
		Connections: 10,
	}
	if err := validateLoadOptions(opts); err != nil {
		t.Fatalf("validateLoadOptions(%q) error = %v, want nil", opts.URL, err)
	}

	optsWithPort := LoadOptions{
		URL:         "https://mycoolsite.test:8443/api",
		Duration:    time.Second,
		Connections: 5,
	}
	if err := validateLoadOptions(optsWithPort); err != nil {
		t.Fatalf("validateLoadOptions(%q) error = %v, want nil", optsWithPort.URL, err)
	}

	optsNonLoopback := LoadOptions{
		URL:         "https://google.com/",
		Duration:    time.Second,
		Connections: 10,
	}
	if err := validateLoadOptions(optsNonLoopback); err == nil {
		t.Fatalf("validateLoadOptions(%q) accepted non-loopback non-test host", optsNonLoopback.URL)
	}
}

func TestRunLoadAgainstTestDomain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	// Use custom load options targeting the test server URL
	res, raw, err := RunLoad(context.Background(), LoadOptions{
		URL:         server.URL,
		Duration:    100 * time.Millisecond,
		Connections: 2,
	})
	if err != nil {
		t.Fatalf("RunLoad error: %v", err)
	}
	if res.Requests <= 0 {
		t.Errorf("res.Requests = %d, want > 0", res.Requests)
	}
	if len(raw) == 0 {
		t.Errorf("raw log is empty")
	}
}
