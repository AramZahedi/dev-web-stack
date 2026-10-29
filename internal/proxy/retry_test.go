package proxy

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

type scriptedTransport struct {
	calls  int
	fail   int
	status int
	err    error
}

func (s *scriptedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	s.calls++
	if s.calls <= s.fail {
		return nil, s.err
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func TestUpstreamRetryWindowCoversWSLForwardingFlap(t *testing.T) {
	if window := time.Duration(upstreamRetryAttempts-1) * upstreamRetryDelay; window < time.Second {
		t.Fatalf("retry window = %v, want at least 1s", window)
	}
}

func TestRetryTransportRetriesRefusedBodylessRequests(t *testing.T) {
	base := &scriptedTransport{
		fail: 2,
		err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: syscall.Errno(10061),
		},
	}
	transport := &retryTransport{base: base, attempts: 3, delay: time.Millisecond}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if base.calls != 3 {
		t.Fatalf("attempts = %d, want 3", base.calls)
	}
}

func TestRetryTransportDoesNotReplayRequestBodies(t *testing.T) {
	base := &scriptedTransport{
		fail: 9,
		err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: syscall.Errno(10061),
		},
	}
	transport := &retryTransport{base: base, attempts: 3, delay: time.Millisecond}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/", strings.NewReader("payload"))
	resp, err := transport.RoundTrip(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("RoundTrip succeeded with a failing transport")
	}
	if base.calls != 1 {
		t.Fatalf("attempts = %d, want 1 for a request with a body", base.calls)
	}
}

func TestRetryTransportDoesNotRetryOtherErrors(t *testing.T) {
	base := &scriptedTransport{fail: 9, err: errors.New("boom")}
	transport := &retryTransport{base: base, attempts: 3, delay: time.Millisecond}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip succeeded with a failing transport")
	}
	if base.calls != 1 {
		t.Fatalf("attempts = %d, want 1 for a non-dial error", base.calls)
	}
}
