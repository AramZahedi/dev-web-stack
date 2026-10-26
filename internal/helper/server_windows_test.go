//go:build windows

package helper

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

const testHelperSID = "S-1-5-21-123456789-123456789-123456789-1001"

func TestNewWindowsServerRequiresAllowedSID(t *testing.T) {
	if _, err := NewServer(Config{AllowSID: ""}); err == nil {
		t.Fatal("NewServer accepted an empty allowed SID")
	}
	if _, err := NewServer(Config{AllowSID: testHelperSID}); err != nil {
		t.Fatalf("NewServer rejected valid SID: %v", err)
	}
}

func TestAuthorizePeerSIDRequiresExactConfiguredSID(t *testing.T) {
	if err := authorizePeerSID(testHelperSID, ipc.PeerIdentity{SID: testHelperSID}); err != nil {
		t.Fatalf("configured SID was rejected: %v", err)
	}
	for _, peer := range []ipc.PeerIdentity{{SID: "S-1-5-21-foreign"}, {SID: ""}, {SID: ipc.SIDLocalSystem}, {SID: ipc.SIDAdministrators}} {
		if err := authorizePeerSID(testHelperSID, peer); err == nil {
			t.Errorf("peer SID %q was accepted", peer.SID)
		}
	}
}

func TestHelperPipeSDDLGrantsOnlyRequiredPrincipals(t *testing.T) {
	sddl, err := helperPipeSDDL(testHelperSID)
	if err != nil {
		t.Fatalf("helperPipeSDDL: %v", err)
	}
	want := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + testHelperSID + ")"
	if sddl != want {
		t.Fatalf("SDDL = %q, want %q", sddl, want)
	}
	for _, forbidden := range []string{"WD", "AU", "AN"} {
		if strings.Contains(sddl, forbidden) {
			t.Errorf("SDDL includes broad principal %q: %s", forbidden, sddl)
		}
	}
}
