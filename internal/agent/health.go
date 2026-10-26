package agent

import (
	"encoding/json"
	"fmt"
	"net/http"

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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
