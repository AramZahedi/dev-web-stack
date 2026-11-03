// Package agentperf applies the allowlisted WSL host tuning profile through
// the authenticated root agent. It never accepts arbitrary paths or commands.
package agentperf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/performance"
)

const (
	defaultSysctlPath = "/etc/sysctl.d/99-devstack-performance.conf"
	defaultLimitsPath = "/etc/security/limits.d/99-devstack-performance.conf"
	maxBodyBytes      = 64 << 10
)

// Runner executes the fixed sysctl refresh command.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Config wires the module. The production agent uses the fixed system paths;
// tests override the roots.
type Config struct {
	Root       string
	SysctlPath string
	LimitsPath string
	ProcRoot   string
	StatePath  string
	Runner     Runner
	Now        func() time.Time
}

// Module owns the WSL host tuning endpoints.
type Module struct {
	root       string
	sysctlPath string
	limitsPath string
	procRoot   string
	statePath  string
	runner     Runner
	now        func() time.Time
	mu         sync.Mutex
}

var _ agentapi.Module = (*Module)(nil)

// New constructs the module.
func New(cfg Config) (*Module, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		return nil, errors.New("agentperf: root is required")
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("agentperf: root must be absolute")
	}
	sysctl := strings.TrimSpace(cfg.SysctlPath)
	if sysctl == "" {
		sysctl = defaultSysctlPath
	}
	limits := strings.TrimSpace(cfg.LimitsPath)
	if limits == "" {
		limits = defaultLimitsPath
	}
	procRoot := strings.TrimSpace(cfg.ProcRoot)
	if procRoot == "" {
		procRoot = "/proc/sys"
	}
	state := strings.TrimSpace(cfg.StatePath)
	if state == "" {
		state = filepath.Join(root, "state", "performance-host.json")
	}
	for name, path := range map[string]string{"sysctl": sysctl, "limits": limits, "proc": procRoot, "state": state} {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("agentperf: %s path must be absolute", name)
		}
	}
	runner := cfg.Runner
	if runner == nil {
		runner = execRunner{}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Module{
		root: root, sysctlPath: sysctl, limitsPath: limits, procRoot: procRoot,
		statePath: state, runner: runner, now: now,
	}, nil
}

// Name implements agentapi.Module.
func (*Module) Name() string { return "performance" }

// MountPath implements agentapi.Module.
func (*Module) MountPath() string { return "/v1/performance/" }

// Handler serves the authenticated performance endpoints.
func (m *Module) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/performance/status", m.handleStatus)
	mux.HandleFunc("POST /v1/performance/apply", m.handleApply)
	mux.HandleFunc("POST /v1/performance/reset", m.handleReset)
	return mux
}

type hostState struct {
	Profile       string            `json:"profile"`
	BackupDir     string            `json:"backup_dir"`
	AppliedAt     time.Time         `json:"applied_at"`
	SysctlExisted bool              `json:"sysctl_existed"`
	LimitsExisted bool              `json:"limits_existed"`
	Sysctl        map[string]string `json:"sysctl,omitempty"`
}

