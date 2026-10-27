//go:build !windows

package helper_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func vhostHTTPClient(t *testing.T, socketPath string) *http.Client {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func vhostRequest(t *testing.T, client *http.Client, method, path string, body io.Reader) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://helper"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

type vhostHookState struct {
	mu      sync.Mutex
	sync    helperproto.VHostSyncRequest
	mode    string
	modeReq helperproto.VHostSyncRequest
	remove  int
}

func TestHelperVhostRoutes(t *testing.T) {
	hooks := &vhostHookState{}
	status := helperproto.VhostStatusResponse{Mode: "nginx", Server: "nginx", ConfigOK: true}
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID: os.Getuid(),
		Service: helper.Service{
			VhostStatus: func(context.Context) (helperproto.VhostStatusResponse, error) {
				return status, nil
			},
			VhostSync: func(_ context.Context, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
				hooks.mu.Lock()
				hooks.sync = req
				hooks.mu.Unlock()
				return status, nil
			},
			VhostSetMode: func(_ context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
				hooks.mu.Lock()
				hooks.mode = mode
				hooks.modeReq = req
				hooks.mu.Unlock()
				return helperproto.VhostStatusResponse{Mode: mode, ConfigOK: true}, nil
			},
			VhostRemove: func(context.Context) error {
				hooks.mu.Lock()
				hooks.remove++
				hooks.mu.Unlock()
				return nil
			},
		},
	})
	client := vhostHTTPClient(t, socketPath)

	code, body := vhostRequest(t, client, http.MethodGet, helperproto.PathVhostsStatus, nil)
	if code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", code, body)
	}
	var gotStatus helperproto.VhostStatusResponse
	if err := json.Unmarshal(body, &gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus.Mode != "nginx" || !gotStatus.ConfigOK {
		t.Fatalf("status = %+v", gotStatus)
	}

	code, body = vhostRequest(t, client, http.MethodPost, helperproto.PathVhostsSync,
		strings.NewReader(`{"mode":"nginx","max_body_bytes":1024,"sites":[],"dashboard":{"domain":"devstack.test","backend_port":7070}}`))
	if code != http.StatusOK {
		t.Fatalf("sync code = %d, body = %s", code, body)
	}
	hooks.mu.Lock()
	if hooks.sync.Mode != "nginx" || hooks.sync.MaxBodyBytes != 1024 {
		t.Fatalf("sync request = %+v", hooks.sync)
	}
	hooks.mu.Unlock()

	code, body = vhostRequest(t, client, http.MethodPost, helperproto.PathVhostsMode,
		strings.NewReader(`{"mode":"builtin"}`))
	if code != http.StatusOK {
		t.Fatalf("mode builtin code = %d, body = %s", code, body)
	}
	hooks.mu.Lock()
	if hooks.mode != "builtin" || hooks.modeReq.Mode != "" {
		t.Fatalf("mode builtin hook = %q %+v", hooks.mode, hooks.modeReq)
	}
	hooks.mu.Unlock()

	code, body = vhostRequest(t, client, http.MethodPost, helperproto.PathVhostsMode,
		strings.NewReader(`{"mode":"nginx","sync":{"max_body_bytes":2048}}`))
	if code != http.StatusOK {
		t.Fatalf("mode nginx code = %d, body = %s", code, body)
	}
	hooks.mu.Lock()
	if hooks.mode != "nginx" || hooks.modeReq.Mode != "nginx" || hooks.modeReq.MaxBodyBytes != 2048 {
		t.Fatalf("mode nginx hook = %q %+v", hooks.mode, hooks.modeReq)
	}
	hooks.mu.Unlock()

	code, body = vhostRequest(t, client, http.MethodDelete, helperproto.PathVhosts, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete code = %d, body = %s", code, body)
	}
	hooks.mu.Lock()
	if hooks.remove != 1 {
		t.Fatalf("remove calls = %d", hooks.remove)
	}
	hooks.mu.Unlock()
}

func TestHelperVhostModeRequiresSync(t *testing.T) {
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID: os.Getuid(),
		Service: helper.Service{
			VhostSetMode: func(_ context.Context, mode string, _ helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
				return helperproto.VhostStatusResponse{Mode: mode}, nil
			},
		},
	})
	client := vhostHTTPClient(t, socketPath)
	code, body := vhostRequest(t, client, http.MethodPost, helperproto.PathVhostsMode,
		strings.NewReader(`{"mode":"nginx"}`))
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	var errResp helperproto.ErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Code != helperproto.CodeInvalidRequest {
		t.Fatalf("code = %q", errResp.Code)
	}
}

func TestHelperVhostErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"serving mode unavailable", helperproto.ErrServingModeUnavailable, http.StatusConflict, helperproto.CodeServingModeUnavailable},
		{"include missing", helperproto.ErrVhostIncludeMissing, http.StatusConflict, helperproto.CodeVhostIncludeMissing},
		{"sync busy", helperproto.ErrVhostSyncBusy, http.StatusConflict, helperproto.CodeVhostSyncBusy},
		{"config invalid", helperproto.ErrVhostConfigInvalid, http.StatusUnprocessableEntity, helperproto.CodeVhostConfigInvalid},
		{"reload failed", helperproto.ErrServerReloadFailed, http.StatusBadGateway, helperproto.CodeServerReloadFailed},
		{"sync failed", helperproto.ErrVhostSyncFailed, http.StatusInternalServerError, helperproto.CodeVhostSyncFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			socketPath, _ := startTestServer(t, helper.Config{
				AllowUID: os.Getuid(),
				Service: helper.Service{
					VhostSync: func(context.Context, helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
						return helperproto.VhostStatusResponse{}, errors.Join(tc.err, errors.New("detail"))
					},
				},
			})
			client := vhostHTTPClient(t, socketPath)
			code, body := vhostRequest(t, client, http.MethodPost, helperproto.PathVhostsSync,
				strings.NewReader(`{"mode":"nginx"}`))
			if code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", code, tc.status, body)
			}
			var errResp helperproto.ErrorResponse
			if err := json.Unmarshal(body, &errResp); err != nil {
				t.Fatal(err)
			}
			if errResp.Code != tc.code {
				t.Fatalf("code = %q, want %q", errResp.Code, tc.code)
			}
		})
	}
}

func TestHelperVhostOversizedSyncRejected(t *testing.T) {
	var called atomic.Bool
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID: os.Getuid(),
		Service: helper.Service{
			VhostSync: func(context.Context, helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
				called.Store(true)
				return helperproto.VhostStatusResponse{}, nil
			},
		},
	})
	client := vhostHTTPClient(t, socketPath)
	body := `{"mode":"nginx","sites":["` + strings.Repeat("a", 8<<20) + `"]}`
	code, data := vhostRequest(t, client, http.MethodPost, helperproto.PathVhostsSync, strings.NewReader(body))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", code, data)
	}
	var errResp helperproto.ErrorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Code != helperproto.CodeInvalidRequest {
		t.Fatalf("code = %q", errResp.Code)
	}
	if called.Load() {
		t.Fatal("oversized request reached the service")
	}
}
