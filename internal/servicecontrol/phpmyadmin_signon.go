package servicecontrol

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// PHPMyAdminSignon is the one-time URL returned to the dashboard.
type PHPMyAdminSignon struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SetSignonKeyPath overrides where the daemon reads the phpMyAdmin signon key.
// Windows uses a \\wsl$ path because the key must match the one mounted into
// the PMA container; Linux uses the local service path.
func (m *Manager) SetSignonKeyPath(path string) {
	m.signonKeyPath = path
}

// IssuePHPMyAdminSignon creates a short-lived HMAC token for the active root
// account. The token is consumed by signon.php; the root password never leaves
// the helper/agent secret files.
func (m *Manager) IssuePHPMyAdminSignon(ctx context.Context, id services.ID) (PHPMyAdminSignon, error) {
	ctx = normalizeContext(ctx)
	if id != services.IDPHPMyAdmin {
		return PHPMyAdminSignon{}, fmt.Errorf("%w: service %q", databases.ErrInvalidRequest, id)
	}
	statuses, err := m.helper.Status(ctx)
	if err != nil {
		return PHPMyAdminSignon{}, helperFailure("check phpMyAdmin signon status", err)
	}
	pmaActive := false
	dbActive := false
	for _, status := range statuses {
		switch services.ID(status.ID) {
		case services.IDPHPMyAdmin:
			pmaActive = status.Active
		case services.IDMySQL, services.IDMariaDB:
			if status.Active {
				dbActive = true
			}
		}
	}
	if !pmaActive {
		return PHPMyAdminSignon{}, fmt.Errorf("%w: phpMyAdmin is not active", databases.ErrServiceInactive)
	}
	if !dbActive {
		return PHPMyAdminSignon{}, fmt.Errorf("%w: activate MySQL or MariaDB first", databases.ErrServiceInactive)
	}
	keyPath := m.signonKeyPath
	if keyPath == "" {
		keyPath = filepath.Join(m.root, "services", "phpmyadmin", "secrets", "signon_key")
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return PHPMyAdminSignon{}, fmt.Errorf("read phpMyAdmin signon key: %w", err)
	}
	key := strings.TrimSpace(string(keyBytes))
	if key == "" {
		return PHPMyAdminSignon{}, fmt.Errorf("read phpMyAdmin signon key: empty key")
	}
	pma, ok := m.catalog.Get(services.IDPHPMyAdmin)
	if !ok || len(pma.Ports) == 0 {
		return PHPMyAdminSignon{}, fmt.Errorf("phpMyAdmin catalog has no dashboard port")
	}
	now := time.Now().UTC()
	nonceRaw := make([]byte, 16)
	if _, err := rand.Read(nonceRaw); err != nil {
		return PHPMyAdminSignon{}, fmt.Errorf("generate phpMyAdmin signon nonce: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"v":      1,
		"sub":    "root",
		"engine": activeDatabaseEngine(statuses),
		"iat":    now.Unix(),
		"exp":    now.Add(30 * time.Second).Unix(),
		"nonce":  hex.EncodeToString(nonceRaw),
	})
	if err != nil {
		return PHPMyAdminSignon{}, fmt.Errorf("encode phpMyAdmin signon payload: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := encoded + "." + signature
	signonURL := fmt.Sprintf("http://127.0.0.1:%d/signon.php#token=%s",
		pma.Ports[0].Host, url.QueryEscape(token))
	return PHPMyAdminSignon{URL: signonURL, ExpiresAt: now.Add(30 * time.Second)}, nil
}

func activeDatabaseEngine(statuses []helperproto.ServiceStatus) string {
	for _, status := range statuses {
		if status.Active && (services.ID(status.ID) == services.IDMySQL || services.ID(status.ID) == services.IDMariaDB) {
			return status.ID
		}
	}
	return ""
}
