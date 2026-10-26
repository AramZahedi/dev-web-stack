package agentclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func newTLSServer(t *testing.T, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	identity, err := agent.GenerateTLSIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(identity.CertPEM, identity.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, identity.Pin
}

func authHandler(t *testing.T, token string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(agentproto.PathHealth, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(agentproto.HeaderProtocol) != agentproto.ProtocolVersion {
			writeJSON(w, http.StatusUpgradeRequired, agentproto.ErrorResponse{Code: agentproto.CodeVersionMismatch, Message: "version"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			writeJSON(w, http.StatusUnauthorized, agentproto.ErrorResponse{Code: agentproto.CodeUnauthenticated, Message: "token"})
			return
		}
		writeJSON(w, http.StatusOK, agentproto.HealthResponse{Version: "9.9.9", Distro: "devstack", Root: "/data/devstack"})
	})
	mux.HandleFunc(agentproto.PathVersion, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, agentproto.VersionResponse{Protocol: agentproto.ProtocolVersion, Agent: "9.9.9"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestHealth(t *testing.T) {
	srv, pin := newTLSServer(t, authHandler(t, "secret"))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	health, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Version != "9.9.9" || health.Distro != "devstack" {
		t.Fatalf("health = %+v", health)
	}
}

func TestWrongToken(t *testing.T) {
	srv, pin := newTLSServer(t, authHandler(t, "secret"))
	c, err := New(Config{BaseURL: srv.URL, Token: "wrong", PinSHA256: pin, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Health(context.Background())
	if !errors.Is(err, agentproto.ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestCertPinMismatch(t *testing.T) {
	srv, _ := newTLSServer(t, authHandler(t, "secret"))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: strings.Repeat("0", 64), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Health(context.Background())
	if !errors.Is(err, agentproto.ErrCertPinMismatch) {
		t.Fatalf("error = %v, want ErrCertPinMismatch", err)
	}
}

func TestVersionMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(agentproto.PathHealth, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUpgradeRequired, agentproto.ErrorResponse{Code: agentproto.CodeVersionMismatch, Message: "version"})
	})
	srv, pin := newTLSServer(t, mux)
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Health(context.Background())
	if !errors.Is(err, agentproto.ErrVersionMismatch) {
		t.Fatalf("error = %v, want ErrVersionMismatch", err)
	}
}

func TestTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(agentproto.PathHealth, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(w, http.StatusOK, agentproto.HealthResponse{})
	})
	srv, pin := newTLSServer(t, mux)
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Health(context.Background())
	if !errors.Is(err, agentproto.ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
}

func TestNewRejectsInsecureConfig(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://127.0.0.1:1", Token: "t", PinSHA256: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("http base URL accepted")
	}
	if _, err := New(Config{BaseURL: "https://127.0.0.1:1", Token: "", PinSHA256: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("empty token accepted")
	}
	if _, err := New(Config{BaseURL: "https://127.0.0.1:1", Token: "t", PinSHA256: "short"}); err == nil {
		t.Fatal("short pin accepted")
	}
}

func TestVersion(t *testing.T) {
	srv, pin := newTLSServer(t, authHandler(t, "secret"))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	version, err := c.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version.Protocol != agentproto.ProtocolVersion || version.Agent != "9.9.9" {
		t.Fatalf("version = %+v", version)
	}
}
