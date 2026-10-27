package agentrelay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentrelay"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestServiceRelayModuleContract(t *testing.T) {
	relay, err := agentrelay.New(agentrelay.Config{})
	if err != nil {
		t.Fatalf("agentrelay.New() error = %v", err)
	}
	defer relay.Close()

	var module agentapi.Module = relay
	if module.Name() != "service-relay" {
		t.Errorf("module.Name() = %q, want %q", module.Name(), "service-relay")
	}
	if module.MountPath() != "/v1/relay/" {
		t.Errorf("module.MountPath() = %q, want %q", module.MountPath(), "/v1/relay/")
	}
	if module.Handler() == nil {
		t.Fatal("module.Handler() returned nil")
	}

	mux := http.NewServeMux()
	agentapi.Mount(mux, module)
	req := httptest.NewRequest(http.MethodGet, "/v1/relay/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status endpoint HTTP code = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestPortAllowlist(t *testing.T) {
	allowedPorts := []int{3306, 5432, 6379, 8080, 8082}
	for _, port := range allowedPorts {
		if !agentrelay.IsAllowedPort(port) {
			t.Errorf("IsAllowedPort(%d) = false, want true", port)
		}
		if err := agentrelay.ValidatePort(port); err != nil {
			t.Errorf("ValidatePort(%d) unexpected error: %v", port, err)
		}
	}

	disallowedPorts := []int{21, 22, 25, 80, 443, 8000, 8443, 9000, 9999}
	for _, port := range disallowedPorts {
		if agentrelay.IsAllowedPort(port) {
			t.Errorf("IsAllowedPort(%d) = true, want false", port)
		}
		if err := agentrelay.ValidatePort(port); err == nil {
			t.Errorf("ValidatePort(%d) want error for disallowed port, got nil", port)
		}
	}
}

func TestServicePortResolution(t *testing.T) {
	cases := []struct {
		service string
		port    int
	}{
		{string(services.IDMySQL), 3306},
		{string(services.IDMariaDB), 3306},
		{string(services.IDPostgreSQL), 5432},
		{"postgres", 5432},
		{string(services.IDRedis), 6379},
		{string(services.IDPHPMyAdmin), 8080},
	}
	for _, tc := range cases {
		port, err := agentrelay.PortForService(tc.service)
		if err != nil {
			t.Errorf("PortForService(%q) error: %v", tc.service, err)
		}
		if port != tc.port {
			t.Errorf("PortForService(%q) = %d, want %d", tc.service, port, tc.port)
		}
	}

	if _, err := agentrelay.PortForService("unsupported-service"); err == nil {
		t.Errorf("PortForService(unsupported-service) want error, got nil")
	}
}

func TestTargetValidationRejectsNonLoopback(t *testing.T) {
	disallowedTargets := []string{
		"192.168.1.1",
		"10.0.0.1",
		"172.16.0.1",
		"8.8.8.8",
		"example.com",
		"100.64.0.1",
	}
	for _, target := range disallowedTargets {
		_, err := agentrelay.New(agentrelay.Config{TargetHost: target})
		if err == nil {
			t.Errorf("agentrelay.New(TargetHost=%q) want error for non-loopback target, got nil", target)
		}
	}

	allowedTargets := []string{"", "127.0.0.1", "localhost", "::1"}
	for _, target := range allowedTargets {
		relay, err := agentrelay.New(agentrelay.Config{TargetHost: target})
		if err != nil {
			t.Errorf("agentrelay.New(TargetHost=%q) unexpected error: %v", target, err)
		}
		if relay != nil {
			relay.Close()
		}
	}
}

func TestRelayForwarding(t *testing.T) {
	// Start an echo server on loopback simulating a container service (e.g. redis or mysql).
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() upstream error: %v", err)
	}
	defer upstream.Close()

	_, upstreamPortStr, _ := net.SplitHostPort(upstream.Addr().String())
	upstreamPort, _ := strconv.Atoi(upstreamPortStr)

	go func() {
		for {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	relay, err := agentrelay.New(agentrelay.Config{
		ListenIP:   "127.0.0.1",
		TargetHost: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("agentrelay.New() error = %v", err)
	}
	defer relay.Close()

	// Start relay explicitly targeting our upstream port with allowed port 6379 semantics
	// or using custom port override for test isolation.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() relay error: %v", err)
	}

	testPort := 6379 // Redis port (allowlisted)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relayErrCh := make(chan error, 1)
	go func() {
		relayErrCh <- relay.ForwardListener(ctx, listener, testPort, upstreamPort)
	}()

	// Connect a client to the relay listener
	client, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("net.Dial() client error: %v", err)
	}
	defer client.Close()

	testMessage := []byte("PING\r\n")
	if _, err := client.Write(testMessage); err != nil {
		t.Fatalf("client.Write() error: %v", err)
	}

	buf := make([]byte, len(testMessage))
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("client.Read() error: %v", err)
	}

	if !bytes.Equal(buf, testMessage) {
		t.Fatalf("received %q, want %q", string(buf), string(testMessage))
	}

	cancel()
	_ = listener.Close()
}

