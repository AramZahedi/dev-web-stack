package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// logSnapshotLines is the number of recent log lines returned by
// follow=false. It matches the backend's tail size.
const logSnapshotLines = 5000

// sseHeaders applies the headers every event stream needs.
func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
}

// handleLogsStream returns recent log lines for follow=false and streams new
// lines for follow=true (the default). The live subscription is created before
// the response headers are flushed, so a client that observes the stream is
// guaranteed to receive every line written afterwards.
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	provider, hasProvider := s.sites.(siteLogProvider)
	if !hasProvider && s.logs == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "log store is not available", nil)
		return
	}
	ref := r.PathValue("ref")
	if ref == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "site reference is required", nil)
		return
	}

	follow := true
	if raw := strings.TrimSpace(r.URL.Query().Get("follow")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "follow must be true or false", nil)
			return
		}
		follow = parsed
	}

	// Site routes accept an id or a name. Resolve when possible so a CLI
	// `devstack logs blog` observes the same log file as the dashboard, which
	// uses the id. An unknown reference still returns an empty stream, but it
	// must not create a permanent in-memory log registry entry for an arbitrary
	// caller-controlled id.
	streamID := ref
	known := s.sites == nil
	if s.sites != nil {
		if site, err := s.sites.Get(r.Context(), ref); err == nil && site.ID != "" {
			streamID = site.ID
			known = true
		}
	}
	if !known {
		sseHeaders(w)
		w.WriteHeader(http.StatusOK)
		return
	}
	if hasProvider {
		s.streamSiteLogs(w, r, provider, streamID, follow)
		return
	}

	if !follow {
		lines, err := s.logs.Tail(streamID, logSnapshotLines)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "logs are unavailable: "+err.Error(), nil)
			return
		}
		sseHeaders(w)
		w.WriteHeader(http.StatusOK)
		for _, line := range lines {
			data, err := json.Marshal(line)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, codeInternal, "streaming is not supported", nil)
		return
	}

	lines, unsubscribe := s.logs.Subscribe(streamID)
	defer unsubscribe()

	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	keepAlive := time.NewTicker(tickerInterval(s.keepAliveInterval, defaultKeepAlive))
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case line, open := <-lines:
			if !open {
				return
			}
			data, err := json.Marshal(line)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// siteLogProvider is implemented by site services that can read the backend's
// own log stream. The Windows daemon uses this to surface WSL agent logs
// instead of the (empty) local log store.
type siteLogProvider interface {
	Logs(ctx context.Context, ref string, follow bool) (io.ReadCloser, error)
}

// providerLogLine turns one backend log line into the dashboard/CLI shape. The
// agent prefixes every line with an RFC3339 timestamp; split it so the UI can
// show the time column and the message separately.
func providerLogLine(raw string) logs.Line {
	if stamp, text, ok := strings.Cut(strings.TrimSpace(raw), " "); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			return logs.Line{Time: parsed, Text: text}
		}
	}
	return logs.Line{Text: raw}
}

func (s *Server) streamSiteLogs(w http.ResponseWriter, r *http.Request, provider siteLogProvider, siteID string, follow bool) {
	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	if !follow {
		reader, err := provider.Logs(r.Context(), siteID, false)
		if err != nil {
			return
		}
		defer func() { _ = reader.Close() }()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for lines := 0; lines < logSnapshotLines && scanner.Scan(); lines++ {
			data, err := json.Marshal(providerLogLine(scanner.Text()))
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
		return
	}

	// Poll finite snapshots instead of relying on the backend's live log
	// stream. This works identically for the local supervisor and the WSL
	// agent, and keeps the SSE response open even when the backend writes no
	// new data for a while.
	seen := make(map[string]bool)
	emit := func() {
		reader, err := provider.Logs(r.Context(), siteID, false)
		if err != nil {
			return
		}
		defer func() { _ = reader.Close() }()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if seen[line] {
				continue
			}
			seen[line] = true
			data, err := json.Marshal(providerLogLine(line))
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit()

	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	keepAlive := time.NewTicker(tickerInterval(s.keepAliveInterval, defaultKeepAlive))
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			emit()
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// handleEvents streams diagnostic events added after the client connected.
// It polls the store once per interval and emits new event IDs in order.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, codeInternal, "streaming is not supported", nil)
		return
	}

	var cursor int64
	if s.store != nil {
		if events, err := s.store.ListEvents(r.Context(), 200); err == nil {
			cursor = newestEventID(events)
		}
	}

	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	poll := time.NewTicker(tickerInterval(s.eventPollInterval, defaultEventPoll))
	defer poll.Stop()
	keepAlive := time.NewTicker(tickerInterval(s.keepAliveInterval, defaultKeepAlive))
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if s.store == nil {
				continue
			}
			events, err := s.store.ListEvents(r.Context(), 200)
			if err != nil {
				s.logger.Warn("events: list", "error", err)
				continue
			}
			// The store returns newest first; SSE consumers expect the order in
			// which the events happened.
			sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
			for _, event := range events {
				if event.ID <= cursor {
					continue
				}
				data, err := json.Marshal(event)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return
				}
				cursor = event.ID
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// newestEventID returns the largest ID in events, or zero for an empty list.
func newestEventID(events []model.Event) int64 {
	var newest int64
	for _, event := range events {
		if event.ID > newest {
			newest = event.ID
		}
	}
	return newest
}

// tickerInterval keeps a zero or negative test override from panicking.
func tickerInterval(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
