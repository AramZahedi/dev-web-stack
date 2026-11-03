//go:build windows

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

const (
	powerPlanBalancedGUID = "381b4222-f694-41f0-9685-ff5bb260df2e"
	powerPlanHighGUID     = "8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c"
)

// PerformanceCommandRunner executes fixed Windows tuning commands.
type PerformanceCommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type windowsPerformanceRunner struct{}

func (windowsPerformanceRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type windowsPortRange struct {
	Start int `json:"start"`
	Count int `json:"count"`
}

type windowsRegistryValue struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
	Value  uint32 `json:"value"`
}

type windowsPerformanceState struct {
	Profile           string                          `json:"profile"`
	BackupDir         string                          `json:"backup_dir"`
	AppliedAt         time.Time                       `json:"applied_at"`
	PowerPlan         string                          `json:"power_plan"`
	Ports             map[string]windowsPortRange     `json:"ports"`
	Registry          map[string]windowsRegistryValue `json:"registry"`
	DisableLastAccess int                             `json:"disable_last_access"`
	Disable8dot3      int                             `json:"disable_8dot3"`
	ExclusionsAdded   []string                        `json:"exclusions_added"`
	RestartRequired   bool                            `json:"restart_required"`
}

// WindowsPerformanceManager applies the allowlisted Windows host layer.
type WindowsPerformanceManager struct {
	systemDir string
	runner    PerformanceCommandRunner
	now       func() time.Time
	mu        sync.Mutex
}

// NewWindowsPerformanceManager constructs the manager.
func NewWindowsPerformanceManager(systemDir string, runner PerformanceCommandRunner, now func() time.Time) (*WindowsPerformanceManager, error) {
	dir := strings.TrimSpace(systemDir)
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, errors.New("helper: Windows performance system directory must be absolute")
	}
	if runner == nil {
		runner = windowsPerformanceRunner{}
	}
	if now == nil {
		now = time.Now
	}
	return &WindowsPerformanceManager{systemDir: dir, runner: runner, now: now}, nil
}

func (m *WindowsPerformanceManager) statePath() string {
	return filepath.Join(m.systemDir, "state", "performance-windows.json")
}

func (m *WindowsPerformanceManager) backupRoot() string {
	return filepath.Join(m.systemDir, "backups", "performance")
}

// Status reports the last Windows tuning state.
func (m *WindowsPerformanceManager) Status(_ context.Context) (helperproto.PerformanceStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readState()
	if err != nil {
		return helperproto.PerformanceStatusResponse{}, err
	}
	target := helperproto.PerformanceTargetStatus{Target: "windows", Available: true}
	if state != nil {
		target.Managed = true
		target.Applied = true
		target.Paths = []string{"powercfg", "netsh dynamicport", "registry", "fsutil", "Defender exclusions"}
		target.BackupDir = state.BackupDir
		target.RestartRequired = state.RestartRequired
		target.Steps = []helperproto.PerformanceStep{
			{Name: "profile", Status: "ok", Detail: state.Profile},
			{Name: "power-plan", Status: "ok", Detail: state.PowerPlan},
		}
	}
	resp := helperproto.PerformanceStatusResponse{Targets: []helperproto.PerformanceTargetStatus{target}}
	if state != nil {
		applied := state.AppliedAt
		resp.AppliedAt = &applied
		resp.BackupDir = state.BackupDir
		resp.Managed = true
	}
	return resp, nil
}

