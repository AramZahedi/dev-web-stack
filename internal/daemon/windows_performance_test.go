//go:build windows

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type fakePerformanceAgent struct {
	applied   bool
	reset     bool
	applyErr  error
	applyReqs []agentproto.PerformanceApplyRequest
	bootAt    *time.Time
}

func (f *fakePerformanceAgent) PerformanceStatus(context.Context) (agentproto.PerformanceStatusResponse, error) {
	return agentproto.PerformanceStatusResponse{
		Targets: []agentproto.PerformanceTargetStatus{{Target: "wsl", Available: true, Applied: f.applied}},
		BootAt:  f.bootAt,
	}, nil
}
func (f *fakePerformanceAgent) PerformanceApply(_ context.Context, req agentproto.PerformanceApplyRequest) (agentproto.PerformanceApplyResponse, error) {
	f.applyReqs = append(f.applyReqs, req)
	if f.applyErr != nil {
		return agentproto.PerformanceApplyResponse{}, f.applyErr
	}
	f.applied = true
	return agentproto.PerformanceApplyResponse{Applied: true, AppliedAt: time.Now()}, nil
}
func (f *fakePerformanceAgent) PerformanceReset(context.Context) (agentproto.PerformanceResetResponse, error) {
	f.applied = false
	f.reset = true
	return agentproto.PerformanceResetResponse{Restored: true, Targets: []agentproto.PerformanceTargetStatus{{Target: "wsl", Available: true}}}, nil
}

type fakePerformanceWindows struct {
	applied  bool
	reset    bool
	applyErr error
	req      helperproto.PerformanceApplyRequest
}

func (f *fakePerformanceWindows) PerformanceStatus(context.Context) (helperproto.PerformanceStatusResponse, error) {
	return helperproto.PerformanceStatusResponse{Targets: []helperproto.PerformanceTargetStatus{{Target: "windows", Available: true, Applied: f.applied}}}, nil
}
func (f *fakePerformanceWindows) PerformanceApply(_ context.Context, req helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error) {
	f.req = req
	if f.applyErr != nil {
		return helperproto.PerformanceApplyResponse{}, f.applyErr
	}
	f.applied = true
	return helperproto.PerformanceApplyResponse{Applied: true, AppliedAt: time.Now(), Targets: []helperproto.PerformanceTargetStatus{{Target: "windows", Available: true, Applied: true}}}, nil
}
func (f *fakePerformanceWindows) PerformanceReset(context.Context, helperproto.PerformanceResetRequest) (helperproto.PerformanceResetResponse, error) {
	f.applied = false
	f.reset = true
	return helperproto.PerformanceResetResponse{Restored: true, Targets: []helperproto.PerformanceTargetStatus{{Target: "windows", Available: true}}}, nil
}

func testPerformanceRequest() helperproto.PerformanceApplyRequest {
	return helperproto.PerformanceApplyRequest{
		Profile: "throughput",
		Host: helperproto.PerformanceHostValues{
			FileMax: 1 << 20, Somaxconn: 4096, NetdevMaxBacklog: 4096,
			IPLocalPortMin: 10240, IPLocalPortMax: 65535, NoFileSoft: 65536, NoFileHard: 1 << 20,
		},
		Windows: &helperproto.PerformanceWindowsValues{
			PowerPlan: "high_performance", DynamicPortMin: 32768, DynamicPortMax: 60999,
			TCPTimedWaitDelaySeconds: 30, NetworkThrottlingIndex: ^uint32(0), SystemResponsiveness: 0,
			DisableLastAccess: true, Disable8dot3: true, DefenderExclusions: true,
		},
		WSLVM: &helperproto.PerformanceWSLVMValues{MemoryMB: 8192, Processors: 8, SwapMB: 2048},
	}
}

