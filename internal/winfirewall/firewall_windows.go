//go:build windows

// Package winfirewall manages only the two named WSL-to-Windows-proxy rules.
package winfirewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

const (
	httpRuleName  = "Devstack WSL Proxy HTTP"
	httpsRuleName = "Devstack WSL Proxy HTTPS"
	httpPort      = 8080
	httpsPort     = 8443
)

type proxyRule struct {
	name string
	port int
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type Manager struct{ runner CommandRunner }

func NewManager(runner CommandRunner) *Manager {
	if runner == nil {
		runner = execRunner{}
	}
	return &Manager{runner: runner}
}

func (m *Manager) Enable(ctx context.Context, wslSubnet string) error {
	if err := validateWSLSubnet(wslSubnet); err != nil {
		return err
	}
	_, subnet, _ := net.ParseCIDR(wslSubnet)
	canonicalSubnet := subnet.String()
	rules := []proxyRule{{httpRuleName, httpPort}, {httpsRuleName, httpsPort}}
	for i, rule := range rules {
		newSettings := []string{
			"dir=in", "action=allow", "protocol=TCP", fmt.Sprintf("localport=%d", rule.port),
			"remoteip=" + canonicalSubnet, "profile=private,domain", "enable=yes",
		}
		setArgs := append([]string{"advfirewall", "firewall", "set", "rule", "name=" + rule.name, "new"}, newSettings...)
		_, err := m.runner.Run(ctx, "netsh.exe", setArgs...)
		if err != nil && ruleMissingError(err) {
			addArgs := append([]string{"advfirewall", "firewall", "add", "rule", "name=" + rule.name}, newSettings...)
			_, err = m.runner.Run(ctx, "netsh.exe", addArgs...)
		}
		if err != nil {
			failures := []error{fmt.Errorf("configure firewall rule %q: %w", rule.name, err)}
			for _, rollback := range rules[:i+1] {
				deleteArgs := []string{"advfirewall", "firewall", "delete", "rule", "name=" + rollback.name}
				if _, rollbackErr := m.runner.Run(ctx, "netsh.exe", deleteArgs...); rollbackErr != nil && !ruleMissingError(rollbackErr) {
					failures = append(failures, fmt.Errorf("rollback firewall rule %q: %w", rollback.name, rollbackErr))
				}
			}
			return errors.Join(failures...)
		}
	}
	return nil
}

func (m *Manager) Disable(ctx context.Context) error {
	return m.eachRule(ctx, "set", "new", "enable=no")
}

func (m *Manager) Remove(ctx context.Context) error {
	return m.eachRule(ctx, "delete")
}

func (m *Manager) eachRule(ctx context.Context, operation string, suffix ...string) error {
	var failures []error
	for _, name := range []string{httpRuleName, httpsRuleName} {
		args := []string{"advfirewall", "firewall", operation, "rule", "name=" + name}
		args = append(args, suffix...)
		if _, err := m.runner.Run(ctx, "netsh.exe", args...); err != nil {
			if ruleMissingError(err) {
				continue
			}
			failures = append(failures, fmt.Errorf("%s firewall rule %q: %w", operation, name, err))
		}
	}
	return errors.Join(failures...)
}

func ruleMissingError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no rules match") || strings.Contains(message, "rule not found")
}

func validateWSLSubnet(value string) error {
	_, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return fmt.Errorf("winfirewall: invalid WSL subnet %q: %w", value, err)
	}
	if subnet.IP.To4() == nil {
		return errors.New("winfirewall: WSL subnet must be IPv4")
	}
	if !subnet.IP.IsPrivate() || subnet.IP.IsLoopback() {
		return errors.New("winfirewall: WSL subnet must be a private IPv4 network")
	}
	one, bits := subnet.Mask.Size()
	if bits != 32 || one < 16 {
		return errors.New("winfirewall: WSL subnet must be at least as narrow as an IPv4 /16")
	}
	return nil
}
