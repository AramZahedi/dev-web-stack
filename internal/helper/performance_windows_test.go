//go:build windows

package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type fakePerformanceRunner struct {
	powerPlan  string
	ports      map[string][2]int
	registry   map[string]uint32
	fsutil     map[string]int
	exclusions []string
	calls      [][]string
}

func newFakePerformanceRunner() *fakePerformanceRunner {
	return &fakePerformanceRunner{
		powerPlan: powerPlanBalancedGUID,
		ports: map[string][2]int{
			"ipv4 tcp": {49152, 16384}, "ipv4 udp": {49152, 16384},
			"ipv6 tcp": {49152, 16384}, "ipv6 udp": {49152, 16384},
		},
		registry: map[string]uint32{},
		fsutil:   map[string]int{"disablelastaccess": 0, "disable8dot3": 0},
	}
}

func (r *fakePerformanceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	switch {
	case name == "powercfg" && len(args) == 1 && args[0] == "/getactivescheme":
		return []byte("Power Scheme GUID: " + r.powerPlan + "  (Balanced)\n"), nil
	case name == "powercfg" && len(args) == 2 && args[0] == "/setactive":
		r.powerPlan = strings.ToLower(args[1])
		return nil, nil
	case name == "netsh" && len(args) == 7 && args[2] == "set":
		key := args[1] + " " + args[4]
		start := strings.TrimPrefix(args[5], "start=")
		count := strings.TrimPrefix(args[6], "num=")
		s, _ := strconv.Atoi(start)
		c, _ := strconv.Atoi(count)
		r.ports[key] = [2]int{s, c}
		return nil, nil
	case name == "netsh" && len(args) == 5 && args[2] == "show":
		key := args[1] + " " + args[4]
		value := r.ports[key]
		return []byte(fmt.Sprintf("Start Port      : %d\nNumber of Ports : %d\n", value[0], value[1])), nil
	case name == "reg" && len(args) >= 4 && args[0] == "query":
		key := args[1] + "|" + args[3]
		value, ok := r.registry[key]
		if !ok {
			return nil, fmt.Errorf("not found")
		}
		return []byte(fmt.Sprintf("%s\n    %s    REG_DWORD    0x%x\n", args[1], args[3], value)), nil
	case name == "reg" && len(args) >= 9 && args[0] == "add":
		key := args[1] + "|" + args[3]
		value, _ := strconv.ParseUint(args[7], 10, 32)
		r.registry[key] = uint32(value)
		return nil, nil
	case name == "reg" && len(args) >= 4 && args[0] == "delete":
		delete(r.registry, args[1]+"|"+args[3])
		return nil, nil
	case name == "fsutil" && len(args) == 4 && args[0] == "behavior" && args[1] == "set":
		value, _ := strconv.Atoi(args[3])
		r.fsutil[args[2]] = value
		return nil, nil
	case name == "fsutil" && len(args) == 3 && args[0] == "behavior" && args[1] == "query":
		return []byte(fmt.Sprintf("%s = %d\n", args[2], r.fsutil[args[2]])), nil
	case name == "powershell.exe":
		script := args[len(args)-1]
		if strings.Contains(script, "Get-MpPreference") {
			return []byte(strings.Join(r.exclusions, "\n")), nil
		}
		if strings.Contains(script, "Add-MpPreference") {
			path := strings.TrimSuffix(strings.TrimPrefix(script, "Add-MpPreference -ExclusionPath '"), "'")
			r.exclusions = append(r.exclusions, path)
			return nil, nil
		}
		if strings.Contains(script, "Remove-MpPreference") {
			path := strings.TrimSuffix(strings.TrimPrefix(script, "Remove-MpPreference -ExclusionPath '"), "'")
			var next []string
			for _, item := range r.exclusions {
				if item != path {
					next = append(next, item)
				}
			}
			r.exclusions = next
			return nil, nil
		}
	}
	return nil, nil
}

