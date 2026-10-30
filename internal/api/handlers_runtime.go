package api

import (
	"context"
	"errors"
	"fmt"
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
	s.recordEvent(r.Context(), "runtime.installed", fmt.Sprintf("runtime %s installed", build.ID))
	writeJSON(w, http.StatusCreated, build)
}

// recordEvent persists a best-effort diagnostic event for dashboard clients.
func (s *Server) recordEvent(ctx context.Context, typ, message string) {
	if s.store == nil {
		return
	}
	_ = s.store.AddEvent(ctx, model.Event{Type: typ, Message: message})
}

// validateRuntimeID rejects transport-level path syntax before the ID reaches a
// runtime service. Registry/catalog membership is enforced by the service.
func validateRuntimeID(id string) error {
	if id == "" || id == "." || id == ".." {
		return errors.New("invalid runtime id")
	}
	if strings.ContainsAny(id, "/\\\x00") || strings.Contains(id, "..") {
		return errors.New("invalid runtime id")
	}
	return nil
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
	if err := validateRuntimeID(buildID); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
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
	s.recordEvent(r.Context(), "runtime.removed", fmt.Sprintf("runtime %s removal completed", buildID))
	w.WriteHeader(http.StatusNoContent)
}
