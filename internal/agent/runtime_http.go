package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

const maxAgentRequestBytes = 1 << 20

func (s *Server) mountRuntime(mux *http.ServeMux, runtime *Runtime) {
	mux.HandleFunc(agentproto.PathPortsAllocate, func(w http.ResponseWriter, r *http.Request) { handlePortAllocate(w, r, runtime) })
	mux.HandleFunc(agentproto.PathPortsRelease, func(w http.ResponseWriter, r *http.Request) { handlePortRelease(w, r, runtime) })
	mux.HandleFunc(agentproto.PathPortsValidate, func(w http.ResponseWriter, r *http.Request) { handlePortValidate(w, r, runtime) })
	mux.HandleFunc(agentproto.PathRuntimes, func(w http.ResponseWriter, r *http.Request) { handleRuntimes(w, r, runtime) })
	mux.HandleFunc(agentproto.PathRuntimesRemove, func(w http.ResponseWriter, r *http.Request) { handleRuntimesRemove(w, r, runtime) })
	mux.HandleFunc(agentproto.PathRuntimesList, func(w http.ResponseWriter, r *http.Request) { handleRuntimesList(w, r, runtime) })
	mux.HandleFunc("/v1/sites/", func(w http.ResponseWriter, r *http.Request) { handleSite(w, r, runtime) })
	mux.HandleFunc(agentproto.PathSourceCopy, func(w http.ResponseWriter, r *http.Request) { handleSourceCopy(w, r, runtime) })
}

