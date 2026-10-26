package agentproto

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestHealthRoundTrip(t *testing.T) {
	want := HealthResponse{Version: "1.2.3", Distro: "devstack", Root: "/data/devstack", Capabilities: []string{"php", "docker"}}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got HealthResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || got.Distro != want.Distro || got.Root != want.Root || len(got.Capabilities) != 2 {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestHostPathHelper(t *testing.T) {
	cases := map[string]string{
		PathHealth:                    PathHealth,
		PathSitePrepareFor("abc"):     "/v1/sites/abc/prepare",
		PathSitePHPStartFor("abc"):    "/v1/sites/abc/php/start",
		PathSiteTerminalFor("abc"):    "/v1/sites/abc/terminal",
		PathServiceActionFor("mysql"): "/v1/services/mysql/action",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
	}
}

func TestErrorCodesMapToSentinels(t *testing.T) {
	cases := map[string]error{
		CodeUnauthenticated: ErrUnauthenticated,
		CodeForbidden:       ErrForbidden,
		CodeVersionMismatch: ErrVersionMismatch,
		CodeCertPinMismatch: ErrCertPinMismatch,
		CodeNotFound:        ErrNotFound,
		CodeConflict:        ErrConflict,
		CodeInvalidRequest:  ErrInvalidRequest,
		CodeTimeout:         ErrTimeout,
		CodeUnsupported:     ErrUnsupported,
	}
	for code, want := range cases {
		err := &Error{Code: code, Message: "x", Status: 400}
		if !errors.Is(err, want) {
			t.Fatalf("code %q does not map to %v", code, want)
		}
	}
	if err := (&Error{Code: "unknown"}).Unwrap(); err != nil {
		t.Fatalf("unknown code unwrap = %v", err)
	}
}

func TestProtocolVersion(t *testing.T) {
	if ProtocolVersion != "v1" {
		t.Fatalf("ProtocolVersion = %q", ProtocolVersion)
	}
	if !SupportedProtocol(ProtocolVersion) {
		t.Fatal("current protocol unsupported")
	}
	if SupportedProtocol("v2") {
		t.Fatal("future protocol accepted")
	}
}
