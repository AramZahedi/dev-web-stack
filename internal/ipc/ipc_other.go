//go:build !linux && !windows

package ipc

import (
	"context"
	"fmt"
	"net"
)

func listen(Endpoint, listenConfig) (net.Listener, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, "local control transport")
}

func dial(context.Context, Endpoint) (net.Conn, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, "local control transport")
}

func PeerIdentityOf(net.Conn) (PeerIdentity, error) {
	return PeerIdentity{}, fmt.Errorf("%w: peer credentials", ErrUnsupported)
}

func PipeSDDL(string, bool) (string, error) { return "", ErrUnsupported }

func CurrentSID() (string, error) { return "", ErrUnsupported }
