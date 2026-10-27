//go:build linux

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func listen(endpoint Endpoint, cfg listenConfig) (net.Listener, error) {
	if endpoint.Network != NetworkUnix {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, endpoint.Network)
	}
	// A stale socket from a crashed process blocks bind. Only remove a path
	// that is itself a socket; never delete an arbitrary file.
	if info, err := os.Lstat(endpoint.Address); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("ipc: %s exists and is not a socket", endpoint.Address)
		}
		if err := os.Remove(endpoint.Address); err != nil {
			return nil, fmt.Errorf("ipc: remove stale socket %s: %w", endpoint.Address, err)
		}
	}
	ln, err := net.Listen("unix", endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", endpoint.Address, err)
	}
	if err := os.Chmod(endpoint.Address, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(endpoint.Address)
		return nil, fmt.Errorf("ipc: set socket mode on %s: %w", endpoint.Address, err)
	}
	return ln, nil
}

func dial(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	if endpoint.Network != NetworkUnix {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, endpoint.Network)
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", endpoint.Address)
}

// PeerIdentityOf returns the peer UID from SO_PEERCRED. The kernel supplies
// the credentials, not the client.
func PeerIdentityOf(conn net.Conn) (PeerIdentity, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return PeerIdentity{}, fmt.Errorf("ipc: %T is not a Unix connection", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: peer credentials: %w", err)
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: peer credentials: %w", err)
	}
	if credErr != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: peer credentials: %w", credErr)
	}
	return PeerIdentity{UID: int(cred.Uid)}, nil
}

// PipeSDDL is only meaningful on Windows.
func PipeSDDL(string, bool) (string, error) { return "", ErrUnsupported }

// CurrentSID is only meaningful on Windows.
func CurrentSID() (string, error) { return "", ErrUnsupported }