// Apply writes every allowlisted Windows value and rolls back on failure.
func (m *WindowsPerformanceManager) Apply(ctx context.Context, req helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.Windows == nil {
		return helperproto.PerformanceApplyResponse{}, errors.New("helper: Windows performance values are required")
	}
	if err := ctx.Err(); err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	now := m.now().UTC()
	backupDir := filepath.Join(m.backupRoot(), now.Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	existing, err := m.readState()
	if err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	state := &windowsPerformanceState{
		Profile:   req.Profile,
		BackupDir: backupDir,
		AppliedAt: now,
		Ports:     map[string]windowsPortRange{},
		Registry:  map[string]windowsRegistryValue{},
	}
	if existing != nil {
		// Preserve the original pre-devstack baseline across profile switches.
		state.BackupDir = existing.BackupDir
		state.PowerPlan = existing.PowerPlan
		state.Ports = existing.Ports
		state.Registry = existing.Registry
		state.DisableLastAccess = existing.DisableLastAccess
		state.Disable8dot3 = existing.Disable8dot3
		state.ExclusionsAdded = append([]string(nil), existing.ExclusionsAdded...)
	} else if err := m.capture(ctx, state); err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	if err := m.writeState(state); err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	fail := func(cause error) (helperproto.PerformanceApplyResponse, error) {
		restoreErr := m.restore(ctx, state)
		if restoreErr != nil {
			return helperproto.PerformanceApplyResponse{}, errors.Join(cause, fmt.Errorf("helper: restore Windows tuning: %w", restoreErr))
		}
		return helperproto.PerformanceApplyResponse{}, cause
	}
	if err := m.applyPowerPlan(ctx, req.Windows.PowerPlan); err != nil {
		return fail(err)
	}
	if err := m.applyPorts(ctx, req.Windows.DynamicPortMin, req.Windows.DynamicPortMax); err != nil {
		return fail(err)
	}
	if err := m.applyRegistry(ctx, req.Windows); err != nil {
		return fail(err)
	}
	if err := m.applyFsutil(ctx, req.Windows.DisableLastAccess, req.Windows.Disable8dot3); err != nil {
		return fail(err)
	}
	defenderStep := helperproto.PerformanceStep{Name: "defender", Status: "ok", Detail: "no exclusions requested"}
	added, err := m.applyDefender(ctx, req.Windows)
	if err != nil {
		// Defender may be disabled or the module may be unavailable. Keep the
		// rest of the profile and surface a skipped step instead of hiding it.
		state.ExclusionsAdded = nil
		defenderStep = helperproto.PerformanceStep{Name: "defender", Status: "skipped", Detail: err.Error()}
	} else {
		state.ExclusionsAdded = append(state.ExclusionsAdded, added...)
		defenderStep.Detail = fmt.Sprintf("%d exclusion(s)", len(added))
	}
	state.RestartRequired = req.Windows.TCPTimedWaitDelaySeconds > 0
	if err := m.verify(ctx, req.Windows, state); err != nil {
		return fail(err)
	}
	if err := m.writeState(state); err != nil {
		return fail(err)
	}
	if err := writeWindowsPerformanceBackup(filepath.Join(backupDir, "windows.json"), state); err != nil {
		return fail(err)
	}
	resp := helperproto.PerformanceApplyResponse{
		Applied:   true,
		Profile:   req.Profile,
		BackupDir: backupDir,
		AppliedAt: now,
		Targets: []helperproto.PerformanceTargetStatus{{
			Target: "windows", Available: true, Managed: true, Applied: true,
			Paths:     []string{"powercfg", "netsh dynamicport", "registry", "fsutil", "Defender exclusions"},
			BackupDir: backupDir, RestartRequired: state.RestartRequired,
		}},
		Steps: []helperproto.PerformanceStep{
			{Name: "power-plan", Status: "ok", Detail: req.Windows.PowerPlan},
			{Name: "dynamic-ports", Status: "ok", Detail: fmt.Sprintf("%d-%d", req.Windows.DynamicPortMin, req.Windows.DynamicPortMax)},
			{Name: "registry", Status: "ok", Detail: "TcpTimedWaitDelay / throttling / responsiveness"},
			{Name: "ntfs", Status: "ok", Detail: fmt.Sprintf("last-access=%t 8dot3=%t", req.Windows.DisableLastAccess, req.Windows.Disable8dot3)},
			defenderStep,
		},
	}
	return resp, nil
}

// Reset restores the captured Windows state.
func (m *WindowsPerformanceManager) Reset(ctx context.Context) (helperproto.PerformanceResetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readState()
	if err != nil {
		return helperproto.PerformanceResetResponse{}, err
	}
	if state == nil {
		return helperproto.PerformanceResetResponse{Restored: true, Steps: []helperproto.PerformanceStep{{Name: "reset", Status: "skipped", Detail: "no managed Windows tuning"}}}, nil
	}
	if err := m.restore(ctx, state); err != nil {
		return helperproto.PerformanceResetResponse{}, err
	}
	if err := os.Remove(m.statePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return helperproto.PerformanceResetResponse{}, err
	}
	return helperproto.PerformanceResetResponse{
		Restored:  true,
		BackupDir: state.BackupDir,
		Targets: []helperproto.PerformanceTargetStatus{{
			Target: "windows", Available: true, Managed: false, Applied: false, BackupDir: state.BackupDir,
		}},
		Steps: []helperproto.PerformanceStep{
			{Name: "power-plan", Status: "ok", Detail: "restored " + state.PowerPlan},
			{Name: "ports", Status: "ok", Detail: "restored dynamic port ranges"},
			{Name: "registry", Status: "ok", Detail: "restored captured values"},
			{Name: "defender", Status: "ok", Detail: fmt.Sprintf("removed %d exclusion(s)", len(state.ExclusionsAdded))},
		},
	}, nil
}

func (m *WindowsPerformanceManager) capture(ctx context.Context, state *windowsPerformanceState) error {
	out, err := m.run(ctx, "powercfg", "/getactivescheme")
	if err != nil {
		return fmt.Errorf("helper: read power plan: %w", err)
	}
	state.PowerPlan = powerPlanGUID(out)
	for _, protocol := range []string{"ipv4 tcp", "ipv4 udp", "ipv6 tcp", "ipv6 udp"} {
		if rangeValue, err := m.readPorts(ctx, protocol); err == nil {
			state.Ports[protocol] = rangeValue
		}
	}
	for _, item := range windowsRegistryTargets(nil) {
		value, err := m.readRegistry(ctx, item.Path, item.Name)
		if err != nil {
			return err
		}
		state.Registry[item.Path+"|"+item.Name] = value
	}
	if value, err := m.readFsutil(ctx, "disablelastaccess"); err == nil {
		state.DisableLastAccess = value
	}
	if value, err := m.readFsutil(ctx, "disable8dot3"); err == nil {
		state.Disable8dot3 = value
	}
	return nil
}

func (m *WindowsPerformanceManager) applyPowerPlan(ctx context.Context, plan string) error {
	guid := powerPlanBalancedGUID
	if strings.EqualFold(plan, "high_performance") {
		guid = powerPlanHighGUID
	}
	if _, err := m.run(ctx, "powercfg", "/setactive", guid); err != nil {
		return fmt.Errorf("helper: set power plan %s: %w", plan, err)
	}
	return nil
}

func (m *WindowsPerformanceManager) applyPorts(ctx context.Context, min, max int) error {
	if min < 1 || max > 65535 || max <= min {
		return fmt.Errorf("helper: invalid dynamic port range %d-%d", min, max)
	}
	count := max - min + 1
	for _, transport := range []string{"tcp", "udp"} {
		if _, err := m.run(ctx, "netsh", "int", "ipv4", "set", "dynamicport", transport, fmt.Sprintf("start=%d", min), fmt.Sprintf("num=%d", count)); err != nil {
			return fmt.Errorf("helper: set ipv4 %s dynamic ports: %w", transport, err)
		}
	}
	// IPv6 is common but optional on some hosts. Keep it best-effort.
	for _, transport := range []string{"tcp", "udp"} {
		_, _ = m.run(ctx, "netsh", "int", "ipv6", "set", "dynamicport", transport, fmt.Sprintf("start=%d", min), fmt.Sprintf("num=%d", count))
	}
	return nil
}

func (m *WindowsPerformanceManager) applyRegistry(ctx context.Context, values *helperproto.PerformanceWindowsValues) error {
	targets := windowsRegistryTargets(values)
	for _, item := range targets {
		if item.Delete {
			_, _ = m.run(ctx, "reg", "delete", item.Path, "/v", item.Name, "/f")
			continue
		}
		if _, err := m.run(ctx, "reg", "add", item.Path, "/v", item.Name, "/t", "REG_DWORD", "/d", strconv.FormatUint(uint64(item.Value), 10), "/f"); err != nil {
			return fmt.Errorf("helper: set registry %s\\%s: %w", item.Path, item.Name, err)
		}
	}
	return nil
}

func (m *WindowsPerformanceManager) applyFsutil(ctx context.Context, disableLastAccess, disable8dot3 bool) error {
	if _, err := m.run(ctx, "fsutil", "behavior", "set", "disablelastaccess", boolFlag(disableLastAccess)); err != nil {
		return fmt.Errorf("helper: set disablelastaccess: %w", err)
	}
	if _, err := m.run(ctx, "fsutil", "behavior", "set", "disable8dot3", boolFlag(disable8dot3)); err != nil {
		return fmt.Errorf("helper: set disable8dot3: %w", err)
	}
	return nil
}

func (m *WindowsPerformanceManager) applyDefender(ctx context.Context, values *helperproto.PerformanceWindowsValues) ([]string, error) {
	if !values.DefenderExclusions {
		return nil, nil
	}
	paths := defenderPaths(m.systemDir, values.VHDXPath)
	current := m.defenderExclusions(ctx)
	existing := make(map[string]bool, len(current))
	for _, path := range current {
		existing[strings.ToLower(path)] = true
	}
	var added []string
	for _, path := range paths {
		if path == "" || existing[strings.ToLower(path)] {
			continue
		}
		script := "Add-MpPreference -ExclusionPath '" + strings.ReplaceAll(path, "'", "''") + "'"
		if _, err := m.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
			return added, fmt.Errorf("helper: add Defender exclusion %s: %w", path, err)
		}
		added = append(added, path)
	}
	if len(added) == 0 {
		return nil, nil
	}
	return added, nil
}

