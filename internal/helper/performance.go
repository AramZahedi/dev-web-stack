//go:build !windows

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/performance"
)

// Fixed host tuning targets. The helper derives these paths itself; no request
// can supply a path.
const (
	performanceSysctlTarget = "/etc/sysctl.d/99-devstack-performance.conf"
	performanceLimitsTarget = "/etc/security/limits.d/99-devstack-performance.conf"
)

const performanceMarkerName = "current.json"

// PerformanceManagerConfig configures the privileged host tuning manager.
// DestinationRoot is a test seam; an empty value or "/" means the real fixed
// paths. BackupRoot is the devstack root that owns backups/performance.
type PerformanceManagerConfig struct {
	BackupRoot      string
	DestinationRoot string
	Runner          CommandRunner
	Now             func() time.Time
	// ReadSysctl reads an applied sysctl value after `sysctl --system`. Tests
	// inject a fake; production reads /proc/sys.
	ReadSysctl func(key string) (string, error)
}

// PerformanceManager applies and resets the fixed sysctl/limits files. Every
// change is backed up under <root>/backups/performance and verified before the
// manager reports success.
type PerformanceManager struct {
	backupRoot string
	destRoot   string
	runner     CommandRunner
	now        func() time.Time
	readSysctl func(string) (string, error)
	verifyProc bool

	mu sync.Mutex
}

// PerformanceApplyError carries the step response when apply fails after it
// started writing host files.
type PerformanceApplyError struct {
	Err      error
	Response helperproto.PerformanceApplyResponse
}

// Error implements error.
func (e *PerformanceApplyError) Error() string {
	if e == nil || e.Err == nil {
		return "helper: performance apply failed"
	}
	message := "helper: performance apply failed: " + e.Err.Error()
	if e.Response.BackupDir != "" {
		message += " (backup: " + e.Response.BackupDir + ")"
	}
	return message
}

// Unwrap exposes the underlying apply failure.
func (e *PerformanceApplyError) Unwrap() error { return e.Err }

type performanceMarker struct {
	BackupDir     string                 `json:"backup_dir"`
	CreatedAt     time.Time              `json:"created_at"`
	AppliedAt     time.Time              `json:"applied_at"`
	Profile       string                 `json:"profile"`
	Host          performance.HostValues `json:"host"`
	SysctlExisted bool                   `json:"sysctl_existed"`
	LimitsExisted bool                   `json:"limits_existed"`
	PriorValues   map[string]string      `json:"prior_sysctl_values,omitempty"`
}

type performancePrior struct {
	SysctlExisted bool
	SysctlContent []byte
	SysctlMode    os.FileMode
	LimitsExisted bool
	LimitsContent []byte
	LimitsMode    os.FileMode
}

// NewPerformanceManager returns a fixed-path host tuning manager.
func NewPerformanceManager(cfg PerformanceManagerConfig) *PerformanceManager {
	dest := cfg.DestinationRoot
	if dest == "" {
		dest = "/"
	}
	dest = filepath.Clean(dest)
	runner := cfg.Runner
	if runner == nil {
		if dest == string(filepath.Separator) {
			runner = execRunner{}
		} else {
			runner = noopRunner{}
		}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	readSysctl := cfg.ReadSysctl
	verifyProc := readSysctl != nil || dest == string(filepath.Separator)
	if readSysctl == nil {
		readSysctl = readProcSysctl
	}
	return &PerformanceManager{
		backupRoot: cfg.BackupRoot,
		destRoot:   dest,
		runner:     runner,
		now:        now,
		readSysctl: readSysctl,
		verifyProc: verifyProc,
	}
}

func (m *PerformanceManager) destination(target string) string {
	if m.destRoot == "" || m.destRoot == string(filepath.Separator) {
		return target
	}
	return filepath.Join(m.destRoot, strings.TrimPrefix(target, "/"))
}

func (m *PerformanceManager) markerPath() string {
	return filepath.Join(m.backupRoot, "backups", "performance", performanceMarkerName)
}

func (m *PerformanceManager) newBackupDir(now time.Time) (string, error) {
	base := filepath.Join(m.backupRoot, "backups", "performance", now.UTC().Format("20060102T150405.000000000Z"))
	dir := base
	for suffix := 1; ; suffix++ {
		_, err := os.Stat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return dir, nil
		}
		if err != nil {
			return "", err
		}
		dir = fmt.Sprintf("%s-%d", base, suffix)
	}
}

