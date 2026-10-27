//go:build windows

package helper_test

import (
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
)

func TestServiceRelayTargetsAllowlist(t *testing.T) {
	allowedPorts := []int{3306, 5432, 6379, 8080, 8082}
	for _, p := range allowedPorts {
		if err := helper.ValidateServiceRelayPort(p); err != nil {
			t.Errorf("ValidateServiceRelayPort(%d) unexpected error: %v", p, err)
		}
	}

	disallowedPorts := []int{21, 22, 25, 80, 443, 8000, 8443, 9000, 9999}
	for _, p := range disallowedPorts {
		if err := helper.ValidateServiceRelayPort(p); err == nil {
			t.Errorf("ValidateServiceRelayPort(%d) want error, got nil", p)
		}
	}
}

func TestServiceRelayForwarderAddress(t *testing.T) {
	validDistroIP := "172.28.16.5" // typical private WSL NAT IP

	listen, target, err := helper.ServiceRelayForwarderAddress(validDistroIP, 3306)
	if err != nil {
		t.Fatalf("ServiceRelayForwarderAddress() error: %v", err)
	}
	if listen != "127.0.0.1:3306" {
		t.Errorf("listen address = %q, want %q", listen, "127.0.0.1:3306")
	}
	if target != "172.28.16.5:3306" {
		t.Errorf("target address = %q, want %q", target, "172.28.16.5:3306")
	}

	// Rejects disallowed port
	if _, _, err := helper.ServiceRelayForwarderAddress(validDistroIP, 22); err == nil {
		t.Errorf("ServiceRelayForwarderAddress with port 22 want error, got nil")
	}

	// Rejects public or non-private IP
	invalidIPs := []string{"", "8.8.8.8", "127.0.0.1", "256.0.0.1", "invalid"}
	for _, ip := range invalidIPs {
		if _, _, err := helper.ServiceRelayForwarderAddress(ip, 3306); err == nil {
			t.Errorf("ServiceRelayForwarderAddress(ip=%q) want error, got nil", ip)
		}
	}
}

func TestServiceRelayFirewallRule(t *testing.T) {
	ruleName, port, localOnly, err := helper.ServiceRelayFirewallRule(6379)
	if err != nil {
		t.Fatalf("ServiceRelayFirewallRule() error: %v", err)
	}
	if port != 6379 {
		t.Errorf("port = %d, want 6379", port)
	}
	if !localOnly {
		t.Errorf("localOnly = false, want true")
	}
	if ruleName == "" {
		t.Errorf("ruleName is empty")
	}

	if _, _, _, err := helper.ServiceRelayFirewallRule(80); err == nil {
		t.Errorf("ServiceRelayFirewallRule(80) want error, got nil")
	}
}
