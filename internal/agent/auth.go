package agent

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func authenticate(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !agentproto.SupportedProtocol(r.Header.Get(agentproto.HeaderProtocol)) {
			writeAgentError(w, http.StatusUpgradeRequired, agentproto.CodeVersionMismatch, "unsupported or missing agent protocol")
			return
		}
		authorization := r.Header.Get(agentproto.HeaderAuth)
		if !strings.HasPrefix(authorization, agentproto.BearerPrefix) {
			writeAgentError(w, http.StatusUnauthorized, agentproto.CodeUnauthenticated, "bearer token required")
			return
		}
		provided := strings.TrimPrefix(authorization, agentproto.BearerPrefix)
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			writeAgentError(w, http.StatusUnauthorized, agentproto.CodeUnauthenticated, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAgentError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(agentproto.ErrorResponse{Code: code, Message: message})
}