// Apply writes the fixed sysctl/limits files, applies them with
// `sysctl --system`, verifies the values, and rolls back on any failure.
func (m *PerformanceManager) Apply(ctx context.Context, req helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, err := performance.ParseProfile(req.Profile)
	if err != nil {
		return helperproto.PerformanceApplyResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
	}
	host := performanceHostFromProto(req.Host)
	if err := validateHostTuningSafety(host); err != nil {
		return helperproto.PerformanceApplyResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
	}
	sysctlContent, err := performance.RenderHostSysctl(profile, host)
	if err != nil {
		return helperproto.PerformanceApplyResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
	}
	limitsContent, err := performance.RenderHostLimits(profile, host)
	if err != nil {
		return helperproto.PerformanceApplyResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
	}
	if m.backupRoot == "" || !filepath.IsAbs(m.backupRoot) {
		return helperproto.PerformanceApplyResponse{}, fmt.Errorf("%w: performance backup root must be an absolute path", helperproto.ErrInvalidRequest)
	}

	now := m.now().UTC()
	marker, err := m.loadMarker()
	if err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	prior, err := m.readPrior()
	if err != nil {
		return helperproto.PerformanceApplyResponse{}, err
	}
	createdMarker := false
	if marker == nil {
		backupDir, err := m.newBackupDir(now)
		if err != nil {
			return helperproto.PerformanceApplyResponse{}, err
		}
		marker = &performanceMarker{
			BackupDir:     backupDir,
			CreatedAt:     now,
			AppliedAt:     now,
			Profile:       string(profile),
			Host:          host,
			SysctlExisted: prior.SysctlExisted,
			LimitsExisted: prior.LimitsExisted,
		}
		marker.PriorValues = m.captureSysctlValues()
		if err := m.writeBackup(marker.BackupDir, prior); err != nil {
			return helperproto.PerformanceApplyResponse{}, err
		}
		if err := m.writeMarker(marker); err != nil {
			return helperproto.PerformanceApplyResponse{}, err
		}
		createdMarker = true
	}

	response := helperproto.PerformanceApplyResponse{
		Profile:   string(profile),
		BackupDir: marker.BackupDir,
		AppliedAt: now,
		Steps:     []helperproto.PerformanceStep{},
	}
	step := func(name, status, detail string) {
		response.Steps = append(response.Steps, helperproto.PerformanceStep{Name: name, Status: status, Detail: detail})
	}

	sysctlPath := m.destination(performanceSysctlTarget)
	limitsPath := m.destination(performanceLimitsTarget)
	if err := writePerformanceFile(sysctlPath, sysctlContent, 0o644); err != nil {
		step("write-sysctl", "failed", err.Error())
		rollbackErr := m.rollback(marker, prior)
		if createdMarker && rollbackErr == nil {
			_ = os.Remove(m.markerPath())
		}
		return response, &PerformanceApplyError{Err: errors.Join(err, rollbackErr), Response: response}
	}
	step("write-sysctl", "ok", performanceSysctlTarget)
	if err := writePerformanceFile(limitsPath, limitsContent, 0o644); err != nil {
		step("write-limits", "failed", err.Error())
		rollbackErr := m.rollback(marker, prior)
		if createdMarker && rollbackErr == nil {
			_ = os.Remove(m.markerPath())
		}
		return response, &PerformanceApplyError{Err: errors.Join(err, rollbackErr), Response: response}
	}
	step("write-limits", "ok", performanceLimitsTarget)

	if _, err := m.runner.Run(ctx, "sysctl", "--system"); err != nil {
		step("apply-sysctl", "failed", err.Error())
		rollbackErr := m.rollback(marker, prior)
		if createdMarker && rollbackErr == nil {
			_ = os.Remove(m.markerPath())
		}
		return response, &PerformanceApplyError{Err: errors.Join(err, rollbackErr), Response: response}
	}
	step("apply-sysctl", "ok", "sysctl --system")

	verified, err := m.verify(sysctlPath, limitsPath, sysctlContent, limitsContent, host)
	if err != nil {
		step("verify", "failed", err.Error())
		rollbackErr := m.rollback(marker, prior)
		if createdMarker && rollbackErr == nil {
			_ = os.Remove(m.markerPath())
		}
		return response, &PerformanceApplyError{Err: errors.Join(err, rollbackErr), Response: response}
	}
	response.Verified = verified
	step("verify", "ok", strings.Join(verified, ", "))

	marker.AppliedAt = now
	marker.Profile = string(profile)
	marker.Host = host
	if err := m.writeMarker(marker); err != nil {
		rollbackErr := m.rollback(marker, prior)
		return response, &PerformanceApplyError{Err: errors.Join(err, rollbackErr), Response: response}
	}
	step("record", "ok", marker.BackupDir)

	response.Applied = true
	response.Files = []helperproto.PerformanceFileResult{
		{Path: performanceSysctlTarget, Bytes: len(sysctlContent), BackedUp: marker.SysctlExisted},
		{Path: performanceLimitsTarget, Bytes: len(limitsContent), BackedUp: marker.LimitsExisted},
	}
	return response, nil
}

