//go:build windows

package helper

import (
	"log/slog"

	"github.com/AramZahedi/dev-web-stack/internal/winfirewall"
	"github.com/AramZahedi/dev-web-stack/internal/winhosts"
	"github.com/AramZahedi/dev-web-stack/internal/wintrust"
)

const (
	DefaultWindowsPipe       = `\\.\pipe\devstack-helper`
	DefaultWindowsRoot       = `C:\devstack`
	DefaultWindowsSystemDir  = `C:\devstack\system`
	DefaultWindowsCADir      = `C:\devstack\system\pki`
	DefaultWindowsRootCert   = `C:\devstack\system\pki\root.crt`
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
	PipeName         string
	AllowSID         string
	SystemDir        string
	CADir            string
	HostsPath        string
	ConfigPath       string
	ProxyHTTPPort    int
	ProxyHTTPSPort   int
	WSLSubnet        string
	Logger           *slog.Logger
	Runner           wintrust.CommandRunner
	HostsManager     *winhosts.Manager
	TrustManager     *wintrust.Manager
	FirewallManager  *winfirewall.Manager
	Authority        *Authority
	forwarderFactory windowsForwarderFactory
}
