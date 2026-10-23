package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	s.runDoctor(w, r, false)
}

func (s *Server) handleDoctorRun(w http.ResponseWriter, r *http.Request) {
	s.runDoctor(w, r, false)
}

func (s *Server) handleDoctorRepair(w http.ResponseWriter, r *http.Request) {
	s.runDoctor(w, r, true)
}

func (s *Server) runDoctor(w http.ResponseWriter, r *http.Request, repair bool) {
	if s.doctor == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "doctor service is not available", nil)
		return
	}
	var (
		report doctor.Report
		err    error
	)
	if repair {
		report, err = s.doctor.Repair(r.Context())
	} else {
		report, err = s.doctor.Run(r.Context())
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if report.Checks == nil {
		report.Checks = []doctor.Check{}
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleTrustStatus(w http.ResponseWriter, r *http.Request) {
	s.writeTrust(w, r, s.helperTrustStatus)
}

func (s *Server) handleTrustRepair(w http.ResponseWriter, r *http.Request) {
	s.writeTrust(w, r, s.helperTrustInstall)
}

func (s *Server) handleTrustRemove(w http.ResponseWriter, r *http.Request) {
	s.writeTrust(w, r, s.helperTrustRemove)
}

func (s *Server) writeTrust(w http.ResponseWriter, r *http.Request, op func(context.Context, string) ([]trust.Target, error)) {
	if s.helper == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "helper is not available", nil)
		return
	}
	home := strings.TrimSpace(s.userHome())
	if home == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "user home is not configured", nil)
		return
	}
	targets, err := op(r.Context(), home)
	if err != nil {
		s.writeHelperError(w, r, err)
		return
	}
	if targets == nil {
		targets = []trust.Target{}
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) helperTrustStatus(ctx context.Context, home string) ([]trust.Target, error) {
	return s.helper.TrustStatus(ctx, home)
}

func (s *Server) helperTrustInstall(ctx context.Context, home string) ([]trust.Target, error) {
	return s.helper.InstallTrust(ctx, home)
}

func (s *Server) helperTrustRemove(ctx context.Context, home string) ([]trust.Target, error) {
	return s.helper.RemoveTrust(ctx, home)
}

// writeHelperError reports an unreachable or failing privileged helper as a
// bad-gateway rather than an internal daemon error.
func (s *Server) writeHelperError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Warn("helper request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusBadGateway, codeHelperUnavailable, err.Error(), nil)
}