// Reset restores the pre-devstack files recorded by the current marker, or
// removes only devstack-owned files when no marker exists.
func (m *PerformanceManager) Reset(ctx context.Context, _ helperproto.PerformanceResetRequest) (helperproto.PerformanceResetResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := helperproto.PerformanceResetResponse{Steps: []helperproto.PerformanceStep{}}
	step := func(name, status, detail string) {
		response.Steps = append(response.Steps, helperproto.PerformanceStep{Name: name, Status: status, Detail: detail})
	}
	marker, err := m.loadMarker()
	if err != nil {
		return response, err
	}
	sysctlPath := m.destination(performanceSysctlTarget)
	limitsPath := m.destination(performanceLimitsTarget)
	if marker == nil {
		removed := 0
		skipped := 0
		for _, path := range []string{sysctlPath, limitsPath} {
			data, readErr := os.ReadFile(path)
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return response, readErr
			}
			if !bytes.HasPrefix(data, []byte("# Code generated by devstack performance")) {
				// No marker means we cannot prove this is devstack-owned. Never
				// delete a pre-existing administrator file at a fixed path.
				skipped++
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return response, err
			}
			removed++
		}
		if removed > 0 {
			if _, err := m.runner.Run(ctx, "sysctl", "--system"); err != nil {
				step("apply-sysctl", "failed", err.Error())
				return response, err
			}
		}
		step("remove", "ok", fmt.Sprintf("no marker; removed %d devstack-owned file(s), left %d unowned file(s)", removed, skipped))
		return response, nil
	}

	prior, err := m.readPriorFromBackup(marker)
	if err != nil {
		return response, err
	}
	if err := m.resetRestore(prior); err != nil {
		step("restore", "failed", err.Error())
		return response, err
	}
	step("restore", "ok", "pre-devstack files restored")
	if _, err := m.runner.Run(ctx, "sysctl", "--system"); err != nil {
		step("apply-sysctl", "failed", err.Error())
		return response, err
	}
	step("apply-sysctl", "ok", "sysctl --system")
	if err := m.restoreSysctlValues(marker.PriorValues); err != nil {
		step("restore-values", "failed", err.Error())
		return response, err
	}
	if len(marker.PriorValues) > 0 {
		step("restore-values", "ok", "pre-devstack sysctl values restored")
	}
	if err := os.Remove(m.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return response, err
	}
	step("record", "ok", "marker removed")

	response.Restored = true
	response.BackupDir = marker.BackupDir
	for _, item := range []struct {
		path   string
		exists bool
	}{
		{performanceSysctlTarget, prior.SysctlExisted},
		{performanceLimitsTarget, prior.LimitsExisted},
	} {
		response.Files = append(response.Files, helperproto.PerformanceFileResult{Path: item.path, BackedUp: item.exists})
	}
	return response, nil
}

