package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type installRuntimeRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	if s.runtimes == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "runtime service is not available", nil)
		return
	}
	builds, err := s.runtimes.List(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if builds == nil {
		builds = []model.RuntimeBuild{}
	}
	writeJSON(w, http.StatusOK, builds)
}

func (s *Server) handleAvailableRuntimes(w http.ResponseWriter, _ *http.Request) {
	if s.runtimes == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "runtime service is not available", nil)
		return
	}
	entries := s.runtimes.Available()
	if entries == nil {
		entries = []catalog.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleInstallRuntime(w http.ResponseWriter, r *http.Request) {
	if s.runtimes == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "runtime service is not available", nil)
		return
	}
	var req installRuntimeRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "runtime id is required", nil)
		return
	}
	build, err := s.runtimes.Install(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, build)
}

func (s *Server) handleRemoveRuntime(w http.ResponseWriter, r *http.Request) {
	if s.runtimes == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "runtime service is not available", nil)
		return
	}
	buildID := strings.TrimSpace(r.PathValue("id"))
	if buildID == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "runtime id is required", nil)
		return
	}
	if s.sites != nil {
		// The OpenAPI contract defines DELETE as idempotent. A build that is
		// already absent cannot be in use, so let the runtime manager return its
		// no-op success instead of surfacing a 404 from the in-use check.
		if err := s.sites.CheckRuntimeRemoval(r.Context(), buildID); err != nil &&
			!errors.Is(err, store.ErrNotFound) {
			s.writeServiceError(w, r, err)
			return
		}
	}
	if err := s.runtimes.Remove(r.Context(), buildID); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