func TestHTTPHandlers(t *testing.T) {
	relay, err := agentrelay.New(agentrelay.Config{
		ListenIP:   "127.0.0.1",
		TargetHost: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("agentrelay.New() error = %v", err)
	}
	defer relay.Close()

	handler := relay.Handler()

	// 1. GET /v1/relay/allowlist
	reqAllow := httptest.NewRequest(http.MethodGet, "/v1/relay/allowlist", nil)
	recAllow := httptest.NewRecorder()
	handler.ServeHTTP(recAllow, reqAllow)
	if recAllow.Code != http.StatusOK {
		t.Fatalf("GET /v1/relay/allowlist code = %d, want %d", recAllow.Code, http.StatusOK)
	}

	var allowlistResp agentrelay.AllowlistResponse
	if err := json.Unmarshal(recAllow.Body.Bytes(), &allowlistResp); err != nil {
		t.Fatalf("Unmarshal allowlist error: %v", err)
	}
	if len(allowlistResp.Ports) < 4 {
		t.Errorf("allowlist ports count = %d, want >= 4", len(allowlistResp.Ports))
	}

	// 2. GET /v1/relay/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/v1/relay/status", nil)
	recStatus := httptest.NewRecorder()
	handler.ServeHTTP(recStatus, reqStatus)
	if recStatus.Code != http.StatusOK {
		t.Fatalf("GET /v1/relay/status code = %d, want %d", recStatus.Code, http.StatusOK)
	}

	// 3. POST /v1/relay/start with disallowed port -> 400 Bad Request
	badBody, _ := json.Marshal(agentrelay.RelayRequest{Port: 22})
	reqBad := httptest.NewRequest(http.MethodPost, "/v1/relay/start", bytes.NewReader(badBody))
	reqBad.Header.Set("Content-Type", "application/json")
	recBad := httptest.NewRecorder()
	handler.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("POST /v1/relay/start with port 22 code = %d, want %d", recBad.Code, http.StatusBadRequest)
	}

	// 4. POST /v1/relay/stop with disallowed port -> 400 Bad Request
	reqBadStop := httptest.NewRequest(http.MethodPost, "/v1/relay/stop", bytes.NewReader(badBody))
	reqBadStop.Header.Set("Content-Type", "application/json")
	recBadStop := httptest.NewRecorder()
	handler.ServeHTTP(recBadStop, reqBadStop)
	if recBadStop.Code != http.StatusBadRequest {
		t.Errorf("POST /v1/relay/stop with port 22 code = %d, want %d", recBadStop.Code, http.StatusBadRequest)
	}

	// 5. POST /v1/relay/sync with disallowed port -> 400 Bad Request
	syncBad, _ := json.Marshal(agentrelay.SyncRelayRequest{Ports: []int{3306, 9999}})
	reqBadSync := httptest.NewRequest(http.MethodPost, "/v1/relay/sync", bytes.NewReader(syncBad))
	reqBadSync.Header.Set("Content-Type", "application/json")
	recBadSync := httptest.NewRecorder()
	handler.ServeHTTP(recBadSync, reqBadSync)
	if recBadSync.Code != http.StatusBadRequest {
		t.Errorf("POST /v1/relay/sync with port 9999 code = %d, want %d", recBadSync.Code, http.StatusBadRequest)
	}

	// 6. POST /v1/relay/sync with unknown service -> 400 Bad Request
	syncBadService, _ := json.Marshal(agentrelay.SyncRelayRequest{Services: []string{"unknown-srv"}})
	reqBadSyncSrv := httptest.NewRequest(http.MethodPost, "/v1/relay/sync", bytes.NewReader(syncBadService))
	reqBadSyncSrv.Header.Set("Content-Type", "application/json")
	recBadSyncSrv := httptest.NewRecorder()
	handler.ServeHTTP(recBadSyncSrv, reqBadSyncSrv)
	if recBadSyncSrv.Code != http.StatusBadRequest {
		t.Errorf("POST /v1/relay/sync with unknown service code = %d, want %d", recBadSyncSrv.Code, http.StatusBadRequest)
	}
}

func TestRelayStopAndClose(t *testing.T) {
	relay, err := agentrelay.New(agentrelay.Config{
		ListenIP:   "127.0.0.1",
		TargetHost: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("agentrelay.New() error = %v", err)
	}

	if err := relay.StopRelay(3306); err != nil {
		t.Errorf("StopRelay on inactive port error = %v", err)
	}

	if err := relay.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestLaneDModulesCoexist(t *testing.T) {
	relay, err := agentrelay.New(agentrelay.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	mux := http.NewServeMux()
	agentapi.Mount(mux, relay)

	// Verify relay endpoint
	req := httptest.NewRequest(http.MethodGet, "/v1/relay/allowlist", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /v1/relay/allowlist code = %d, want %d", rec.Code, http.StatusOK)
	}
}