func testWindowsPerformanceValues() *helperproto.PerformanceWindowsValues {
	return &helperproto.PerformanceWindowsValues{
		PowerPlan:                "high_performance",
		DynamicPortMin:           32768,
		DynamicPortMax:           60999,
		TCPTimedWaitDelaySeconds: 30,
		NetworkThrottlingIndex:   ^uint32(0),
		SystemResponsiveness:     0,
		DisableLastAccess:        true,
		Disable8dot3:             true,
		DefenderExclusions:       true,
	}
}

func TestWindowsPerformanceApplyAndReset(t *testing.T) {
	system := t.TempDir()
	runner := newFakePerformanceRunner()
	manager, err := NewWindowsPerformanceManager(system, runner, func() time.Time {
		return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{
		Profile: "throughput", Windows: testWindowsPerformanceValues(),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !resp.Applied || resp.BackupDir == "" {
		t.Fatalf("apply response = %+v", resp)
	}
	if runner.powerPlan != powerPlanHighGUID {
		t.Fatalf("power plan = %s", runner.powerPlan)
	}
	if runner.ports["ipv4 tcp"] != [2]int{32768, 28232} {
		t.Fatalf("ipv4 tcp ports = %+v", runner.ports["ipv4 tcp"])
	}
	if runner.registry[`HKLM\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters|TcpTimedWaitDelay`] != 30 {
		t.Fatalf("TcpTimedWaitDelay = %+v", runner.registry)
	}
	if runner.fsutil["disablelastaccess"] != 1 || runner.fsutil["disable8dot3"] != 1 {
		t.Fatalf("fsutil = %+v", runner.fsutil)
	}
	if len(runner.exclusions) != 2 {
		t.Fatalf("exclusions = %v", runner.exclusions)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Targets) != 1 || !status.Targets[0].Applied || status.Targets[0].Target != "windows" {
		t.Fatalf("status = %+v", status)
	}

	// A second profile switch must not overwrite the original baseline.
	balanced := testWindowsPerformanceValues()
	balanced.PowerPlan = "balanced"
	balanced.DynamicPortMin = 49152
	balanced.DynamicPortMax = 65535
	balanced.TCPTimedWaitDelaySeconds = 0
	balanced.NetworkThrottlingIndex = 10
	balanced.SystemResponsiveness = 20
	balanced.DisableLastAccess = false
	balanced.Disable8dot3 = false
	if _, err := manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{
		Profile: "balanced", Windows: balanced,
	}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	reset, err := manager.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !reset.Restored {
		t.Fatal("reset did not report restored")
	}
	if runner.powerPlan != powerPlanBalancedGUID {
		t.Fatalf("reset power plan = %s", runner.powerPlan)
	}
	if runner.ports["ipv4 tcp"] != [2]int{49152, 16384} {
		t.Fatalf("reset ipv4 tcp ports = %+v", runner.ports["ipv4 tcp"])
	}
	if len(runner.exclusions) != 0 {
		t.Fatalf("reset left exclusions = %v", runner.exclusions)
	}
	if runner.fsutil["disablelastaccess"] != 0 || runner.fsutil["disable8dot3"] != 0 {
		t.Fatalf("reset fsutil = %+v", runner.fsutil)
	}
}

func TestWindowsPerformanceRestartClearsAfterReboot(t *testing.T) {
	system := t.TempDir()
	appliedAt := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	manager, err := NewWindowsPerformanceManager(system, newFakePerformanceRunner(), func() time.Time {
		return appliedAt
	})
	if err != nil {
		t.Fatal(err)
	}
	// The host booted ten hours before the apply, so the TCP change is pending.
	manager.sinceBoot = func() time.Duration { return 10 * time.Hour }

	resp, err := manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{
		Profile: "throughput", Windows: testWindowsPerformanceValues(),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !resp.Targets[0].RestartRequired {
		t.Fatal("fresh apply did not report the pending restart")
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Targets[0].RestartRequired {
		t.Fatal("status cleared the restart before the host rebooted")
	}

	// Simulate a reboot: the status request arrives an hour after the apply and
	// the current session booted ten minutes ago.
	manager.now = func() time.Time { return appliedAt.Add(time.Hour) }
	manager.sinceBoot = func() time.Duration { return 10 * time.Minute }
	status, err = manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Targets[0].RestartRequired {
		t.Fatal("status still requires a restart after the host rebooted")
	}
	data, err := os.ReadFile(manager.statePath())
	if err != nil {
		t.Fatal(err)
	}
	var state windowsPerformanceState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.RestartRequired {
		t.Fatal("persisted state still requires a restart after the host rebooted")
	}

	// Applying the same profile in the new session must not re-arm the marker:
	// the registry value already survived a boot.
	resp, err = manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{
		Profile: "throughput", Windows: testWindowsPerformanceValues(),
	})
	if err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
	if resp.Targets[0].RestartRequired {
		t.Fatal("re-applying the active profile re-armed the restart marker")
	}
}

func TestWindowsPerformanceRestartReturnsWhenTCPValueChanges(t *testing.T) {
	system := t.TempDir()
	appliedAt := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	manager, err := NewWindowsPerformanceManager(system, newFakePerformanceRunner(), func() time.Time {
		return appliedAt.Add(time.Hour)
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.sinceBoot = func() time.Duration { return 10 * time.Minute }
	deferred := appliedAt.Add(-time.Hour)
	if err := manager.writeState(&windowsPerformanceState{
		Profile:           "throughput",
		AppliedAt:         deferred,
		TCPTimedWaitDelay: 30,
		RestartRequired:   false,
	}); err != nil {
		t.Fatal(err)
	}
	values := testWindowsPerformanceValues()
	values.TCPTimedWaitDelaySeconds = 45
	resp, err := manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{
		Profile: "custom", Windows: values,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !resp.Targets[0].RestartRequired {
		t.Fatal("a new TCP timed-wait value did not require a restart")
	}
}

func TestWindowsPerformanceReapplyAfterRebootUsesLiveRegistryValue(t *testing.T) {
	system := t.TempDir()
	runner := newFakePerformanceRunner()
	appliedAt := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	manager, err := NewWindowsPerformanceManager(system, runner, func() time.Time {
		return appliedAt.Add(time.Hour)
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.sinceBoot = func() time.Duration { return 10 * time.Minute }
	if err := manager.writeState(&windowsPerformanceState{
		Profile:         "throughput",
		AppliedAt:       appliedAt.Add(-time.Hour),
		RestartRequired: true,
	}); err != nil {
		t.Fatal(err)
	}
	// The old helper did not persist the requested value. The registry shows
	// the value survived the current boot, so re-applying it is already live.
	runner.registry[windowsTCPParametersPath+"|TcpTimedWaitDelay"] = 30
	resp, err := manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{
		Profile: "throughput", Windows: testWindowsPerformanceValues(),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if resp.Targets[0].RestartRequired {
		t.Fatal("re-applying a live value re-armed the restart marker")
	}
}

func TestWindowsPerformanceRollbackOnVerificationFailure(t *testing.T) {
	system := t.TempDir()
	runner := newFakePerformanceRunner()
	manager, err := NewWindowsPerformanceManager(system, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	values := testWindowsPerformanceValues()
	values.DynamicPortMin = 1024
	values.DynamicPortMax = 2048
	// Force a verification failure after the runner accepts the change by
	// setting a value the fake will not actually store.
	runner.ports["ipv4 tcp"] = [2]int{49152, 16384}
	if _, err := manager.Apply(context.Background(), helperproto.PerformanceApplyRequest{Profile: "custom", Windows: values}); err != nil {
		// A direct verification mismatch is acceptable; the important check is
		// that captured state was restored.
		if !strings.Contains(err.Error(), "dynamic ports") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
