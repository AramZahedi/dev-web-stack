package api

import (
	"errors"
	"net/http"

	"github.com/AramZahedi/dev-web-stack/internal/performance"
)

func (s *Server) handlePerformanceStatus(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	status, err := s.performance.Status(r.Context())
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handlePerformanceProfile(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	var req performance.ProfileRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	status, err := s.performance.SetProfile(r.Context(), req)
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handlePerformancePreview(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	preview, err := s.performance.Preview(r.Context())
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handlePerformanceApply(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	var req performance.ApplyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.performance.Apply(r.Context(), req)
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePerformanceReset(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	var req performance.ResetRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.performance.Reset(r.Context(), req)
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	var req performance.BenchmarkRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.performance.RunBenchmark(r.Context(), req)
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListBenchmarks(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	results, err := s.performance.ListBenchmarks(r.Context())
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	if results == nil {
		results = []performance.BenchmarkResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleGetBenchmark(w http.ResponseWriter, r *http.Request) {
	if s.performance == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "performance service is not available", nil)
		return
	}
	result, err := s.performance.GetBenchmark(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writePerformanceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writePerformanceError preserves the per-step apply/reset result in the error
// details while keeping the stable API envelope.
func (s *Server) writePerformanceError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message, details := classifyError(err)
	var applyErr *performance.ApplyError
	if errors.As(err, &applyErr) {
		details = map[string]any{"result": applyErr.Result}
	}
	var resetErr *performance.ResetError
	if errors.As(err, &resetErr) {
		details = map[string]any{"result": resetErr.Result}
	}
	if status >= http.StatusInternalServerError {
		s.logger.Error("performance request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err.Error(),
		)
	}
	writeError(w, status, code, message, details)
}
