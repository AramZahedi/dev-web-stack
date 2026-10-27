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
	calls  []commandCall
	failAt int
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, commandCall{name: name, args: append([]string(nil), args...)})
	if r.failAt > 0 && len(r.calls) == r.failAt {
		return nil, errors.New("injected command failure")
	}
	return nil, nil
}

func TestFirewallCreatesFixedProxyRulesForValidatedWSLSubnet(t *testing.T) {
	runner := &recordingRunner{}
	manager := NewManager(runner)
	if err := manager.Enable(context.Background(), "172.28.64.0/20"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v; want HTTP and HTTPS rules", runner.calls)
	}
	joined := strings.Join(runner.calls[0].args, " ") + " " + strings.Join(runner.calls[1].args, " ")
	for _, value := range []string{"8080", "8443", "172.28.64.0/20", "enable=yes", "action=allow", "dir=in"} {
		if !strings.Contains(joined, value) {
			t.Errorf("firewall rules lack %q: %s", value, joined)
		}
	}
}

func TestFirewallRejectsInvalidSubnetBeforeCommands(t *testing.T) {
	runner := &recordingRunner{}
	if err := NewManager(runner).Enable(context.Background(), "0.0.0.0/0"); err == nil {
		t.Fatal("Enable accepted an all-addresses rule")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid subnet ran commands: %+v", runner.calls)
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
	runner := &recordingRunner{failAt: 2}
	err := NewManager(runner).Enable(context.Background(), "172.28.64.0/20")
	if err == nil {
		t.Fatal("Enable succeeded despite failure creating HTTPS rule")
	}
	if len(runner.calls) != 4 || runner.calls[2].args[2] != "delete" || !strings.Contains(strings.Join(runner.calls[2].args, " "), httpRuleName) || !strings.Contains(strings.Join(runner.calls[3].args, " "), httpsRuleName) {
		t.Fatalf("partial firewall rule was not rolled back: %+v", runner.calls)
	}
}
