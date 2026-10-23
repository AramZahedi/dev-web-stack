package helperclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

// newUnixClient starts an HTTP server on a Unix socket and returns a client
// pointed at it. The server handler is expected to exercise one test scenario.
func newUnixClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestVhostMethodsUseProtocolPathsAndBodies(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
	)
	status := helperproto.VhostStatusResponse{
		Mode:           "nginx",
		Server:         "nginx",
		ServerVersion:  "1.26.0",
		IncludePresent: true,
		ConfigOK:       true,
		LastSync:       time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if got := r.Header.Get(helperproto.ProtocolHeader); got != helperproto.Version {
			t.Errorf("protocol header = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == helperproto.PathVhostsStatus:
			_ = json.NewEncoder(w).Encode(status)
		case r.Method == http.MethodPost && r.URL.Path == helperproto.PathVhostsSync:
			var req helperproto.VHostSyncRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode sync: %v", err)
			}
			if req.Mode != "nginx" || len(req.Sites) != 1 || req.Sites[0].ID != "site-1" {
				t.Errorf("sync request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(status)
		case r.Method == http.MethodPost && r.URL.Path == helperproto.PathVhostsMode:
			var req helperproto.VhostModeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode mode: %v", err)
			}
			if req.Mode != "apache" {
				t.Errorf("mode request = %+v", req)
			}
			if req.Sync == nil || req.Sync.Mode != "apache" {
				t.Errorf("mode sync request = %+v", req.Sync)
			}
			_ = json.NewEncoder(w).Encode(status)
		case r.Method == http.MethodDelete && r.URL.Path == helperproto.PathVhosts:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	client := newUnixClient(t, handler)
	ctx := context.Background()

	got, err := client.VhostStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "nginx" || got.ServerVersion != "1.26.0" || !got.ConfigOK {
		t.Fatalf("status = %+v", got)
	}

	req := helperproto.VHostSyncRequest{
		Mode: "nginx",
		Sites: []helperproto.VHostSite{{
			ID:              "site-1",
			Name:            "blog",
			Domain:          "blog.test",
			Kind:            "static",
			DocumentRoot:    "/tmp/blog",
			DesiredState:    "running",
			BackendPath:     "/tmp",
			CertPEM:         nil,
			KeyPEM:          nil,
			TLS:             false,
			WWW:             false,
			ForceHTTPS:      false,
			SPAFallback:     false,
			FrontController: false,
			BackendPort:     0,
		}},
		Dashboard: helperproto.VHostDashboard{Domain: "devstack.test", BackendPort: 7070},
	}
	if err := client.SyncVhosts(ctx, req); err != nil {
		t.Fatal(err)
	}

	modeStatus, err := client.SetServingMode(ctx, "apache", helperproto.VHostSyncRequest{Mode: "apache"})
	if err != nil {
		t.Fatal(err)
	}
	if modeStatus.Mode != "nginx" {
		t.Fatalf("mode status = %+v", modeStatus)
	}

	if err := client.RemoveVhosts(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET " + helperproto.PathVhostsStatus,
		"POST " + helperproto.PathVhostsSync,
		"POST " + helperproto.PathVhostsMode,
		"DELETE " + helperproto.PathVhosts,
	}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestVhostErrorsMapToProtocolSentinels(t *testing.T) {
	tests := []struct {
		code   string
		status int
		want   error
	}{
		{helperproto.CodeServingModeUnavailable, http.StatusConflict, helperproto.ErrServingModeUnavailable},
		{helperproto.CodeVhostConfigInvalid, http.StatusUnprocessableEntity, helperproto.ErrVhostConfigInvalid},
		{helperproto.CodeVhostSyncFailed, http.StatusInternalServerError, helperproto.ErrVhostSyncFailed},
		{helperproto.CodeVhostIncludeMissing, http.StatusConflict, helperproto.ErrVhostIncludeMissing},
		{helperproto.CodeServerReloadFailed, http.StatusBadGateway, helperproto.ErrServerReloadFailed},
		{helperproto.CodeVhostSyncBusy, http.StatusConflict, helperproto.ErrVhostSyncBusy},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			client := newUnixClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(helperproto.ErrorResponse{Code: tt.code, Message: "boom"})
			}))
			_, err := client.VhostStatus(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want sentinel %v", err, tt.want)
			}
		})
	}
}