// Status reports the fixed file state and contents for the preview diff.
func (m *PerformanceManager) Status(_ context.Context) (helperproto.PerformanceStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	marker, err := m.loadMarker()
	if err != nil {
		return helperproto.PerformanceStatusResponse{}, err
	}
	status := helperproto.PerformanceStatusResponse{
		SysctlPath: performanceSysctlTarget,
		LimitsPath: performanceLimitsTarget,
	}
	if marker != nil {
		status.Managed = true
		status.Profile = marker.Profile
		status.BackupDir = marker.BackupDir
		appliedAt := marker.AppliedAt
		status.AppliedAt = &appliedAt
		host := hostValuesToProto(marker.Host)
		status.Host = &host
	}
	if data, err := os.ReadFile(m.destination(performanceSysctlTarget)); err == nil {
		status.SysctlPresent = true
		status.SysctlContent = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return helperproto.PerformanceStatusResponse{}, err
	}
	if data, err := os.ReadFile(m.destination(performanceLimitsTarget)); err == nil {
		status.LimitsPresent = true
		status.LimitsContent = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return helperproto.PerformanceStatusResponse{}, err
	}
	return status, nil
}

func (m *PerformanceManager) loadMarker() (*performanceMarker, error) {
	data, err := os.ReadFile(m.markerPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var marker performanceMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, fmt.Errorf("performance marker is invalid: %w", err)
	}
	if marker.BackupDir == "" || !filepath.IsAbs(marker.BackupDir) {
		return nil, errors.New("performance marker has no absolute backup directory")
	}
	if err := m.validateBackupDir(marker.BackupDir); err != nil {
		return nil, err
	}
	if marker.Profile != "" {
		if _, err := performance.ParseProfile(marker.Profile); err != nil {
			return nil, fmt.Errorf("performance marker profile is invalid: %w", err)
		}
	}
	if err := marker.Host.Validate(); err != nil {
		return nil, fmt.Errorf("performance marker host values are invalid: %w", err)
	}
	if err := m.validateMarkerBackup(&marker); err != nil {
		return nil, err
	}
	return &marker, nil
}

