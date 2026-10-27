//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(endpoint Endpoint, cfg listenConfig) (net.Listener, error) {
	if endpoint.Network != NetworkNamedPipe {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, endpoint.Network)
	}
	pipeCfg := &winio.PipeConfig{}
	if cfg.policy.AllowSID != "" {
		sddl, err := PipeSDDL(cfg.policy.AllowSID, cfg.policy.AllowAdministrators)
		if err != nil {
			return nil, err
		}
		pipeCfg.SecurityDescriptor = sddl
	}
	ln, err := winio.ListenPipe(endpoint.Address, pipeCfg)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", endpoint.Address, err)
	}
	return ln, nil
}

func dial(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	if endpoint.Network != NetworkNamedPipe {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, endpoint.Network)
	}
	conn, err := winio.DialPipeContext(ctx, endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial %s: %w", endpoint.Address, err)
	}
	return conn, nil
}

// PeerIdentityOf resolves the client process behind a named-pipe connection
// and returns the user SID from its token. A pipe server can trust this value:
// the handle belongs to the accepted connection, not to a caller-supplied PID.
func PeerIdentityOf(conn net.Conn) (PeerIdentity, error) {
	fdConn, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return PeerIdentity{}, fmt.Errorf("ipc: %T has no pipe handle", conn)
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(fdConn.Fd()), &pid); err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: named-pipe client pid: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: open client process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: open client token: %w", err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: read client token user: %w", err)
	}
	if user.User.Sid == nil {
		return PeerIdentity{}, fmt.Errorf("ipc: client token has no user SID")
	}
	return PeerIdentity{SID: user.User.Sid.String()}, nil
}

// PipeSDDL builds a protected DACL that grants full access to SYSTEM, the
// installing user, and optionally Administrators. No inherited or anonymous
// access is allowed.
func PipeSDDL(allowSID string, administrators bool) (string, error) {
	if allowSID == "" {
		return "", fmt.Errorf("ipc: empty allowed SID")
	}
	sid, err := windows.StringToSid(allowSID)
	if err != nil {
		return "", fmt.Errorf("ipc: invalid allowed SID %q: %w", allowSID, err)
	}
	// Do not force an owner: assigning SYSTEM ownership requires
	// SeRestorePrivilege. The default owner is the installing user, while the
	// protected DACL below still excludes everyone but the listed SIDs.
	sddl := "D:P(A;;GA;;;SY)"
	if administrators {
		sddl += "(A;;GA;;;BA)"
	}
	sddl += "(A;;GA;;;" + sid.String() + ")"
	return sddl, nil
}

// CurrentSID returns the current process user SID.
func CurrentSID() (string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "", fmt.Errorf("ipc: open current token: %w", err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("ipc: read current token user: %w", err)
	}
	if user.User.Sid == nil {
		return "", fmt.Errorf("ipc: current token has no user SID")
	}
	return user.User.Sid.String(), nil
}
