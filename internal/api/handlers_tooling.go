package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

// phpDefaultRequest accepts either a value, an explicit clear, or force.
type phpDefaultRequest struct {
	Value string `json:"value"`
	Clear bool   `json:"clear"`
	Force bool   `json:"force"`
}

type globalIniRequest struct {
	Content string `json:"content"`
}

type toolActionRequest struct {
	Force bool `json:"force"`
}

func (s *Server) handlePHPDefault(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	status, err := s.tooling.PHPDefault(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSetPHPDefault(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	var req phpDefaultRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var (
		status interface{}
		err    error
	)
	if req.Clear {
		status, err = s.tooling.ClearPHPDefault(r.Context())
	} else {
		if strings.TrimSpace(req.Value) == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "value or clear is required", nil)
			return
		}
		status, err = s.tooling.SetPHPDefault(r.Context(), req.Value, req.Force)
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGlobalIni(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	ini, err := s.tooling.GlobalIni(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ini)
}

func (s *Server) handleSaveGlobalIni(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	var req globalIniRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	ini, err := s.tooling.SaveGlobalIni(r.Context(), req.Content)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ini)
}

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	tools, err := s.tooling.Tools(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if tools == nil {
		tools = []helperproto.ToolStatus{}
	}
	writeJSON(w, http.StatusOK, struct {
		Tools []helperproto.ToolStatus `json:"tools"`
	}{Tools: tools})
}

func (s *Server) handleInstallTool(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	var req toolActionRequest
	if r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}
	tool, err := s.tooling.InstallTool(r.Context(), r.PathValue("id"), req.Force)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tool)
}

func (s *Server) handleRemoveTool(w http.ResponseWriter, r *http.Request) {
	if s.tooling == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	tool, err := s.tooling.RemoveTool(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tool)
}

func (s *Server) handleBuildSite(w http.ResponseWriter, r *http.Request) {
	if s.build == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "build service is not available", nil)
		return
	}
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	site, err := s.sites.Get(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	result, err := s.build.Build(r.Context(), site)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBuildLogs(w http.ResponseWriter, r *http.Request) {
	if s.build == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "build service is not available", nil)
		return
	}
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	tail := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("tail")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "tail must be a non-negative integer", nil)
			return
		}
		tail = parsed
	}
	site, err := s.sites.Get(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	logs, err := s.build.Logs(r.Context(), site, tail)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}
