//go:build !windows

package helper

import (
	"net/http"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func (s *Server) handlePerformanceStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.PerformanceStatus == nil {
		s.notImplemented(w, "PerformanceStatus")
		return
	}
	resp, err := s.cfg.Service.PerformanceStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePerformanceApply(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.PerformanceApply == nil {
		s.notImplemented(w, "PerformanceApply")
		return
	}
	var req helperproto.PerformanceApplyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	resp, err := s.cfg.Service.PerformanceApply(r.Context(), req)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePerformanceReset(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.PerformanceReset == nil {
		s.notImplemented(w, "PerformanceReset")
		return
	}
	// The request carries no path or content. An absent body is valid.
	resp, err := s.cfg.Service.PerformanceReset(r.Context(), helperproto.PerformanceResetRequest{})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}
