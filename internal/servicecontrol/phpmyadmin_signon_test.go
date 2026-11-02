package servicecontrol

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func signonManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	if err := writePhpMyAdminSecret(filepath.Join(root, "services", "phpmyadmin", "secrets"), "signon_key", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		activeStatus(services.IDMySQL),
		activeStatus(services.IDPHPMyAdmin),
	}}
	store := newFakeStore()
	manager := NewManager(root, store, helper, testCatalogWithPhpMyAdmin(), nil)
	return manager, root
}

func TestIssuePHPMyAdminSignonCreatesVerifiableToken(t *testing.T) {
	manager, root := signonManager(t)
	signon, err := manager.IssuePHPMyAdminSignon(context.Background(), services.IDPHPMyAdmin)
	if err != nil {
		t.Fatalf("IssuePHPMyAdminSignon() = %v", err)
	}
	parsed, err := url.Parse(signon.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/signon.php" || parsed.RawQuery != "" || !strings.HasPrefix(parsed.Fragment, "token=") {
		t.Fatalf("signon URL = %q", signon.URL)
	}
	token := strings.TrimPrefix(parsed.Fragment, "token=")
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("token parts = %d", len(parts))
	}
	key := strings.TrimSpace(readTestFile(t, root+"/services/phpmyadmin/secrets/signon_key"))
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		t.Fatal("token signature does not verify with the generated key")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "root" || claims["v"] != float64(1) || claims["engine"] != "mysql" {
		t.Fatalf("claims = %+v", claims)
	}
	if claims["nonce"] == "" || claims["exp"].(float64) <= claims["iat"].(float64) {
		t.Fatalf("invalid claims = %+v", claims)
	}
}

func TestIssuePHPMyAdminSignonRequiresActivePMA(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		activeStatus(services.IDMySQL),
		{ID: string(services.IDPHPMyAdmin), State: string(services.StateInstalledButStopped), Installed: true},
	}}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, testCatalogWithPhpMyAdmin(), nil)
	_, err := manager.IssuePHPMyAdminSignon(context.Background(), services.IDPHPMyAdmin)
	if !errors.Is(err, databases.ErrServiceInactive) {
		t.Fatalf("error = %v, want ErrServiceInactive", err)
	}
}

func TestIssuePHPMyAdminSignonRequiresActiveDatabase(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{ID: string(services.IDMySQL), State: string(services.StateInstalledButStopped), Installed: true},
		activeStatus(services.IDPHPMyAdmin),
	}}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, testCatalogWithPhpMyAdmin(), nil)
	_, err := manager.IssuePHPMyAdminSignon(context.Background(), services.IDPHPMyAdmin)
	if !errors.Is(err, databases.ErrServiceInactive) {
		t.Fatalf("error = %v, want ErrServiceInactive", err)
	}
}