func handleSourceCopy(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var req agentproto.SourceCopyRequest
	if err := decodeAgentJSON(w, r, &req); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	resp, err := runtime.CopySource(r.Context(), req)
	if err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleRuntimes(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var req agentproto.RuntimeRequest
	if err := decodeAgentJSON(w, r, &req); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	resp, err := runtime.InstallRuntime(r.Context(), req)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleRuntimesRemove(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var req agentproto.RuntimeRequest
	if err := decodeAgentJSON(w, r, &req); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := runtime.RemoveRuntime(r.Context(), req); err != nil {
		writeRuntimeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleRuntimesList(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodGet {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
		return
	}
	resp, err := runtime.ListRuntimes(r.Context())
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handlePortAllocate(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var request agentproto.PortRequest
	if err := decodeAgentJSON(w, r, &request); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	response := agentproto.PortResponse{}
	var err error
	switch kind {
	case "http":
		response.HTTPPort, response.Reused, err = runtime.ports.Allocate(request.SiteID, "http", firstPort(request.RequestedHTTP, request.HTTPPort))
	case "fastcgi":
		response.FastCGIPort, response.Reused, err = runtime.ports.Allocate(request.SiteID, "fastcgi", firstPort(request.RequestedFastCGI, request.FastCGIPort))
	case "site":
		response.HTTPPort, response.Reused, err = runtime.ports.Allocate(request.SiteID, "http", firstPort(request.RequestedHTTP, request.HTTPPort))
		if err == nil {
			var reused bool
			response.FastCGIPort, reused, err = runtime.ports.Allocate(request.SiteID, "fastcgi", firstPort(request.RequestedFastCGI, request.FastCGIPort))
			response.Reused = response.Reused || reused
		}
		if err != nil {
			_ = runtime.ports.Release(request.SiteID, 0)
		}
	default:
		err = fmt.Errorf("agent: unsupported port kind %q", request.Kind)
	}
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func handlePortRelease(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var request agentproto.PortRequest
	if err := decodeAgentJSON(w, r, &request); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	port := 0
	if strings.EqualFold(request.Kind, "http") {
		port = firstPort(request.HTTPPort, request.RequestedHTTP)
	} else if strings.EqualFold(request.Kind, "fastcgi") {
		port = firstPort(request.FastCGIPort, request.RequestedFastCGI)
	}
	if err := runtime.ReleasePorts(request.SiteID, port); err != nil {
		writeRuntimeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handlePortValidate(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var request agentproto.PortRequest
	if err := decodeAgentJSON(w, r, &request); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	port := firstPort(request.RequestedHTTP, request.HTTPPort)
	if kind == "fastcgi" {
		port = firstPort(request.RequestedFastCGI, request.FastCGIPort)
	}
	valid, err := runtime.ports.Validate(request.SiteID, kind, port)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentproto.PortValidateResponse{Valid: valid})
}

func handleSite(w http.ResponseWriter, r *http.Request, runtime *Runtime) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/sites/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || len(parts) > 3 {
		writeAgentError(w, http.StatusNotFound, agentproto.CodeNotFound, "site endpoint not found")
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || validateSiteID(id) != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, "invalid site ID")
		return
	}

	if len(parts) == 3 {
		if parts[1] != "php" {
			writeAgentError(w, http.StatusNotFound, agentproto.CodeNotFound, "site endpoint not found")
			return
		}
		switch parts[2] {
		case "prepare":
			if r.Method != http.MethodPost {
				writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
				return
			}
			var request agentproto.PHPPrepareRequest
			if err := decodeAgentJSON(w, r, &request); err != nil {
				writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
				return
			}
			response, err := runtime.PreparePHP(r.Context(), id, request)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
		case "start":
			if r.Method != http.MethodPost {
				writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
				return
			}
			var request agentproto.PHPActionRequest
			_ = decodeAgentJSON(w, r, &request)
			response, err := runtime.StartPHP(r.Context(), id, request)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
		case "stop":
			if r.Method != http.MethodPost {
				writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
				return
			}
			var request agentproto.PHPActionRequest
			_ = decodeAgentJSON(w, r, &request)
			response, err := runtime.StopPHP(r.Context(), id, request)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
		default:
			writeAgentError(w, http.StatusNotFound, agentproto.CodeNotFound, "site endpoint not found")
		}
		return
	}

	action := parts[1]
	switch action {
	case "prepare":
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
			return
		}
		var request agentproto.SitePrepareRequest
		if err := decodeAgentJSON(w, r, &request); err != nil {
			writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
			return
		}
		if request.Site.ID != id {
			writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, "site ID in path and body differ")
			return
		}
		response, err := runtime.PrepareSite(r.Context(), request.Site)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "start":
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
			return
		}
		response, err := runtime.StartSite(r.Context(), id)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "stop":
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
			return
		}
		if err := runtime.StopSite(r.Context(), id, false); err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, agentproto.SiteActionResponse{Status: "stopped"})
	case "status":
		if r.Method != http.MethodGet {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
			return
		}
		response, err := runtime.SiteStatus(id)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "logs":
		if r.Method != http.MethodGet {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
			return
		}
		tail := 200
		if raw := r.URL.Query().Get("tail"); raw != "" {
			tail, err = strconv.Atoi(raw)
			if err != nil || tail < 0 {
				writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, "invalid tail count")
				return
			}
		}
		stream, err := runtime.SiteLogs(r.Context(), id, r.URL.Query().Get("follow") == "true", tail)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		defer stream.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := io.Copy(w, stream); err != nil {
			return
		}
	case "exec":
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
			return
		}
		var request agentproto.SiteExecRequest
		if err := decodeAgentJSON(w, r, &request); err != nil {
			writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
			return
		}
		response, err := runtime.ExecSite(r.Context(), id, request)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "build":
		if r.Method != http.MethodPost {
			writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
			return
		}
		var request agentproto.BuildRequest
		if err := decodeAgentJSON(w, r, &request); err != nil {
			writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
			return
		}
		response, err := runtime.BuildSite(r.Context(), id, request)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "terminal":
		if r.Method == http.MethodPost {
			var request agentproto.TerminalTicketRequest
			_ = decodeAgentJSON(w, r, &request)
			response, err := runtime.IssueTerminalTicket(id, request)
			if err != nil {
				writeRuntimeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
		if r.Method == http.MethodGet {
			runtime.HandleTerminalWebSocket(w, r, id)
			return
		}
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET or POST required")
	default:
		writeAgentError(w, http.StatusNotFound, agentproto.CodeNotFound, "site endpoint not found")
	}
}

func decodeAgentJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeRuntimeError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, agentproto.CodeInvalidRequest
	switch {
	case errors.Is(err, ErrSiteNotFound):
		status, code = http.StatusNotFound, agentproto.CodeNotFound
	case errors.Is(err, ErrPortUnavailable):
		status, code = http.StatusConflict, agentproto.CodeConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusRequestTimeout, agentproto.CodeTimeout
	}
	writeAgentError(w, status, code, err.Error())
}

func firstPort(first, second int) int {
	if first != 0 {
		return first
	}
	return second
}
