package proxy

import (
	"errors"
	"net/http"
	"syscall"
	"time"
)

const (
	upstreamRetryAttempts = 20
	upstreamRetryDelay    = 75 * time.Millisecond
)

// retryTransport retries only connection-refused dials for requests without a
// body. A dial failure means no request bytes reached the upstream, so GET/HEAD
// retries are safe; requests with bodies are never replayed.
type retryTransport struct {
	base     http.RoundTripper
	attempts int
	delay    time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	attempts := t.attempts
	if attempts <= 0 {
		attempts = upstreamRetryAttempts
	}
	delay := t.delay
	if delay <= 0 {
		delay = upstreamRetryDelay
	}
	replayable := req.Body == nil || req.Body == http.NoBody
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		if attempt+1 >= attempts || !replayable || !isConnectionRefused(err) {
			return nil, err
		}
		time.Sleep(delay)
	}
}

func isConnectionRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && uintptr(errno) == 10061 {
		return true
	}
	return false
}
