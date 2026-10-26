package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/terminal"
)

// handleTerminalSession issues a short-lived, one-use ticket for the site's PTY
// WebSocket. It is a normal API mutation, so the session cookie and CSRF token
// are enforced by the authenticate middleware before this handler runs.
func (s *Server) handleTerminalSession(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil || s.terminal == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site terminal is not available", nil)
		return
	}
	site, err := s.sites.Get(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	ticket, err := s.terminal.IssueTicket(r.Context(), site)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ticket)
}

// handleTerminalWebSocket upgrades a ticket-authenticated request to the PTY
// protocol. The ticket is the capability; Origin is validated independently so
// a foreign page cannot open a shell even if it learns a ticket. The request is
// a GET, so the normal read-only session rules apply at the middleware layer.
func (s *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil || s.terminal == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site terminal is not available", nil)
		return
	}
	if !s.validTerminalOrigin(r) {
		writeError(w, http.StatusForbidden, codeForbidden, "terminal WebSocket origin is not allowed", nil)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if token == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "terminal ticket is required", nil)
		return
	}
	site, err := s.sites.Get(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if err := s.terminal.Serve(r.Context(), w, r, site, token); err != nil {
		if errors.Is(err, terminal.ErrUpgrade) {
			// websocket.Accept already wrote a handshake error status; never
			// append a JSON body to a hijacked or half-written response.
			s.logger.Warn("terminal WebSocket upgrade failed",
				"path", r.URL.Path,
				"error", err,
			)
			return
		}
		s.writeServiceError(w, r, err)
	}
}

// validTerminalOrigin requires an Origin header whose host is in the same
// allowlist as the Host guard. A missing or "null" Origin is rejected: browsers
// always send Origin for WebSocket handshakes, so its absence indicates a
// non-browser client rather than the loopback dashboard.
func (s *Server) validTerminalOrigin(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host, ok := requestHost(parsed.Host)
	if !ok {
		return false
	}
	_, allowed := s.allowedHosts[host]
	return allowed
}