func defenderPaths(systemDir, vhdx string) []string {
	paths := []string{}
	system := filepath.Clean(systemDir)
	home := filepath.Dir(system)
	if home != "" {
		paths = append(paths, home, filepath.Join(home, "projects"))
	}
	if strings.TrimSpace(vhdx) != "" {
		paths = append(paths, vhdx)
	}
	return paths
}

func (m *WindowsPerformanceManager) verify(ctx context.Context, values *helperproto.PerformanceWindowsValues, state *windowsPerformanceState) error {
	out, err := m.run(ctx, "powercfg", "/getactivescheme")
	if err != nil {
		return fmt.Errorf("helper: verify power plan: %w", err)
	}
	wantPlan := powerPlanBalancedGUID
	if strings.EqualFold(values.PowerPlan, "high_performance") {
		wantPlan = powerPlanHighGUID
	}
	if got := powerPlanGUID(out); !strings.EqualFold(got, wantPlan) {
		return fmt.Errorf("helper: power plan = %s, want %s", got, wantPlan)
	}
	for _, protocol := range []string{"ipv4 tcp", "ipv4 udp"} {
		got, err := m.readPorts(ctx, protocol)
		if err != nil {
			return err
		}
		if got.Start != values.DynamicPortMin || got.Count != values.DynamicPortMax-values.DynamicPortMin+1 {
			return fmt.Errorf("helper: %s dynamic ports = %+v, want %d+%d", protocol, got, values.DynamicPortMin, values.DynamicPortMax-values.DynamicPortMin+1)
		}
	}
	for _, item := range windowsRegistryTargets(values) {
		if item.Delete {
			continue
		}
		got, err := m.readRegistry(ctx, item.Path, item.Name)
		if err != nil {
			return err
		}
		if !got.Exists || got.Value != item.Value {
			return fmt.Errorf("helper: registry %s\\%s = %+v, want %d", item.Path, item.Name, got, item.Value)
		}
	}
	if values.DisableLastAccess {
		if got, err := m.readFsutil(ctx, "disablelastaccess"); err == nil && got == 0 {
			return errors.New("helper: disablelastaccess was not applied")
		}
	}
	if values.Disable8dot3 {
		if got, err := m.readFsutil(ctx, "disable8dot3"); err == nil && got == 0 {
			return errors.New("helper: disable8dot3 was not applied")
		}
	}
	return nil
}

