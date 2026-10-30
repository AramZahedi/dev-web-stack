package daemon

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// persistEffectiveSettings mirrors the running daemon's effective machine
// configuration into the store so GET /api/v1/settings reports the values the
// listeners actually use. Both the Unix and Windows startup paths call this;
// each restart clears restart_required after a saved port change has been
// applied.
func persistEffectiveSettings(ctx context.Context, st *store.Store, cfg config.Config, servingMode, servingModeError string, logger *slog.Logger) {
	values := map[string]string{
		"home":                  cfg.Home,
		"wsl_distro":            cfg.WSLDistro,
		"http_port":             strconv.Itoa(cfg.HTTPPort),
		"https_port":            strconv.Itoa(cfg.HTTPSPort),
		"ui_port":               strconv.Itoa(cfg.UIPort),
		"auto_start_sites":      strconv.FormatBool(cfg.AutoStartSites),
		"serving_mode":          servingMode,
		"serving_provider":      cfg.ServingProvider,
		"serving_mode_error":    servingModeError,
		"graceful_stop_seconds": strconv.Itoa(cfg.GracefulStopSeconds),
		"max_request_body_mb":   strconv.Itoa(cfg.MaxRequestBodyMB),
		"log_max_mb":            strconv.Itoa(cfg.LogMaxMB),
		"log_keep":              strconv.Itoa(cfg.LogKeep),
		"restart_required":      "false",
	}
	for key, value := range values {
		if err := st.SetSetting(ctx, key, value); err != nil {
			logger.Warn("daemon: persist setting", "key", key, "error", err)
		}
	}
}
