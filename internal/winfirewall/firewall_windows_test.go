//go:build windows

package winfirewall

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type commandCall struct {
	name string
	args []string
}

type recordingRunner struct {
	calls       []commandCall
	failAt      int
	missing     bool
	missingSets bool
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, commandCall{name: name, args: append([]string(nil), args...)})
	if r.failAt > 0 && len(r.calls) == r.failAt {
		return nil, errors.New("injected command failure")
	}
	if r.missing || (r.missingSets && len(args) > 2 && args[2] == "set") {
		return []byte("No rules match the specified criteria."), errors.New("netsh rule not found")
	}
	return nil, nil
}

func TestFirewallDisableAndRemoveAreIdempotentWhenRulesAreAbsent(t *testing.T) {
	runner := &recordingRunner{missing: true}
	manager := NewManager(runner)
	if err := manager.Disable(context.Background()); err != nil {
		t.Fatalf("Disable missing rules: %v", err)
	}
	if err := manager.Remove(context.Background()); err != nil {
		t.Fatalf("Remove missing rules: %v", err)
	}
}

func TestFirewallCreatesFixedProxyRulesForValidatedWSLSubnet(t *testing.T) {
	runner := &recordingRunner{missingSets: true}
	manager := NewManager(runner)
	if err := manager.Enable(context.Background(), "172.28.64.0/20"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("calls = %+v; want update/creation for HTTP and HTTPS rules", runner.calls)
	}
	joined := strings.Join(runner.calls[1].args, " ") + " " + strings.Join(runner.calls[3].args, " ")
	for _, value := range []string{"8080", "8443", "172.28.64.0/20", "enable=yes", "action=allow", "dir=in"} {
		if !strings.Contains(joined, value) {
			t.Errorf("firewall rules lack %q: %s", value, joined)
		}
	}
}

func TestFirewallEnableUpdatesExistingRulesWithoutDuplicates(t *testing.T) {
	runner := &recordingRunner{}
	if err := NewManager(runner).Enable(context.Background(), "172.28.64.0/20"); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 || runner.calls[0].args[2] != "set" || runner.calls[1].args[2] != "set" {
		t.Fatalf("Enable did not update existing fixed rules: %+v", runner.calls)
	}
}

func TestFirewallRejectsInvalidSubnetBeforeCommands(t *testing.T) {
	runner := &recordingRunner{}
	invalidSubnets := []string{
		"0.0.0.0/0",
		"0.0.0.0/16",
		"8.8.8.0/24",
		"1.1.1.0/24",
		"198.51.100.0/24",
		"127.0.0.0/16",
		"172.16.0.0/8", // wider than /16
		"invalid-cidr",
	}

	for _, subnet := range invalidSubnets {
		t.Run(subnet, func(t *testing.T) {
			runner.calls = nil
			if err := NewManager(runner).Enable(context.Background(), subnet); err == nil {
				t.Fatalf("Enable accepted invalid/non-private subnet %q", subnet)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("invalid subnet %q ran commands: %+v", subnet, runner.calls)
			}
		})
	}
}

func TestFirewallCanonicalizesSubnetCIDR(t *testing.T) {
	runner := &recordingRunner{missingSets: true}
	manager := NewManager(runner)
	// Passing a host address with CIDR mask must be masked to canonical network address
	if err := manager.Enable(context.Background(), "172.28.64.15/20"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	joined := strings.Join(runner.calls[1].args, " ")
	if !strings.Contains(joined, "remoteip=172.28.64.0/20") {
		t.Fatalf("expected canonical network CIDR 172.28.64.0/20, got: %s", joined)
	}
}

func TestFirewallDisableAndDeleteOnlyManagedRules(t *testing.T) {
	runner := &recordingRunner{}
	manager := NewManager(runner)
	if err := manager.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("calls = %+v; want two disable and two delete commands", runner.calls)
	}
	for _, call := range runner.calls {
		if call.name != "netsh.exe" || !strings.Contains(strings.Join(call.args, " "), "Devstack WSL Proxy") {
			t.Errorf("command is not scoped to our named rules: %+v", call)
		}
	}
	if runner.calls[0].args[2] != "set" || runner.calls[1].args[2] != "set" || !strings.Contains(strings.Join(runner.calls[0].args, " "), "enable=no") {
		t.Fatalf("disable commands are malformed: %+v", runner.calls[:2])
	}
	if runner.calls[2].args[2] != "delete" || runner.calls[3].args[2] != "delete" {
		t.Fatalf("delete commands are malformed: %+v", runner.calls[2:])
	}
}

func TestFirewallEnableRollsBackPartiallyCreatedRules(t *testing.T) {
	runner := &recordingRunner{failAt: 4, missingSets: true}
	err := NewManager(runner).Enable(context.Background(), "172.28.64.0/20")
	if err == nil {
		t.Fatal("Enable succeeded despite failure creating HTTPS rule")
	}
	if len(runner.calls) != 6 || runner.calls[4].args[2] != "delete" || !strings.Contains(strings.Join(runner.calls[4].args, " "), httpRuleName) || !strings.Contains(strings.Join(runner.calls[5].args, " "), httpsRuleName) {
		t.Fatalf("partial firewall rule was not rolled back: %+v", runner.calls)
	}
}
