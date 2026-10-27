//go:build windows

package daemon

import (
	"fmt"
	"net"

	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

func listenControlPipe(home string) (net.Listener, error) {
	sid, err := ipc.CurrentSID()
	if err != nil {
		return nil, fmt.Errorf("daemon: resolve current Windows SID: %w", err)
	}
	ln, err := ipc.Listen(Endpoint(home), ipc.WithAllowSID(sid))
	if err != nil {
		return nil, fmt.Errorf("daemon: listen on control pipe: %w", err)
	}
	return ln, nil
}
