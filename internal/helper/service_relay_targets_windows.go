//go:build windows

package helper

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Allowed service relay ports on Windows helper.
// Matches the agent relay allowlist for MySQL/MariaDB, PostgreSQL, Redis, and phpMyAdmin.
var allowedServiceRelayPorts = map[int]string{
	3306: "mysql",
	5432: "postgresql",
	6379: "redis",
	8080: "phpmyadmin",
	8082: "phpmyadmin",
}

// ErrDisallowedServicePort is returned when requesting a relay for an unsupported port.
var ErrDisallowedServicePort = errors.New("helper: port is not in the allowed service relay list")

// ValidateServiceRelayPort checks if port is on the fixed service allowlist.
func ValidateServiceRelayPort(port int) error {
	if _, ok := allowedServiceRelayPorts[port]; !ok {
		return fmt.Errorf("%w: port %d", ErrDisallowedServicePort, port)
	}
	return nil
}

// ServiceRelayForwarderAddress validates and computes the listen and target addresses
// for a Windows loopback forwarder pointing to the WSL agent relay.
func ServiceRelayForwarderAddress(distroIP string, port int) (listenAddr, targetAddr string, err error) {
	if err := ValidateServiceRelayPort(port); err != nil {
		return "", "", err
	}

	ip := net.ParseIP(strings.TrimSpace(distroIP))
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return "", "", errors.New("helper: distroIP must be a valid private WSL NAT address")
	}

	listenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	targetAddr = net.JoinHostPort(ip.String(), strconv.Itoa(port))
	return listenAddr, targetAddr, nil
}

// ServiceRelayFirewallRule returns the rule name, port, and scoping for loopback-only service relay.
func ServiceRelayFirewallRule(port int) (ruleName string, rulePort int, localSubnetOnly bool, err error) {
	if err := ValidateServiceRelayPort(port); err != nil {
		return "", 0, false, err
	}
	service := allowedServiceRelayPorts[port]
	ruleName = fmt.Sprintf("DevStack Service Relay (%s %d)", strings.ToUpper(service), port)
	return ruleName, port, true, nil
}
