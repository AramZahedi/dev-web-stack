package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// nodeDefaultService is the narrow Node CLI default read surface used by
// handleNodeDefault. Tool services that predate the Node default surface do
// not implement it, so the handler reports service unavailable instead of
// requiring every fake and implementation to grow the method at once.
type nodeDefaultService interface {
	NodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error)
}

// nodeDefaultMutator is the narrow Node CLI default mutation surface. It is
// asserted separately so the status endpoint stays readable when a service
// only implements the read half.
type nodeDefaultMutator interface {
	SetNodeDefault(ctx context.Context, value string, force bool) (tooling.NodeDefaultStatus, error)
	ClearNodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error)
}

// nodeDefaultRequest accepts either a value, an explicit clear, or force.
type nodeDefaultRequest struct {
	Value string `json:"value"`
	Clear bool   `json:"clear"`
	Force bool   `json:"force"`
}

// handleNodeDefault reports the machine-wide Node CLI default. The route is
// registered by the daemon wiring.
func (s *Server) handleNodeDefault(w http.ResponseWriter, r *http.Request) {
	service, ok := s.tooling.(nodeDefaultService)
	if s.tooling == nil || !ok {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	status, err := service.NodeDefault(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleSetNodeDefault sets or clears the machine-wide Node CLI default.
func (s *Server) handleSetNodeDefault(w http.ResponseWriter, r *http.Request) {
	service, ok := s.tooling.(nodeDefaultMutator)
	if s.tooling == nil || !ok {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "tooling is not available", nil)
		return
	}
	var req nodeDefaultRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var (
		status tooling.NodeDefaultStatus
		err    error
	)
	if req.Clear {
		status, err = service.ClearNodeDefault(r.Context())
	} else {
		if strings.TrimSpace(req.Value) == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "value or clear is required", nil)
			return
		}
		status, err = service.SetNodeDefault(r.Context(), req.Value, req.Force)
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
