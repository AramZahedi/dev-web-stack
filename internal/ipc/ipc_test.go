package ipc

import (
	"errors"
	"testing"
)

func TestEndpointValidate(t *testing.T) {
	tests := []struct {
		name string
		ep   Endpoint
		ok   bool
	}{
		{"unix absolute", Endpoint{Network: NetworkUnix, Address: "/run/devstack.sock"}, true},
		{"unix relative", Endpoint{Network: NetworkUnix, Address: "devstack.sock"}, false},
		{"named pipe", Endpoint{Network: NetworkNamedPipe, Address: `\\.\pipe\devstack`}, true},
		{"named pipe without prefix", Endpoint{Network: NetworkNamedPipe, Address: `devstack`}, false},
		{"empty network", Endpoint{Address: "/tmp/x"}, false},
		{"empty address", Endpoint{Network: NetworkUnix}, false},
		{"unknown network", Endpoint{Network: "tcp", Address: "127.0.0.1:1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ep.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidEndpoint) {
				t.Fatalf("Validate() = %v, want ErrInvalidEndpoint", err)
			}
		})
	}
}

func TestEndpointFromAddress(t *testing.T) {
	if got := EndpointFromAddress(`\\.\pipe\devstack`); got.Network != NetworkNamedPipe {
		t.Fatalf("named pipe network = %q", got.Network)
	}
	if got := EndpointFromAddress("/run/devstack.sock"); got.Network != NetworkUnix {
		t.Fatalf("unix network = %q", got.Network)
	}
	if err := EndpointFromAddress(`\\.\pipe\`).Validate(); err == nil {
		t.Fatal("accepted an empty pipe name")
	}
}

func TestAuthorizePeer(t *testing.T) {
	policy := PeerPolicy{AllowUID: 1000, AllowSID: "S-1-5-21-1-2-3-1001", AllowAdministrators: true}
	if err := policy.Authorize(PeerIdentity{UID: 1000}); err != nil {
		t.Fatalf("allowed uid rejected: %v", err)
	}
	if err := policy.Authorize(PeerIdentity{SID: "S-1-5-21-1-2-3-1001"}); err != nil {
		t.Fatalf("allowed sid rejected: %v", err)
	}
	if err := policy.Authorize(PeerIdentity{UID: 1001}); !errors.Is(err, ErrPeerUnauthorized) {
		t.Fatalf("foreign uid = %v, want ErrPeerUnauthorized", err)
	}
	if err := policy.Authorize(PeerIdentity{SID: "S-1-5-21-1-2-3-1002"}); !errors.Is(err, ErrPeerUnauthorized) {
		t.Fatalf("foreign sid = %v, want ErrPeerUnauthorized", err)
	}
	empty := PeerPolicy{}
	if err := empty.Authorize(PeerIdentity{UID: 1000}); !errors.Is(err, ErrPeerUnauthorized) {
		t.Fatalf("empty policy accepted peer: %v", err)
	}
}
