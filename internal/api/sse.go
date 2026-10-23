package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// sseHeaders applies the headers every event stream needs.
func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
}

// handleLogsStream streams new log lines for one site. The subscription is
// created before the response headers are flushed, so a client that observes
// the stream is guaranteed to receive every line written afterwards.
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "log store is not available", nil)
		return
	}
	ref := r.PathValue("ref")
	if ref == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "site reference is required", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, codeInternal, "streaming is not supported", nil)
		return
	}

	// Site routes accept an id or a name. Resolve when possible so a CLI
	// `devstack logs blog` observes the same log file as the dashboard, which
	// uses the id. An unknown reference still streams (empty) rather than
	// turning an EventSource reconnect loop into a hard error.
	streamID := ref
	if s.sites != nil {
		if site, err := s.sites.Get(r.Context(), ref); err == nil && site.ID != "" {
			streamID = site.ID
		}
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
