//go:build windows

package proxy

import (
	"fmt"
	"net"
	"strconv"
)

// WindowsPublicListenAddr returns an IPv4 wildcard address so the public
// daemon listeners accept both Windows loopback traffic and the WSL host
// adapter. The Windows helper owns the firewall rule that restricts adapter
// traffic to the WSL subnet; public 80/443 remain helper-forwarded.
func WindowsPublicListenAddr(port int) (string, error) {
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("proxy: Windows public port %d is outside 0-65535", port)
	}
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(port)), nil
}
