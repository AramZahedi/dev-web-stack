package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
		return
	}
	capabilities := append([]string(nil), s.config.Capabilities...)
	if s.docker != nil {
		if _, err := s.docker.Health(r.Context()); err != nil {
			writeAgentError(w, http.StatusServiceUnavailable, agentproto.CodeInternal, fmt.Sprintf("agent readiness check failed: %v", err))
			return
		}
		capabilities = append(capabilities, "docker", "compose")
	}
	writeJSON(w, http.StatusOK, agentproto.HealthResponse{
		Version: s.config.Version, Distro: s.config.Distro, Root: s.config.Root,
		Capabilities: capabilities,
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
		return
	}
	writeJSON(w, http.StatusOK, agentproto.VersionResponse{Protocol: agentproto.ProtocolVersion, Agent: s.config.Version})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
		return
	}
	writeJSON(w, http.StatusOK, agentproto.CapabilitiesResponse{Capabilities: append([]string(nil), s.config.Capabilities...)})
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "POST required")
		return
	}
	var request agentproto.HostsRequest
	if err := decodeAgentJSON(w, r, &request); err != nil {
		writeAgentError(w, http.StatusBadRequest, agentproto.CodeInvalidRequest, err.Error())
		return
	}
	gateway, err := DiscoverWindowsGateway(r.Context())
	if err != nil {
		writeAgentError(w, http.StatusServiceUnavailable, agentproto.CodeInternal, err.Error())
		return
	}
	changed, err := s.hosts.Sync(request.Domains, gateway.String())
	if err != nil {
		status, code := http.StatusBadRequest, agentproto.CodeInvalidRequest
		if errors.Is(err, ErrRootRequired) {
			status, code = http.StatusForbidden, agentproto.CodeForbidden
		}
		writeAgentError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentproto.HostsResponse{Changed: changed})
}

func (s *Server) handleCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
		return
	}
	response, err := CAResponse(s.config.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeAgentError(w, http.StatusNotFound, agentproto.CodeNotFound, "CA bundle is not installed")
			return
		}
		writeAgentError(w, http.StatusInternalServerError, agentproto.CodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
