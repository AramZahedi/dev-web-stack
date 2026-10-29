package api

import (
	"context"
	"net/http"

	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// nodeDefaultService is the narrow Node CLI default surface used by
// handleNodeDefault. Tool services that predate the Node default surface do
// not implement it, so the handler reports service unavailable instead of
// requiring every fake and implementation to grow the method at once.
type nodeDefaultService interface {
	NodeDefault(ctx context.Context) (tooling.NodeDefaultStatus, error)
}

// handleNodeDefault reports the machine-wide Node CLI default. The route is
// registered by the daemon wiring; the mutation half (PUT/DELETE) is owned by
// a later task.
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
