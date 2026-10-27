//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"runtime"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

var (
	modadvapi32                    = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipeClient = modadvapi32.NewProc("ImpersonateNamedPipeClient")
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
	conn, err := winio.DialPipeAccessImpLevel(ctx, endpoint.Address,
		uint32(windows.GENERIC_READ|windows.GENERIC_WRITE),
		winio.PipeImpLevelImpersonation)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial %s: %w", endpoint.Address, err)
	}
	return conn, nil
}

// PeerIdentityOf resolves the client process behind a named-pipe connection
// and returns the user SID from its token. A pipe server can trust this value:
// the handle belongs to the accepted connection, not to a caller-supplied PID.
// It uses ImpersonateNamedPipeClient to bind directly to the client token
// on the pipe handle, eliminating PID-reuse TOCTOU.
func PeerIdentityOf(conn net.Conn) (PeerIdentity, error) {
	fdConn, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return PeerIdentity{}, fmt.Errorf("ipc: %T has no pipe handle", conn)
	}
	handle := windows.Handle(fdConn.Fd())

	// Primary method: derive identity directly from the connected pipe handle
	// via ImpersonateNamedPipeClient. This eliminates the PID-reuse TOCTOU race
	// where a client process could terminate and have its PID reused between
	// GetNamedPipeClientProcessId and OpenProcess.
	sid, err := peerIdentityFromImpersonation(handle)
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("ipc: verify named-pipe client identity: %w", err)
	}
	return PeerIdentity{SID: sid}, nil
}

func peerIdentityFromImpersonation(handle windows.Handle) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	r1, _, lastErr := procImpersonateNamedPipeClient.Call(uintptr(handle))
	if r1 == 0 {
		return "", fmt.Errorf("ipc: impersonate named pipe client: %w", lastErr)
	}
	defer func() { _ = windows.RevertToSelf() }()

	var token windows.Token
	// OpenAsSelf must be true so the server's process context is used to open
	// the thread token, which succeeds even when the client connected with
	// SecurityIdentification level or restricted token privileges.
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
		return "", fmt.Errorf("ipc: open thread token: %w", err)
	}
	defer func() { _ = token.Close() }()

	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("ipc: read client token user: %w", err)
	}
	if user.User.Sid == nil {
		return "", fmt.Errorf("ipc: client token has no user SID")
	}
	return user.User.Sid.String(), nil
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
