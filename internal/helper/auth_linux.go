//go:build linux

package helper

import (
	"fmt"
	"net"
	"syscall"
)

// peerUID returns the effective user ID of the process at the other end of an
// accepted Unix-domain socket using SO_PEERCRED.
func peerUID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return -1, fmt.Errorf("peer is not a Unix connection (%T)", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return -1, fmt.Errorf("get raw connection: %w", err)
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return -1, fmt.Errorf("access socket: %w", err)
	}
	if credErr != nil {
		return -1, fmt.Errorf("SO_PEERCRED: %w", credErr)
	}
	if cred == nil {
		return -1, fmt.Errorf("SO_PEERCRED returned no credentials")
	}
	return int(cred.Uid), nil
}
