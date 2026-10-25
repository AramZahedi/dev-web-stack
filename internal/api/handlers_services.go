package api

import (
	"errors"
	"fmt"
	"net/http"

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
