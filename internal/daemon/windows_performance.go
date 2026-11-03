//go:build windows

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/wslconfig"
)

type performanceAgent interface {
	PerformanceStatus(context.Context) (agentproto.PerformanceStatusResponse, error)
	PerformanceApply(context.Context, agentproto.PerformanceApplyRequest) (agentproto.PerformanceApplyResponse, error)
	PerformanceReset(context.Context) (agentproto.PerformanceResetResponse, error)
}

type performanceWindowsHelper interface {
	PerformanceStatus(context.Context) (helperproto.PerformanceStatusResponse, error)
	PerformanceApply(context.Context, helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error)
	PerformanceReset(context.Context, helperproto.PerformanceResetRequest) (helperproto.PerformanceResetResponse, error)
}

type wslConfigState struct {
	Backup  string    `json:"backup"`
	At      time.Time `json:"at"`
	Existed bool      `json:"existed"`
}

// windowsPerformanceHost combines the WSL agent, Windows helper, and .wslconfig
// into the single host-tuning surface used by the performance service.
type windowsPerformanceHost struct {
	agent    performanceAgent
	windows  performanceWindowsHelper
	home     string
	userHome string
	now      func() time.Time
}

func newWindowsPerformanceHost(agent performanceAgent, windows performanceWindowsHelper, home string) (*windowsPerformanceHost, error) {
	if agent == nil || windows == nil {
		return nil, errors.New("daemon: performance host requires the WSL agent and Windows helper")
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("daemon: resolve user home: %w", err)
	}
	return &windowsPerformanceHost{agent: agent, windows: windows, home: home, userHome: userHome, now: time.Now}, nil
}

func (h *windowsPerformanceHost) wslConfigPath() string {
	return filepath.Join(h.userHome, ".wslconfig")
}

func (h *windowsPerformanceHost) wslConfigStatePath() string {
	return filepath.Join(h.home, "state", "performance-wslconfig.json")
}

func (h *windowsPerformanceHost) PerformanceStatus(ctx context.Context) (helperproto.PerformanceStatusResponse, error) {
	resp := helperproto.PerformanceStatusResponse{}
	var agentBootAt *time.Time
	agentResp, agentErr := h.agent.PerformanceStatus(ctx)
	if agentErr != nil {
		resp.Targets = append(resp.Targets, helperproto.PerformanceTargetStatus{
			Target: "wsl", Available: false, Error: agentErr.Error(),
		})
	} else {
		agentBootAt = agentResp.BootAt
		for _, target := range agentResp.Targets {
			resp.Targets = append(resp.Targets, toHelperTarget(target))
		}
		if agentResp.AppliedAt != nil {
			resp.AppliedAt = agentResp.AppliedAt
		}
		if len(agentResp.Targets) > 0 {
			resp.SysctlPath = firstPath(agentResp.Targets[0].Paths, 0)
			resp.LimitsPath = firstPath(agentResp.Targets[0].Paths, 1)
			resp.SysctlPresent = agentResp.Targets[0].Applied
			resp.LimitsPresent = agentResp.Targets[0].Applied
		}
		resp.SysctlContent = agentResp.SysctlContent
		resp.LimitsContent = agentResp.LimitsContent
	}
	windowsResp, windowsErr := h.windows.PerformanceStatus(ctx)
	if windowsErr != nil {
		resp.Targets = append(resp.Targets, helperproto.PerformanceTargetStatus{
			Target: "windows", Available: false, Error: windowsErr.Error(),
		})
	} else {
		resp.Targets = append(resp.Targets, windowsResp.Targets...)
		if resp.BackupDir == "" {
			resp.BackupDir = windowsResp.BackupDir
		}
	}
	resp.Targets = append(resp.Targets, h.wslVMStatus(agentBootAt))
	for _, target := range resp.Targets {
		if target.Applied {
			resp.Managed = true
		}
	}
	if resp.BackupDir == "" {
		for _, target := range resp.Targets {
			if target.BackupDir != "" {
				resp.BackupDir = target.BackupDir
				break
			}
		}
	}
	return resp, nil
}

