package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type listServicesResponse struct {
	Services []services.View `json:"services"`
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service coordinator is not available", nil)
		return
	}
	list, err := s.services.List(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if list == nil {
		list = []services.View{}
	}
	writeJSON(w, http.StatusOK, listServicesResponse{Services: list})
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service coordinator is not available", nil)
		return
	}
	id := r.PathValue("id")
	view, err := s.services.Get(r.Context(), services.ID(id))
	if err != nil {
		if errors.Is(err, services.ErrNotFound) || errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, fmt.Sprintf("service %q not found", id), nil)
			return
		}
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type serviceActionFunc func(ctx context.Context, id services.ID) error

func (s *Server) runServiceAction(w http.ResponseWriter, r *http.Request, action serviceActionFunc) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service manager is not available", nil)
		return
	}
	id := services.ID(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "service id is required", nil)
		return
	}
	if err := action(r.Context(), id); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInstallService(w http.ResponseWriter, r *http.Request) {
	s.runServiceAction(w, r, func(ctx context.Context, id services.ID) error {
		return s.services.Install(ctx, id)
	})
}

func (s *Server) handleActivateService(w http.ResponseWriter, r *http.Request) {
	s.runServiceAction(w, r, func(ctx context.Context, id services.ID) error {
		return s.services.Activate(ctx, id)
	})
}

func (s *Server) handleDeactivateService(w http.ResponseWriter, r *http.Request) {
	s.runServiceAction(w, r, func(ctx context.Context, id services.ID) error {
		return s.services.Deactivate(ctx, id)
	})
}

func (s *Server) handleRestartService(w http.ResponseWriter, r *http.Request) {
	s.runServiceAction(w, r, func(ctx context.Context, id services.ID) error {
		return s.services.Restart(ctx, id)
	})
}

func (s *Server) handleUninstallService(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service manager is not available", nil)
		return
	}
	var req struct {
		DeleteData bool `json:"delete_data"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}
	id := services.ID(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "service id is required", nil)
		return
	}
	result, err := s.services.Uninstall(r.Context(), id, req.DeleteData)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service manager is not available", nil)
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
	id := services.ID(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "service id is required", nil)
		return
	}
	logs, err := s.services.Logs(r.Context(), id, tail)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *Server) handleRevealServiceCredentials(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service manager is not available", nil)
		return
	}
	id := services.ID(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "service id is required", nil)
		return
	}
	creds, err := s.services.Credentials(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, creds)
}

func (s *Server) handleRotateServiceCredentials(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "service manager is not available", nil)
		return
	}
	id := services.ID(strings.TrimSpace(r.PathValue("id")))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "service id is required", nil)
		return
	}
	creds, err := s.services.RotateCredentials(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, creds)
}
