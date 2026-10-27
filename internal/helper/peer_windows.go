//go:build windows

package helper

import (
	"fmt"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

// helperPipeSDDL returns the protected DACL used by the helper pipe. The ACL
// includes SYSTEM and Administrators for service maintenance, but request
// authorization still requires the installing user's exact SID.
func helperPipeSDDL(allowSID string) (string, error) {
	return ipc.PipeSDDL(allowSID, true)
}

func authorizePeerSID(allowSID string, peer ipc.PeerIdentity) error {
	if strings.TrimSpace(allowSID) == "" || strings.TrimSpace(peer.SID) == "" || !strings.EqualFold(allowSID, peer.SID) {
		return fmt.Errorf("%w: named-pipe peer SID does not match the configured user", ipc.ErrPeerUnauthorized)
	}
	return nil
}
