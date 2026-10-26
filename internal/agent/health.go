package agent

import (
	"encoding/json"
	"net/http"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAgentError(w, http.StatusMethodNotAllowed, agentproto.CodeInvalidRequest, "GET required")
		return
	}
	writeJSON(w, http.StatusOK, agentproto.HealthResponse{
		Version: s.config.Version, Distro: s.config.Distro, Root: s.config.Root,
		Capabilities: append([]string(nil), s.config.Capabilities...),
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
