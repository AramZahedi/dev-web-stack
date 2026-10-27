//go:build !windows

package helper

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

var serviceIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func validateServiceID(id string) error {
	if !serviceIDRegex.MatchString(id) {
		return fmt.Errorf("%w: invalid service ID %q", helperproto.ErrInvalidRequest, id)
	}
	return nil
}

func (s *Server) handleServicesStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceStatus == nil {
		s.notImplemented(w, "ServiceStatus")
		return
	}
	resp, err := s.cfg.Service.ServiceStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleServicesInstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceInstall == nil {
		s.notImplemented(w, "ServiceInstall")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceInstall(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesActivate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceActivate == nil {
		s.notImplemented(w, "ServiceActivate")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceActivate(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesDeactivate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceDeactivate == nil {
		s.notImplemented(w, "ServiceDeactivate")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceDeactivate(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesUninstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceUninstall == nil {
		s.notImplemented(w, "ServiceUninstall")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceUninstall(r.Context(), req.ID, req.DeleteData); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesRestart(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceRestart == nil {
		s.notImplemented(w, "ServiceRestart")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceRestart(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesRotate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceRotateCredentials == nil {
		s.notImplemented(w, "ServiceRotateCredentials")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceRotateCredentials(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesValidate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceValidate == nil {
		s.notImplemented(w, "ServiceValidate")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceValidate(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesReload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceReload == nil {
		s.notImplemented(w, "ServiceReload")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceReload(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesLogs(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceLogs == nil {
		s.notImplemented(w, "ServiceLogs")
		return
	}
	id := r.URL.Query().Get("id")
	var tail int
	if t := r.URL.Query().Get("tail"); t != "" {
		tail, _ = strconv.Atoi(t)
	}
	if id == "" && r.Body != nil && r.ContentLength != 0 {
		var req helperproto.ServiceLogsRequest
		if err := decodeJSON(w, r, &req); err == nil {
			id = req.ID
			tail = req.Tail
		}
	}
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	logs, err := s.cfg.Service.ServiceLogs(r.Context(), id, tail)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, helperproto.ServiceLogsResponse{Logs: logs})
}
