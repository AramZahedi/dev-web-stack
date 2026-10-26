package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/config"
)

type settingsUpdateRequest struct {
	HTTPPort  *int `json:"http_port"`
	HTTPSPort *int `json:"https_port"`
	UIPort    *int `json:"ui_port"`
}

type settingsUpdateResponse struct {
	settingsResponse
	Warnings []string `json:"warnings,omitempty"`
}

type portUnavailableError struct {
	Field string
	Port  int
	Err   error
}

func (e *portUnavailableError) Error() string {
	return fmt.Sprintf("%s port %d is already in use or cannot be bound: %v", e.Field, e.Port, e.Err)
}

// handleUpdateSettings updates only the listener ports. It never rebinds a
// listener inside the request: the caller receives restart_required and uses
// `devstack daemon restart`. Config.yaml is the durable source; store settings
// mirror the effective values for GET /settings until that restart.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsUpdateRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	current := s.currentSettings(r.Context())
	if strings.TrimSpace(current.Home) == "" {
		writeError(w, http.StatusInternalServerError, codeInternal, "devstack home is not configured", nil)
		return
	}
	cfg, err := config.Load(current.Home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "load config.yaml: "+err.Error(), nil)
		return
	}

	// config.yaml is the durable source. It already contains any saved-but-not-
	// restarted port change and any deliberate out-of-band edit. The store is
	// only a mirror for GET /settings, so omitted values must come from cfg.
	running := s.runningPorts(current)
	if req.HTTPPort != nil {
		cfg.HTTPPort = *req.HTTPPort
	}
	if req.HTTPSPort != nil {
		cfg.HTTPSPort = *req.HTTPSPort
	}
	if req.UIPort != nil {
		cfg.UIPort = *req.UIPort
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}

	currentPorts := map[int]bool{
		running.HTTPPort:  true,
		running.HTTPSPort: true,
		running.UIPort:    true,
	}
	for _, candidate := range []struct {
		field string
		port  int
	}{
		{"http_port", cfg.HTTPPort},
		{"https_port", cfg.HTTPSPort},
		{"ui_port", cfg.UIPort},
	} {
		if currentPorts[candidate.port] {
			// The current daemon owns this listener, or the port is an
			// unchanged value; a restart releases/reuses it safely.
			continue
		}
		if err := probeLoopbackPort(candidate.field, candidate.port); err != nil {
			var unavailable *portUnavailableError
			if errors.As(err, &unavailable) {
				writeError(w, http.StatusConflict, codePortInUse,
					fmt.Sprintf("%s port %d is already in use; stop the process using it or choose another port",
						unavailable.Field, unavailable.Port),
					map[string]any{"field": unavailable.Field, "port": unavailable.Port})
				return
			}
			writeError(w, http.StatusInternalServerError, codeInternal, err.Error(), nil)
			return
		}
	}

	if err := cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "save config.yaml: "+err.Error(), nil)
		return
	}
	restartRequired := cfg.HTTPPort != running.HTTPPort ||
		cfg.HTTPSPort != running.HTTPSPort ||
		cfg.UIPort != running.UIPort
	if s.store != nil {
		values := map[string]string{
			"http_port":        strconv.Itoa(cfg.HTTPPort),
			"https_port":       strconv.Itoa(cfg.HTTPSPort),
			"ui_port":          strconv.Itoa(cfg.UIPort),
			"restart_required": strconv.FormatBool(restartRequired),
		}
		for key, value := range values {
			if err := s.store.SetSetting(r.Context(), key, value); err != nil {
				s.logger.Error("api: persist port setting", "key", key, "error", err)
				writeError(w, http.StatusInternalServerError, codeInternal, "persist settings", nil)
				return
			}
		}
	}

	out := settingsUpdateResponse{
		settingsResponse: s.currentSettings(r.Context()),
	}
	if restartRequired {
		out.Warnings = append(out.Warnings,
			"Run `devstack daemon restart` to move the listeners. The current browser connection may close.")
		if cfg.HTTPPort != running.HTTPPort || cfg.HTTPSPort != running.HTTPSPort {
			out.Warnings = append(out.Warnings,
				"Public 80/443 forwarding is owned by the helper. If builtin forwarding is enabled, update proxy_http_port/proxy_https_port in /etc/devstack/helper.yaml and restart devstack-helper before public URLs can use the new daemon ports.")
		}
		if cfg.UIPort != running.UIPort {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("Reopen the dashboard at http://127.0.0.1:%d after the restart.", cfg.UIPort))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// runningPorts returns the daemon's bound listener ports, falling back to the
// effective settings for callers that do not supply them (tests and read-only
// API harnesses).
func (s *Server) runningPorts(current settingsResponse) ListenerPorts {
	if s.cfg.Listeners.HTTPPort > 0 && s.cfg.Listeners.HTTPSPort > 0 && s.cfg.Listeners.UIPort > 0 {
		return s.cfg.Listeners
	}
	return ListenerPorts{
		HTTPPort:  current.HTTPPort,
		HTTPSPort: current.HTTPSPort,
		UIPort:    current.UIPort,
	}
}

// probeLoopbackPort verifies that the daemon's listener address is available
// without leaving a listener behind.
func probeLoopbackPort(field string, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return &portUnavailableError{Field: field, Port: port, Err: err}
	}
	if err := ln.Close(); err != nil {
		return fmt.Errorf("close probe listener for port %d: %w", port, err)
	}
	return nil
}
