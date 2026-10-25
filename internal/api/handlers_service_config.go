package api

import (
	"net/http"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func (s *Server) serviceConfigAvailable(w http.ResponseWriter) bool {
	if s.serviceConfig != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable,
		"service config manager is not available", nil)
	return false
}

func serviceConfigID(w http.ResponseWriter, r *http.Request) (services.ID, bool) {
	id := services.ID(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "service id is required", nil)
		return "", false
	}
	return id, true
}

func (s *Server) handleServiceConfigList(w http.ResponseWriter, r *http.Request) {
	if !s.serviceConfigAvailable(w) {
		return
	}
	id, ok := serviceConfigID(w, r)
	if !ok {
		return
	}
	listing, err := s.serviceConfig.List(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if listing.Files == nil {
		listing.Files = []servicecontrol.ConfigStatus{}
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Server) handleServiceConfigContent(w http.ResponseWriter, r *http.Request) {
	if !s.serviceConfigAvailable(w) {
		return
	}
	id, ok := serviceConfigID(w, r)
	if !ok {
		return
	}
	content, err := s.serviceConfig.Content(r.Context(), id, strings.TrimSpace(r.URL.Query().Get("path")))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, content)
}

func (s *Server) handleServiceConfigDiff(w http.ResponseWriter, r *http.Request) {
	if !s.serviceConfigAvailable(w) {
		return
	}
	id, ok := serviceConfigID(w, r)
	if !ok {
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	diff, err := s.serviceConfig.Diff(r.Context(), id, req.Path, req.Content)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) handleServiceConfigApply(w http.ResponseWriter, r *http.Request) {
	if !s.serviceConfigAvailable(w) {
		return
	}
	id, ok := serviceConfigID(w, r)
	if !ok {
		return
	}
	var req servicecontrol.ConfigApplyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.serviceConfig.Apply(r.Context(), id, req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleServiceConfigReset(w http.ResponseWriter, r *http.Request) {
	if !s.serviceConfigAvailable(w) {
		return
	}
	id, ok := serviceConfigID(w, r)
	if !ok {
		return
	}
	var req servicecontrol.ConfigResetRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.serviceConfig.Reset(r.Context(), id, req.Path)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