func (m *WindowsPerformanceManager) restore(ctx context.Context, state *windowsPerformanceState) error {
	if state.PowerPlan != "" {
		_, _ = m.run(ctx, "powercfg", "/setactive", state.PowerPlan)
	}
	for protocol, value := range state.Ports {
		parts := strings.SplitN(protocol, " ", 2)
		if len(parts) != 2 {
			continue
		}
		_, _ = m.run(ctx, "netsh", "int", parts[0], "set", "dynamicport", parts[1], fmt.Sprintf("start=%d", value.Start), fmt.Sprintf("num=%d", value.Count))
	}
	for _, value := range state.Registry {
		if value.Exists {
			_, _ = m.run(ctx, "reg", "add", value.Path, "/v", value.Name, "/t", "REG_DWORD", "/d", strconv.FormatUint(uint64(value.Value), 10), "/f")
		} else {
			_, _ = m.run(ctx, "reg", "delete", value.Path, "/v", value.Name, "/f")
		}
	}
	_, _ = m.run(ctx, "fsutil", "behavior", "set", "disablelastaccess", strconv.Itoa(state.DisableLastAccess))
	_, _ = m.run(ctx, "fsutil", "behavior", "set", "disable8dot3", strconv.Itoa(state.Disable8dot3))
	for _, path := range state.ExclusionsAdded {
		script := "Remove-MpPreference -ExclusionPath '" + strings.ReplaceAll(path, "'", "''") + "'"
		_, _ = m.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	return nil
}

func (m *WindowsPerformanceManager) readPorts(ctx context.Context, protocol string) (windowsPortRange, error) {
	parts := strings.SplitN(protocol, " ", 2)
	out, err := m.run(ctx, "netsh", "int", parts[0], "show", "dynamicport", parts[1])
	if err != nil {
		return windowsPortRange{}, err
	}
	start := regexp.MustCompile(`(?m)Start Port\s*:\s*(\d+)`).FindStringSubmatch(string(out))
	count := regexp.MustCompile(`(?m)Number of Ports\s*:\s*(\d+)`).FindStringSubmatch(string(out))
	if len(start) != 2 || len(count) != 2 {
		return windowsPortRange{}, fmt.Errorf("helper: cannot parse %s dynamic ports", protocol)
	}
	s, _ := strconv.Atoi(start[1])
	c, _ := strconv.Atoi(count[1])
	return windowsPortRange{Start: s, Count: c}, nil
}

func (m *WindowsPerformanceManager) readRegistry(ctx context.Context, path, name string) (windowsRegistryValue, error) {
	out, err := m.run(ctx, "reg", "query", path, "/v", name)
	if err != nil {
		return windowsRegistryValue{Path: path, Name: name}, nil
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return windowsRegistryValue{Path: path, Name: name}, nil
	}
	raw := fields[len(fields)-1]
	raw = strings.TrimPrefix(strings.ToLower(raw), "0x")
	value, err := strconv.ParseUint(raw, 16, 32)
	if err != nil {
		return windowsRegistryValue{Path: path, Name: name}, nil
	}
	return windowsRegistryValue{Path: path, Name: name, Exists: true, Value: uint32(value)}, nil
}

func (m *WindowsPerformanceManager) readFsutil(ctx context.Context, name string) (int, error) {
	out, err := m.run(ctx, "fsutil", "behavior", "query", name)
	if err != nil {
		return 0, err
	}
	matches := regexp.MustCompile(`=\s*(\d+)`).FindStringSubmatch(string(out))
	if len(matches) != 2 {
		return 0, fmt.Errorf("helper: cannot parse fsutil %s", name)
	}
	return strconv.Atoi(matches[1])
}

func (m *WindowsPerformanceManager) defenderExclusions(ctx context.Context) []string {
	out, err := m.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Get-MpPreference | Select-Object -ExpandProperty ExclusionPath")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

type registryTarget struct {
	Path   string
	Name   string
	Value  uint32
	Delete bool
}

func windowsRegistryTargets(values *helperproto.PerformanceWindowsValues) []registryTarget {
	tcp := `HKLM\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`
	multimedia := `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Multimedia\SystemProfile`
	if values == nil {
		return []registryTarget{
			{Path: tcp, Name: "TcpTimedWaitDelay"},
			{Path: multimedia, Name: "NetworkThrottlingIndex"},
			{Path: multimedia, Name: "SystemResponsiveness"},
		}
	}
	return []registryTarget{
		{Path: tcp, Name: "TcpTimedWaitDelay", Value: uint32(values.TCPTimedWaitDelaySeconds), Delete: values.TCPTimedWaitDelaySeconds == 0},
		{Path: multimedia, Name: "NetworkThrottlingIndex", Value: values.NetworkThrottlingIndex},
		{Path: multimedia, Name: "SystemResponsiveness", Value: values.SystemResponsiveness},
	}
}

func powerPlanGUID(out []byte) string {
	match := regexp.MustCompile(`([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`).FindStringSubmatch(string(out))
	if len(match) == 2 {
		return strings.ToLower(match[1])
	}
	return ""
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func (m *WindowsPerformanceManager) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := m.runner.Run(ctx, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (m *WindowsPerformanceManager) readState() (*windowsPerformanceState, error) {
	data, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state windowsPerformanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (m *WindowsPerformanceManager) writeState(state *windowsPerformanceState) error {
	return writeWindowsPerformanceBackup(m.statePath(), state)
}

func writeWindowsPerformanceBackup(path string, state *windowsPerformanceState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".devstack-perf-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
