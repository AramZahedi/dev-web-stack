//go:build windows

package helper

import (
	"log/slog"
)

const (
	DefaultWindowsPipe       = `\\.\pipe\devstack-helper`
	DefaultWindowsRoot       = `C:\devstack`
	DefaultWindowsSystemDir  = `C:\devstack\system`
	DefaultWindowsCADir      = `C:\devstack\system\pki`
	DefaultWindowsConfigPath = `C:\devstack\system\helper.yaml`
	DefaultWindowsHostsPath  = `C:\Windows\System32\drivers\etc\hosts`
	DefaultWindowsHTTPPort   = 80
	DefaultWindowsHTTPSPort  = 443
	DefaultWindowsProxyHTTP  = 8080
	DefaultWindowsProxyHTTPS = 8443
)

// Config contains the narrow Windows helper surface. The helper has no Unix
// socket or UID authentication settings; all calls arrive over a protected
// named pipe and are checked against AllowSID.
type Config struct {
	PipeName string
	AllowSID string
	Logger   *slog.Logger
}