func (h *windowsPerformanceHost) PerformanceApply(ctx context.Context, req helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error) {
	agentReq := agentproto.PerformanceApplyRequest{
		Profile: req.Profile,
		Host: agentproto.PerformanceHostValues{
			FileMax: req.Host.FileMax, Somaxconn: req.Host.Somaxconn, NetdevMaxBacklog: req.Host.NetdevMaxBacklog,
			IPLocalPortMin: req.Host.IPLocalPortMin, IPLocalPortMax: req.Host.IPLocalPortMax,
			NoFileSoft: req.Host.NoFileSoft, NoFileHard: req.Host.NoFileHard,
			User: req.Host.User, Group: req.Host.Group,
		},
	}
	if req.WSLVM != nil {
		agentReq.WSLVM = agentproto.PerformanceWSLVMValues{
			MemoryMB: req.WSLVM.MemoryMB, Processors: req.WSLVM.Processors, SwapMB: req.WSLVM.SwapMB,
		}
	}
	if _, err := h.agent.PerformanceApply(ctx, agentReq); err != nil {
		return helperproto.PerformanceApplyResponse{}, fmt.Errorf("daemon: apply WSL host tuning: %w", err)
	}
	rollbackAgent := func(cause error) (helperproto.PerformanceApplyResponse, error) {
		_, resetErr := h.agent.PerformanceReset(context.WithoutCancel(ctx))
		if resetErr != nil {
			return helperproto.PerformanceApplyResponse{}, errors.Join(cause, fmt.Errorf("daemon: rollback WSL tuning: %w", resetErr))
		}
		return helperproto.PerformanceApplyResponse{}, cause
	}
	windowsResp, err := h.windows.PerformanceApply(ctx, req)
	if err != nil {
		return rollbackAgent(fmt.Errorf("daemon: apply Windows host tuning: %w", err))
	}
	if req.WSLVM != nil && req.WSLVM.MemoryMB > 0 {
		backup := filepath.Join(h.home, "backups", "performance", h.now().UTC().Format("20060102T150405.000000000Z"), ".wslconfig")
		existed := false
		var previous *wslConfigState
		if _, err := os.Stat(h.wslConfigPath()); err == nil {
			existed = true
		}
		if state, err := h.readWSLVMState(); err == nil && state != nil {
			backup = state.Backup
			existed = state.Existed
			previous = state
		}
		if !existed {
			backup = ""
		}
		_, changed, err := wslconfig.Apply(h.wslConfigPath(), backup, wslconfig.Values{
			MemoryMB: req.WSLVM.MemoryMB, Processors: req.WSLVM.Processors, SwapMB: req.WSLVM.SwapMB,
		})
		if err != nil {
			_, _ = h.windows.PerformanceReset(context.WithoutCancel(ctx), helperproto.PerformanceResetRequest{})
			return rollbackAgent(fmt.Errorf("daemon: apply .wslconfig: %w", err))
		}
		// Re-applying identical values must not restart the pending marker: the
		// VM only needs a restart when the file actually changed.
		if changed || previous == nil {
			_ = h.writeWSLVMState(wslConfigState{Backup: backup, At: h.now().UTC(), Existed: existed})
		}
	}
	resp := helperproto.PerformanceApplyResponse{
		Applied:   true,
		Profile:   req.Profile,
		AppliedAt: windowsResp.AppliedAt,
		BackupDir: windowsResp.BackupDir,
		Targets:   windowsResp.Targets,
		Steps:     windowsResp.Steps,
	}
	if resp.BackupDir == "" {
		resp.BackupDir = filepath.Join(h.home, "backups", "performance")
	}
	wslTarget := helperproto.PerformanceTargetStatus{Target: "wsl", Available: true, Managed: true, Applied: true}
	resp.Targets = append([]helperproto.PerformanceTargetStatus{wslTarget}, resp.Targets...)
	resp.Targets = append(resp.Targets, h.wslVMStatus(nil))
	return resp, nil
}

func (h *windowsPerformanceHost) PerformanceReset(ctx context.Context, _ helperproto.PerformanceResetRequest) (helperproto.PerformanceResetResponse, error) {
	var errs []error
	var targets []helperproto.PerformanceTargetStatus
	if resp, err := h.agent.PerformanceReset(ctx); err != nil {
		errs = append(errs, fmt.Errorf("daemon: reset WSL tuning: %w", err))
	} else {
		targets = append(targets, toHelperTargets(resp.Targets)...)
	}
	if resp, err := h.windows.PerformanceReset(ctx, helperproto.PerformanceResetRequest{}); err != nil {
		errs = append(errs, fmt.Errorf("daemon: reset Windows tuning: %w", err))
	} else {
		targets = append(targets, resp.Targets...)
	}
	if state, err := h.readWSLVMState(); err != nil {
		errs = append(errs, err)
	} else if state != nil {
		if err := wslconfig.Reset(h.wslConfigPath(), state.Backup); err != nil {
			errs = append(errs, fmt.Errorf("daemon: reset .wslconfig: %w", err))
		} else {
			_ = os.Remove(h.wslConfigStatePath())
			targets = append(targets, helperproto.PerformanceTargetStatus{Target: "wsl-vm", Available: true, Applied: false})
		}
	}
	if len(errs) > 0 {
		return helperproto.PerformanceResetResponse{}, errors.Join(errs...)
	}
	return helperproto.PerformanceResetResponse{Restored: true, Targets: targets}, nil
}

func (h *windowsPerformanceHost) wslVMStatus(bootAt *time.Time) helperproto.PerformanceTargetStatus {
	state, err := h.readWSLVMState()
	target := helperproto.PerformanceTargetStatus{
		Target: "wsl-vm", Available: true, Paths: []string{h.wslConfigPath()},
	}
	if err != nil {
		target.Error = err.Error()
		return target
	}
	if state != nil {
		target.Managed = true
		target.Applied = true
		if state.Backup != "" {
			target.BackupDir = filepath.Dir(state.Backup)
		}
		// A .wslconfig change only needs a restart until the WSL VM has
		// actually booted again after the apply.
		target.RestartRequired = bootAt == nil || !bootAt.After(state.At)
	}
	return target
}

func (h *windowsPerformanceHost) readWSLVMState() (*wslConfigState, error) {
	data, err := os.ReadFile(h.wslConfigStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state wslConfigState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (h *windowsPerformanceHost) writeWSLVMState(state wslConfigState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := h.wslConfigStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func toHelperTarget(target agentproto.PerformanceTargetStatus) helperproto.PerformanceTargetStatus {
	return helperproto.PerformanceTargetStatus{
		Target: target.Target, Available: target.Available, Managed: target.Managed, Applied: target.Applied,
		Paths: append([]string(nil), target.Paths...), BackupDir: target.BackupDir,
		RestartRequired: target.RestartRequired, Error: target.Error,
	}
}

func toHelperTargets(targets []agentproto.PerformanceTargetStatus) []helperproto.PerformanceTargetStatus {
	out := make([]helperproto.PerformanceTargetStatus, 0, len(targets))
	for _, target := range targets {
		out = append(out, toHelperTarget(target))
	}
	return out
}

func firstPath(paths []string, index int) string {
	if index < len(paths) {
		return paths[index]
	}
	return ""
}