func TestWindowsPerformanceHostAppliesAllLayers(t *testing.T) {
	home := t.TempDir()
	userHome := t.TempDir()
	agent := &fakePerformanceAgent{}
	windows := &fakePerformanceWindows{}
	host, err := newWindowsPerformanceHost(agent, windows, home)
	if err != nil {
		t.Fatal(err)
	}
	host.userHome = userHome
	host.now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }

	resp, err := host.PerformanceApply(context.Background(), testPerformanceRequest())
	if err != nil {
		t.Fatalf("PerformanceApply: %v", err)
	}
	if !resp.Applied || !agent.applied || !windows.applied {
		t.Fatalf("apply response = %+v agent=%t windows=%t", resp, agent.applied, windows.applied)
	}
	if len(agent.applyReqs) != 1 || agent.applyReqs[0].Profile != "throughput" {
		t.Fatalf("agent requests = %+v", agent.applyReqs)
	}
	if windows.req.Windows == nil || windows.req.Windows.PowerPlan != "high_performance" {
		t.Fatalf("windows request = %+v", windows.req)
	}
	data, err := os.ReadFile(filepath.Join(userHome, ".wslconfig"))
	if err != nil {
		t.Fatalf("read .wslconfig: %v", err)
	}
	if string(data) == "" || filepath.Dir(filepath.Join(home, "state", "performance-wslconfig.json")) == "" {
		t.Fatal("wslconfig or state was not written")
	}
	targets := map[string]bool{}
	for _, target := range resp.Targets {
		targets[target.Target] = true
	}
	for _, want := range []string{"wsl", "windows", "wsl-vm"} {
		if !targets[want] {
			t.Errorf("apply targets missing %q: %+v", want, resp.Targets)
		}
	}

	if _, err := host.PerformanceReset(context.Background(), helperproto.PerformanceResetRequest{}); err != nil {
		t.Fatalf("PerformanceReset: %v", err)
	}
	if !agent.reset || !windows.reset {
		t.Fatalf("reset agent=%t windows=%t", agent.reset, windows.reset)
	}
	if _, err := os.Stat(filepath.Join(userHome, ".wslconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reset left .wslconfig: %v", err)
	}
}

func TestWindowsPerformanceReapplyKeepsWSLVMState(t *testing.T) {
	home := t.TempDir()
	host, err := newWindowsPerformanceHost(&fakePerformanceAgent{}, &fakePerformanceWindows{}, home)
	if err != nil {
		t.Fatal(err)
	}
	host.userHome = t.TempDir()
	appliedAt := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	host.now = func() time.Time { return appliedAt }
	if _, err := host.PerformanceApply(context.Background(), testPerformanceRequest()); err != nil {
		t.Fatal(err)
	}
	var first wslConfigState
	data, err := os.ReadFile(host.wslConfigStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatal(err)
	}

	host.now = func() time.Time { return appliedAt.Add(time.Hour) }
	if _, err := host.PerformanceApply(context.Background(), testPerformanceRequest()); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(host.wslConfigStatePath())
	if err != nil {
		t.Fatal(err)
	}
	var second wslConfigState
	if err := json.Unmarshal(data, &second); err != nil {
		t.Fatal(err)
	}
	if !second.At.Equal(first.At) {
		t.Fatalf("identical re-apply moved the WSL-VM apply time from %s to %s", first.At, second.At)
	}
}

func TestWSLVMRestartRequiredFollowsTheVMBoot(t *testing.T) {
	home := t.TempDir()
	agent := &fakePerformanceAgent{}
	host, err := newWindowsPerformanceHost(agent, &fakePerformanceWindows{}, home)
	if err != nil {
		t.Fatal(err)
	}
	host.userHome = t.TempDir()
	appliedAt := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if err := host.writeWSLVMState(wslConfigState{At: appliedAt, Existed: true}); err != nil {
		t.Fatal(err)
	}

	beforeApply := appliedAt.Add(-time.Minute)
	agent.bootAt = &beforeApply
	status, err := host.PerformanceStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if target := performanceTarget(status, "wsl-vm"); target == nil || !target.RestartRequired {
		t.Fatalf("wsl-vm before reboot = %+v, want restart required", target)
	}

	afterApply := appliedAt.Add(time.Minute)
	agent.bootAt = &afterApply
	status, err = host.PerformanceStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if target := performanceTarget(status, "wsl-vm"); target == nil || target.RestartRequired {
		t.Fatalf("wsl-vm after reboot = %+v, want no restart", target)
	}

	agent.bootAt = nil
	status, err = host.PerformanceStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if target := performanceTarget(status, "wsl-vm"); target == nil || !target.RestartRequired {
		t.Fatalf("wsl-vm without a boot time = %+v, want conservative restart", target)
	}
}

func performanceTarget(status helperproto.PerformanceStatusResponse, name string) *helperproto.PerformanceTargetStatus {
	for index := range status.Targets {
		if status.Targets[index].Target == name {
			return &status.Targets[index]
		}
	}
	return nil
}

func TestWindowsPerformanceHostRollsBackWSLWhenWindowsFails(t *testing.T) {
	home := t.TempDir()
	agent := &fakePerformanceAgent{}
	windows := &fakePerformanceWindows{applyErr: errors.New("windows failed")}
	host, err := newWindowsPerformanceHost(agent, windows, home)
	if err != nil {
		t.Fatal(err)
	}
	host.userHome = t.TempDir()
	if _, err := host.PerformanceApply(context.Background(), testPerformanceRequest()); err == nil {
		t.Fatal("apply succeeded despite Windows failure")
	}
	if !agent.reset {
		t.Fatal("WSL tuning was not rolled back after Windows failure")
	}
}
