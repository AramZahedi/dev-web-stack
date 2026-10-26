//go:build !windows

package helper_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestProxyTargetsRPCUpdatesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte(`
socket: /run/devstack-helper/helper.sock
forward_http_port: 18080
forward_https_port: 18443
proxy_http_port: 19080
proxy_https_port: 19443
`), 0o640); err != nil {
		t.Fatal(err)
	}
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID:        os.Getuid(),
		ProxyConfigPath: path,
	})
	client := dialClient(t, socketPath)

	resp, err := client.UpdateProxyTargets(context.Background(), 20080, 20443)
	if err != nil {
		t.Fatalf("UpdateProxyTargets: %v", err)
	}
	if resp.HTTPPort != 20080 || resp.HTTPSPort != 20443 || !resp.Persisted {
		t.Fatalf("response = %+v", resp)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy_http_port: 20080") ||
		!strings.Contains(string(data), "proxy_https_port: 20443") {
		t.Fatalf("config not persisted:\n%s", data)
	}

	_, err = client.UpdateProxyTargets(context.Background(), 20080, 20080)
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("colliding targets error = %v, want ErrInvalidRequest", err)
	}
}