func (m *Module) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := m.Status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (m *Module) handleApply(w http.ResponseWriter, r *http.Request) {
	var req agentproto.PerformanceApplyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := m.Apply(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (m *Module) handleReset(w http.ResponseWriter, r *http.Request) {
	resp, err := m.Reset(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Status reports the managed WSL host files and last apply state.
func (m *Module) Status() (agentproto.PerformanceStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, _ := m.readState()
	sysctl, sysctlErr := os.ReadFile(m.sysctlPath)
	limits, limitsErr := os.ReadFile(m.limitsPath)
	target := agentproto.PerformanceTargetStatus{
		Target:    "wsl",
		Available: true,
		Paths:     []string{m.sysctlPath, m.limitsPath},
	}
	if sysctlErr == nil && len(sysctl) > 0 {
		target.Managed = true
		target.Applied = true
	}
	if limitsErr == nil && len(limits) > 0 {
		target.Managed = true
		target.Applied = true
	}
	if sysctlErr != nil && !errors.Is(sysctlErr, os.ErrNotExist) {
		target.Error = sysctlErr.Error()
	}
	if limitsErr != nil && !errors.Is(limitsErr, os.ErrNotExist) {
		if target.Error != "" {
			target.Error += "; "
		}
		target.Error += limitsErr.Error()
	}
	resp := agentproto.PerformanceStatusResponse{Targets: []agentproto.PerformanceTargetStatus{target}}
	if len(sysctl) > 0 {
		resp.SysctlContent = sysctl
	}
	if len(limits) > 0 {
		resp.LimitsContent = limits
	}
	if state != nil {
		target.BackupDir = state.BackupDir
		resp.Targets[0] = target
		applied := state.AppliedAt
		resp.AppliedAt = &applied
	}
	return resp, nil
}

// Apply writes the allowlisted sysctl/limits files, refreshes the kernel, and
// verifies every sysctl. Any failure restores the prior files.
func (m *Module) Apply(ctx context.Context, req agentproto.PerformanceApplyRequest) (agentproto.PerformanceApplyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return agentproto.PerformanceApplyResponse{}, err
	}
	profile, err := performance.ParseProfile(req.Profile)
	if err != nil {
		return agentproto.PerformanceApplyResponse{}, err
	}
	host := performance.HostValues{
		FileMax:          req.Host.FileMax,
		Somaxconn:        req.Host.Somaxconn,
		NetdevMaxBacklog: req.Host.NetdevMaxBacklog,
		IPLocalPortRange: performance.PortRange{Min: req.Host.IPLocalPortMin, Max: req.Host.IPLocalPortMax},
		NoFileSoft:       req.Host.NoFileSoft,
		NoFileHard:       req.Host.NoFileHard,
		User:             req.Host.User,
		Group:            req.Host.Group,
	}
	sysctl, err := performance.RenderHostSysctl(profile, host)
	if err != nil {
		return agentproto.PerformanceApplyResponse{}, err
	}
	limits, err := performance.RenderHostLimits(profile, host)
	if err != nil {
		return agentproto.PerformanceApplyResponse{}, err
	}
	now := m.now().UTC()
	backupDir := filepath.Join(m.root, "backups", "performance", now.Format("20060102T150405.000000000Z"), "host")
	state := hostState{Profile: string(profile), BackupDir: backupDir, AppliedAt: now}
	existing, err := m.readState()
	if err != nil {
		return agentproto.PerformanceApplyResponse{}, err
	}
	var priorSysctl, priorLimits []byte
	var sysctlExisted, limitsExisted bool
	if existing != nil {
		// Keep the original pre-devstack snapshot across profile switches.
		state.BackupDir = existing.BackupDir
		state.SysctlExisted = existing.SysctlExisted
		state.LimitsExisted = existing.LimitsExisted
		state.Sysctl = existing.Sysctl
		priorSysctl, sysctlExisted, err = m.readBackup(state.BackupDir, "sysctl.d", m.sysctlPath)
		if err != nil {
			return agentproto.PerformanceApplyResponse{}, err
		}
		priorLimits, limitsExisted, err = m.readBackup(state.BackupDir, "limits.d", m.limitsPath)
		if err != nil {
			return agentproto.PerformanceApplyResponse{}, err
		}
	} else {
		priorSysctl, sysctlExisted, err = m.snapshot(m.sysctlPath, filepath.Join(backupDir, "sysctl.d", filepath.Base(m.sysctlPath)))
		if err != nil {
			return agentproto.PerformanceApplyResponse{}, err
		}
		state.SysctlExisted = sysctlExisted
		priorLimits, limitsExisted, err = m.snapshot(m.limitsPath, filepath.Join(backupDir, "limits.d", filepath.Base(m.limitsPath)))
		if err != nil {
			return agentproto.PerformanceApplyResponse{}, err
		}
		state.LimitsExisted = limitsExisted
	}
	if len(state.Sysctl) == 0 {
		state.Sysctl = m.readSysctlValues()
	}

	if err := writeAtomic(m.sysctlPath, sysctl, 0o644); err != nil {
		return agentproto.PerformanceApplyResponse{}, fmt.Errorf("agentperf: write sysctl: %w", err)
	}
	if err := writeAtomic(m.limitsPath, limits, 0o644); err != nil {
		_ = m.restoreFile(m.sysctlPath, priorSysctl, sysctlExisted)
		return agentproto.PerformanceApplyResponse{}, fmt.Errorf("agentperf: write limits: %w", err)
	}
	if out, err := m.runner.Run(ctx, "sysctl", "--system"); err != nil {
		_ = m.restoreFile(m.sysctlPath, priorSysctl, sysctlExisted)
		_ = m.restoreFile(m.limitsPath, priorLimits, limitsExisted)
		_, _ = m.runner.Run(context.WithoutCancel(ctx), "sysctl", "--system")
		return agentproto.PerformanceApplyResponse{}, fmt.Errorf("agentperf: sysctl --system: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := m.verifySysctl(host); err != nil {
		_ = m.restoreFile(m.sysctlPath, priorSysctl, sysctlExisted)
		_ = m.restoreFile(m.limitsPath, priorLimits, limitsExisted)
		_, _ = m.runner.Run(context.WithoutCancel(ctx), "sysctl", "--system")
		return agentproto.PerformanceApplyResponse{}, err
	}
	if err := m.writeState(state); err != nil {
		return agentproto.PerformanceApplyResponse{}, err
	}
	resp := agentproto.PerformanceApplyResponse{
		Applied:   true,
		BackupDir: backupDir,
		AppliedAt: now,
		Targets: []agentproto.PerformanceTargetStatus{{
			Target:    "wsl",
			Available: true,
			Managed:   true,
			Applied:   true,
			Paths:     []string{m.sysctlPath, m.limitsPath},
			BackupDir: backupDir,
		}},
		Steps: []agentproto.PerformanceStep{
			{Name: "sysctl-file", Status: "ok", Detail: m.sysctlPath},
			{Name: "limits-file", Status: "ok", Detail: m.limitsPath + " (new sessions)"},
			{Name: "sysctl-refresh", Status: "ok", Detail: "sysctl --system"},
			{Name: "verify", Status: "ok", Detail: "allowlisted sysctl values verified from " + m.procRoot},
		},
	}
	return resp, nil
}

// Reset restores the previous files or removes the managed files.
func (m *Module) Reset(ctx context.Context) (agentproto.PerformanceResetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return agentproto.PerformanceResetResponse{}, err
	}
	state, _ := m.readState()
	result := agentproto.PerformanceResetResponse{Restored: true}
	if state != nil {
		result.BackupDir = state.BackupDir
		priorSysctl, sysctlExisted, err := m.readBackup(state.BackupDir, "sysctl.d", m.sysctlPath)
		if err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
		priorLimits, limitsExisted, err := m.readBackup(state.BackupDir, "limits.d", m.limitsPath)
		if err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
		if err := m.restoreFile(m.sysctlPath, priorSysctl, sysctlExisted); err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
		if err := m.restoreFile(m.limitsPath, priorLimits, limitsExisted); err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
	} else {
		if err := m.removeManaged(m.sysctlPath); err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
		if err := m.removeManaged(m.limitsPath); err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
	}
	if out, err := m.runner.Run(ctx, "sysctl", "--system"); err != nil {
		return agentproto.PerformanceResetResponse{}, fmt.Errorf("agentperf: sysctl --system: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if state != nil {
		if err := m.restoreLiveSysctl(ctx, state.Sysctl); err != nil {
			return agentproto.PerformanceResetResponse{}, err
		}
	}
	if err := os.Remove(m.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return agentproto.PerformanceResetResponse{}, err
	}
	result.Targets = []agentproto.PerformanceTargetStatus{{
		Target:    "wsl",
		Available: true,
		Managed:   false,
		Applied:   false,
		Paths:     []string{m.sysctlPath, m.limitsPath},
		BackupDir: result.BackupDir,
	}}
	result.Steps = []agentproto.PerformanceStep{
		{Name: "restore", Status: "ok", Detail: "previous WSL host files restored or removed"},
		{Name: "sysctl-refresh", Status: "ok", Detail: "sysctl --system"},
		{Name: "limits", Status: "ok", Detail: "limits apply to new sessions"},
	}
	return result, nil
}

func (m *Module) snapshot(path, backup string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if err := writeAtomic(backup, data, info.Mode().Perm()); err != nil {
		return nil, false, fmt.Errorf("agentperf: backup %s: %w", path, err)
	}
	return data, true, nil
}

func (m *Module) readBackup(dir, sub, path string) ([]byte, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, sub, filepath.Base(path)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (m *Module) restoreFile(path string, data []byte, existed bool) error {
	if existed {
		return writeAtomic(path, data, 0o644)
	}
	return m.removeManaged(path)
}

func (m *Module) removeManaged(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "Code generated by devstack performance profile") {
		return fmt.Errorf("agentperf: refusing to remove unmanaged file %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Module) verifySysctl(host performance.HostValues) error {
	checks := map[string]string{
		"net/core/somaxconn":           fmt.Sprint(host.Somaxconn),
		"net/core/netdev_max_backlog":  fmt.Sprint(host.NetdevMaxBacklog),
		"fs/file-max":                  fmt.Sprint(host.FileMax),
		"net/ipv4/ip_local_port_range": fmt.Sprintf("%d\t%d", host.IPLocalPortRange.Min, host.IPLocalPortRange.Max),
	}
	for rel, want := range checks {
		data, err := os.ReadFile(filepath.Join(m.procRoot, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("agentperf: verify %s: %w", rel, err)
		}
		got := strings.Join(strings.Fields(string(data)), " ")
		if got != strings.Join(strings.Fields(want), " ") {
			return fmt.Errorf("agentperf: verify %s: got %q, want %q", rel, got, want)
		}
	}
	return nil
}

func (m *Module) readSysctlValues() map[string]string {
	out := map[string]string{}
	for _, key := range []string{"fs.file-max", "net.core.somaxconn", "net.core.netdev_max_backlog", "net.ipv4.ip_local_port_range"} {
		rel := strings.ReplaceAll(key, ".", "/")
		data, err := os.ReadFile(filepath.Join(m.procRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		out[key] = strings.Join(strings.Fields(string(data)), " ")
	}
	return out
}

func (m *Module) restoreLiveSysctl(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if out, err := m.runner.Run(ctx, "sysctl", "-w", key+"="+value); err != nil {
			return fmt.Errorf("agentperf: restore %s: %w: %s", key, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (m *Module) readState() (*hostState, error) {
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state hostState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (m *Module) writeState(state hostState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeAtomic(m.statePath, data, 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".devstack-perf-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxBodyBytes {
		return errors.New("request body is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
