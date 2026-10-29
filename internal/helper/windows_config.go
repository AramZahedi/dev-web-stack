//go:build windows

package helper

import (
	"log/slog"
	"path/filepath"

	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"github.com/AramZahedi/dev-web-stack/internal/winfirewall"
	"github.com/AramZahedi/dev-web-stack/internal/winhosts"
	"github.com/AramZahedi/dev-web-stack/internal/wintrust"
)

var (
	DefaultWindowsRoot       = platform.DefaultRoot("windows")
	DefaultWindowsSystemDir  = filepath.Join(platform.DefaultRoot("windows"), "system")
	DefaultWindowsCADir      = filepath.Join(platform.DefaultRoot("windows"), "system", "pki")
	DefaultWindowsRootCert   = filepath.Join(platform.DefaultRoot("windows"), "system", "pki", "root.crt")
	DefaultWindowsConfigPath = filepath.Join(platform.DefaultRoot("windows"), "system", "helper.yaml")
)

const (
	DefaultWindowsPipe       = `\\.\pipe\devstack-helper`
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
	RootCertPath     string
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
