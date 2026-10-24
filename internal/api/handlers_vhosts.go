package api

import (
	"net/http"
	"strings"
)

type vhostModeRequest struct {
	Mode string `json:"mode"`
}

type vhostPreviewResponse struct {
	Server  string `json:"server"`
	Site    string `json:"site"`
	Content string `json:"content"`
}

func (s *Server) handleVhostStatus(w http.ResponseWriter, r *http.Request) {
	if s.vhosts == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "vhost service is not available", nil)
		return
	}
	status, err := s.vhosts.VhostStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleVhostMode(w http.ResponseWriter, r *http.Request) {
	if s.vhosts == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "vhost service is not available", nil)
		return
	}
	var req vhostModeRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "builtin", "nginx", "apache":
	default:
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "mode must be builtin, nginx, or apache", nil)
		return
	}
	if err := s.vhosts.SetServingMode(r.Context(), mode); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	status, err := s.vhosts.VhostStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleVhostSync(w http.ResponseWriter, r *http.Request) {
	if s.vhosts == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "vhost service is not available", nil)
		return
	}
	if err := s.vhosts.SyncVhosts(r.Context()); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	status, err := s.vhosts.VhostStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleVhostPreview(w http.ResponseWriter, r *http.Request) {
	if s.vhosts == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "vhost service is not available", nil)
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("site"))
	server := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("server")))
	if site == "" || server == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "site and server query parameters are required", nil)
		return
	}
	switch server {
	case "nginx", "apache":
	default:
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "server must be nginx or apache", nil)
		return
	}
	content, err := s.vhosts.PreviewVhost(r.Context(), site, server)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vhostPreviewResponse{Server: server, Site: site, Content: content})
}
