package daemon

import (
	"encoding/hex"
	"fmt"

	"github.com/AramZahedi/dev-web-stack/internal/securestore"
)

// dashboardAuthToken loads or creates the per-install dashboard secret. The
// secret is DPAPI-protected on Windows and a 0600 file elsewhere; the API only
// needs its hex form.
func dashboardAuthToken(home string) (string, error) {
	secret, err := securestore.LoadOrCreateAt(home, "api-token", 32)
	if err != nil {
		return "", fmt.Errorf("daemon: load dashboard auth token: %w", err)
	}
	return hex.EncodeToString(secret), nil
}