// validateMarkerBackup proves the referenced snapshot still exists before a
// later apply overwrites the live files or a reset restores from it.
func (m *PerformanceManager) validateMarkerBackup(marker *performanceMarker) error {
	info, err := os.Stat(marker.BackupDir)
	if err != nil {
		return fmt.Errorf("performance backup directory %s is unavailable: %w", marker.BackupDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("performance backup path %s is not a directory", marker.BackupDir)
	}
	for _, item := range []struct {
		exists bool
		rel    string
	}{
		{marker.SysctlExisted, filepath.Join("etc", "sysctl.d", filepath.Base(performanceSysctlTarget))},
		{marker.LimitsExisted, filepath.Join("etc", "security", "limits.d", filepath.Base(performanceLimitsTarget))},
	} {
		if !item.exists {
			continue
		}
		if _, err := os.Stat(filepath.Join(marker.BackupDir, item.rel)); err != nil {
			return fmt.Errorf("performance backup file %s is missing: %w", item.rel, err)
		}
	}
	return nil
}

// validateBackupDir keeps a tampered marker from directing the privileged
// helper at arbitrary files outside backups/performance.
func (m *PerformanceManager) validateBackupDir(dir string) error {
	root := filepath.Join(m.backupRoot, "backups", "performance")
	cleanRoot := filepath.Clean(root)
	cleanDir := filepath.Clean(dir)
	rel, err := filepath.Rel(cleanRoot, cleanDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("performance marker backup directory %q is outside %s", dir, cleanRoot)
	}
	return nil
}

func (m *PerformanceManager) writeMarker(marker *performanceMarker) error {
	if err := os.MkdirAll(filepath.Dir(m.markerPath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return writePerformanceFile(m.markerPath(), data, 0o644)
}

func (m *PerformanceManager) readPrior() (performancePrior, error) {
	prior := performancePrior{}
	data, mode, err := readRegularFile(m.destination(performanceSysctlTarget))
	if err != nil {
		return performancePrior{}, err
	}
	if data != nil {
		prior.SysctlExisted = true
		prior.SysctlContent = data
		prior.SysctlMode = mode
	}
	data, mode, err = readRegularFile(m.destination(performanceLimitsTarget))
	if err != nil {
		return performancePrior{}, err
	}
	if data != nil {
		prior.LimitsExisted = true
		prior.LimitsContent = data
		prior.LimitsMode = mode
	}
	return prior, nil
}

func (m *PerformanceManager) readPriorFromBackup(marker *performanceMarker) (performancePrior, error) {
	prior := performancePrior{}
	if marker.SysctlExisted {
		data, mode, err := readRegularFile(filepath.Join(marker.BackupDir, "etc", "sysctl.d", filepath.Base(performanceSysctlTarget)))
		if err != nil {
			return performancePrior{}, err
		}
		if data == nil {
			return performancePrior{}, fmt.Errorf("backup for %s is missing from %s", performanceSysctlTarget, marker.BackupDir)
		}
		prior.SysctlExisted = true
		prior.SysctlContent = data
		prior.SysctlMode = mode
	}
	if marker.LimitsExisted {
		data, mode, err := readRegularFile(filepath.Join(marker.BackupDir, "etc", "security", "limits.d", filepath.Base(performanceLimitsTarget)))
		if err != nil {
			return performancePrior{}, err
		}
		if data == nil {
			return performancePrior{}, fmt.Errorf("backup for %s is missing from %s", performanceLimitsTarget, marker.BackupDir)
		}
		prior.LimitsExisted = true
		prior.LimitsContent = data
		prior.LimitsMode = mode
	}
	return prior, nil
}

func (m *PerformanceManager) writeBackup(backupDir string, prior performancePrior) error {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	if prior.SysctlExisted {
		path := filepath.Join(backupDir, "etc", "sysctl.d", filepath.Base(performanceSysctlTarget))
		if err := writePerformanceFile(path, prior.SysctlContent, priorMode(prior.SysctlMode)); err != nil {
			return err
		}
	}
	if prior.LimitsExisted {
		path := filepath.Join(backupDir, "etc", "security", "limits.d", filepath.Base(performanceLimitsTarget))
		if err := writePerformanceFile(path, prior.LimitsContent, priorMode(prior.LimitsMode)); err != nil {
			return err
		}
	}
	return nil
}

func (m *PerformanceManager) rollback(marker *performanceMarker, prior performancePrior) error {
	if err := m.restore(prior); err != nil {
		return err
	}
	if _, err := m.runner.Run(context.Background(), "sysctl", "--system"); err != nil {
		return fmt.Errorf("restore host files: %w", err)
	}
	if marker != nil {
		if err := m.restoreSysctlValues(marker.PriorValues); err != nil {
			return fmt.Errorf("restore sysctl values: %w", err)
		}
	}
	return nil
}

func (m *PerformanceManager) resetRestore(prior performancePrior) error {
	if prior.SysctlExisted {
		if err := validatePriorSysctl(prior.SysctlContent); err != nil {
			return fmt.Errorf("refusing to restore prior sysctl file: %w", err)
		}
	}
	if prior.LimitsExisted {
		if err := validatePriorLimits(prior.LimitsContent); err != nil {
			return fmt.Errorf("refusing to restore prior limits file: %w", err)
		}
	}
	return m.restore(prior)
}

func (m *PerformanceManager) restore(prior performancePrior) error {
	if prior.SysctlExisted {
		if err := writePerformanceFile(m.destination(performanceSysctlTarget), prior.SysctlContent, priorMode(prior.SysctlMode)); err != nil {
			return err
		}
	} else if err := os.Remove(m.destination(performanceSysctlTarget)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if prior.LimitsExisted {
		if err := writePerformanceFile(m.destination(performanceLimitsTarget), prior.LimitsContent, priorMode(prior.LimitsMode)); err != nil {
			return err
		}
	} else if err := os.Remove(m.destination(performanceLimitsTarget)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *PerformanceManager) verify(sysctlPath, limitsPath string, sysctlContent, limitsContent []byte, host performance.HostValues) ([]string, error) {
	written, _, err := readRegularFile(sysctlPath)
	if err != nil {
		return nil, err
	}
	if string(written) != string(sysctlContent) {
		return nil, errors.New("written sysctl file does not match the generated content")
	}
	written, _, err = readRegularFile(limitsPath)
	if err != nil {
		return nil, err
	}
	if string(written) != string(limitsContent) {
		return nil, errors.New("written limits file does not match the generated content")
	}
	verified := []string{"files"}
	if !m.verifyProc {
		return verified, nil
	}
	expected := map[string]string{
		"fs.file-max":                  strconv.FormatUint(host.FileMax, 10),
		"net.core.somaxconn":           strconv.Itoa(host.Somaxconn),
		"net.core.netdev_max_backlog":  strconv.Itoa(host.NetdevMaxBacklog),
		"net.ipv4.ip_local_port_range": fmt.Sprintf("%d %d", host.IPLocalPortRange.Min, host.IPLocalPortRange.Max),
	}
	for key, want := range expected {
		got, err := m.readSysctl(key)
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", key, err)
		}
		if normalizeSysctlValue(got) != normalizeSysctlValue(want) {
			return nil, fmt.Errorf("verify %s: got %q, want %q", key, got, want)
		}
		verified = append(verified, key)
	}
	return verified, nil
}

func normalizeSysctlValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func readProcSysctl(key string) (string, error) {
	path := filepath.Join("/proc/sys", strings.ReplaceAll(key, ".", "/"))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func performanceHostFromProto(host helperproto.PerformanceHostValues) performance.HostValues {
	return performance.HostValues{
		FileMax:          host.FileMax,
		Somaxconn:        host.Somaxconn,
		NetdevMaxBacklog: host.NetdevMaxBacklog,
		IPLocalPortRange: performance.PortRange{Min: host.IPLocalPortMin, Max: host.IPLocalPortMax},
		NoFileSoft:       host.NoFileSoft,
		NoFileHard:       host.NoFileHard,
		User:             host.User,
		Group:            host.Group,
	}
}

func hostValuesToProto(host performance.HostValues) helperproto.PerformanceHostValues {
	return helperproto.PerformanceHostValues{
		FileMax:          host.FileMax,
		Somaxconn:        host.Somaxconn,
		NetdevMaxBacklog: host.NetdevMaxBacklog,
		IPLocalPortMin:   host.IPLocalPortRange.Min,
		IPLocalPortMax:   host.IPLocalPortRange.Max,
		NoFileSoft:       host.NoFileSoft,
		NoFileHard:       host.NoFileHard,
		User:             host.User,
		Group:            host.Group,
	}
}

func priorMode(mode os.FileMode) os.FileMode {
	if mode.Perm() == 0 {
		return 0o644
	}
	return mode.Perm()
}

func readRegularFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

type noopRunner struct{}

func (noopRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, nil }

// writePerformanceFile creates the fixed target's parent directory before an
// atomic write. Only helper-derived fixed paths reach this function.
func writePerformanceFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomicFile(path, data, mode)
}

var performanceSysctlKeys = []string{
	"fs.file-max",
	"net.core.somaxconn",
	"net.core.netdev_max_backlog",
	"net.ipv4.ip_local_port_range",
}

// captureSysctlValues records the kernel values that existed before the first
// apply. Values that cannot be read or validated are skipped; the file backup
// remains the primary restoration source.
func (m *PerformanceManager) captureSysctlValues() map[string]string {
	if !m.verifyProc {
		return nil
	}
	values := make(map[string]string, len(performanceSysctlKeys))
	for _, key := range performanceSysctlKeys {
		value, err := m.readSysctl(key)
		if err != nil {
			continue
		}
		value = normalizeSysctlValue(value)
		if err := validatePriorSysctl([]byte(key + " = " + value)); err != nil {
			continue
		}
		values[key] = value
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// restoreSysctlValues writes the saved kernel values back after the file
// restore and `sysctl --system`. Keys and values are allowlisted and validated;
// a tampered marker cannot turn this into an arbitrary sysctl command.
func (m *PerformanceManager) restoreSysctlValues(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := normalizeSysctlValue(values[key])
		if err := validatePriorSysctl([]byte(key + " = " + value)); err != nil {
			return fmt.Errorf("saved %s value %q is unsafe: %w", key, value, err)
		}
		if _, err := m.runner.Run(context.Background(), "sysctl", "-w", key+"="+value); err != nil {
			return err
		}
	}
	return nil
}

// Host tuning safety ceilings. The daemon normally computes much smaller
// values; these bounds stop a direct helper caller from requesting an absurd
// kernel limit while still allowing an existing larger host value to be
// preserved.
const (
	maxPerformanceFileMax = uint64(1) << 32
	maxPerformanceNoFile  = uint64(1) << 24
	maxPerformanceBacklog = 1 << 20
)

func validateHostTuningSafety(host performance.HostValues) error {
	if host.FileMax > maxPerformanceFileMax {
		return fmt.Errorf("fs.file-max %d exceeds the %d safety ceiling", host.FileMax, maxPerformanceFileMax)
	}
	if host.NoFileSoft > maxPerformanceNoFile || host.NoFileHard > maxPerformanceNoFile {
		return fmt.Errorf("nofile %d/%d exceeds the %d safety ceiling", host.NoFileSoft, host.NoFileHard, maxPerformanceNoFile)
	}
	if host.Somaxconn > maxPerformanceBacklog || host.NetdevMaxBacklog > maxPerformanceBacklog {
		return fmt.Errorf("backlog %d/%d exceeds the %d safety ceiling", host.Somaxconn, host.NetdevMaxBacklog, maxPerformanceBacklog)
	}
	if host.IPLocalPortRange.Min < 1024 || host.IPLocalPortRange.Max > 65535 ||
		host.IPLocalPortRange.Min >= host.IPLocalPortRange.Max {
		return fmt.Errorf("ip_local_port_range %d-%d is outside 1024-65535", host.IPLocalPortRange.Min, host.IPLocalPortRange.Max)
	}
	return nil
}

// validatePriorSysctl accepts only the allowlisted keys so a tampered backup
// cannot make the privileged helper write arbitrary kernel settings.
func validatePriorSysctl(content []byte) error {
	allowed := map[string]bool{
		"fs.file-max":                  true,
		"net.core.somaxconn":           true,
		"net.core.netdev_max_backlog":  true,
		"net.ipv4.ip_local_port_range": true,
	}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("line %q is not a sysctl assignment", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !allowed[key] {
			return fmt.Errorf("key %q is not allowlisted", key)
		}
		fields := strings.Fields(value)
		if key == "net.ipv4.ip_local_port_range" {
			if len(fields) != 2 {
				return fmt.Errorf("%s must contain two ports", key)
			}
			low, errLow := strconv.Atoi(fields[0])
			high, errHigh := strconv.Atoi(fields[1])
			if errLow != nil || errHigh != nil || low < 1024 || high > 65535 || low >= high {
				return fmt.Errorf("%s value %q is unsafe", key, value)
			}
			continue
		}
		if len(fields) != 1 {
			return fmt.Errorf("%s must contain one number", key)
		}
		number, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || number == 0 {
			return fmt.Errorf("%s value %q is not a positive number", key, value)
		}
		switch key {
		case "fs.file-max":
			if number > maxPerformanceFileMax {
				return fmt.Errorf("fs.file-max %d exceeds the safety ceiling", number)
			}
		case "net.core.somaxconn", "net.core.netdev_max_backlog":
			if number > uint64(maxPerformanceBacklog) {
				return fmt.Errorf("%s %d exceeds the safety ceiling", key, number)
			}
		}
	}
	return nil
}

var performanceLimitIdentity = regexp.MustCompile(`^(\*|@?[A-Za-z0-9_.-]+)$`)

// validatePriorLimits accepts only nofile soft/hard lines so a tampered backup
// cannot make the helper write arbitrary limits directives.
func validatePriorLimits(content []byte) error {
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 || (fields[1] != "soft" && fields[1] != "hard") || fields[2] != "nofile" {
			return fmt.Errorf("line %q is not a nofile limit", line)
		}
		if !performanceLimitIdentity.MatchString(fields[0]) {
			return fmt.Errorf("identity %q is not safe", fields[0])
		}
		value, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil || value == 0 || value > maxPerformanceNoFile {
			return fmt.Errorf("nofile value %q is unsafe", fields[3])
		}
	}
	return nil
}
