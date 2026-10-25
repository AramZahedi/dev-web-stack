package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// SettingsStore is the persisted profile/apply-state surface.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// HostHelper is the privileged host tuning surface.
type HostHelper interface {
	PerformanceStatus(ctx context.Context) (helperproto.PerformanceStatusResponse, error)
	PerformanceApply(ctx context.Context, req helperproto.PerformanceApplyRequest) (helperproto.PerformanceApplyResponse, error)
	PerformanceReset(ctx context.Context, req helperproto.PerformanceResetRequest) (helperproto.PerformanceResetResponse, error)
}

// WebServices is the catalog service surface used for the active web server and
// its fixed validate/reload commands.
type WebServices interface {
	List(ctx context.Context) ([]services.View, error)
	Validate(ctx context.Context, id services.ID) error
	Reload(ctx context.Context, id services.ID) error
}

// Sites is the daemon-side site surface used to merge PHP ini overrides through
// the existing validate-then-swap flow.
type Sites interface {
	List(ctx context.Context) ([]model.Site, error)
	ApplyPHP(ctx context.Context, ref string, change sites.PHPChange) (model.Site, error)
}

// ServiceConfig wires the performance service.
type ServiceConfig struct {
	Root     string
	Store    SettingsStore
	Helper   HostHelper
	Services WebServices
	Sites    Sites
	// HTTPPort and HTTPSPort are the builtin loopback proxy ports. When a
	// catalog web server is active (or serving_mode names a native nginx/Apache),
	// benchmarks target its public 80/443.
	HTTPPort    int
	HTTPSPort   int
	ServingMode string

	Now       func() time.Time
	Resources func() (Resources, error)
	LoadRun   func(ctx context.Context, opts LoadOptions) (LoadResult, []byte, error)
	Logger    *slog.Logger
}

// Service orchestrates performance profile preview, apply, reset, and
// benchmarks. All mutations are serialized; benchmarks have their own lock so
// they cannot overlap each other but do not block reads.
type Service struct {
	cfg ServiceConfig

	mu      sync.Mutex
	benchMu sync.Mutex
}

// NewService returns a performance service.
func NewService(cfg ServiceConfig) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Resources == nil {
		cfg.Resources = Collect
	}
	if cfg.LoadRun == nil {
		cfg.LoadRun = RunLoad
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{cfg: cfg}
}

// Status returns the persisted profile, measured resources, effective values,
// last applied state, and host tuning status.
func (s *Service) Status(ctx context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(ctx)
}

func (s *Service) statusLocked(ctx context.Context) (Status, error) {
	if s.cfg.Store == nil {
		return Status{}, errors.New("performance: store is not configured")
	}
	res, err := s.collectLocked()
	if err != nil {
		return Status{}, err
	}
	profile, custom, values, err := s.loadProfileLocked(ctx, res)
	if err != nil {
		return Status{}, err
	}
	applied, err := s.loadAppliedLocked(ctx)
	if err != nil {
		return Status{}, err
	}
	active, _, warnings := s.activeServerLocked(ctx)
	host := s.hostStatusLocked(ctx, applied)
	if host.Error != "" {
		warnings = append(warnings, "host tuning status is unavailable: "+host.Error)
	}
	return Status{
		Profile:      profile,
		Custom:       custom,
		Resources:    res,
		Values:       values,
		Applied:      applied,
		HostTuning:   host,
		ActiveServer: string(active),
		Warnings:     dedupeStrings(warnings),
	}, nil
}

// SetProfile persists the selected profile. Custom values are validated before
// they are stored and again on every preview/apply.
func (s *Service) SetProfile(ctx context.Context, req ProfileRequest) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Store == nil {
		return Status{}, errors.New("performance: store is not configured")
	}
	profile, err := ParseProfile(req.Profile)
	if err != nil {
		return Status{}, err
	}
	res, err := s.collectLocked()
	if err != nil {
		return Status{}, err
	}
	switch profile {
	case Custom:
		if req.Custom == nil {
			return Status{}, invalid("custom", "the custom profile requires explicit values")
		}
		custom := *req.Custom
		custom.Profile = Custom
		if _, err := Compute(res, Options{Profile: Custom, Custom: &custom}); err != nil {
			return Status{}, err
		}
		data, err := json.Marshal(custom)
		if err != nil {
			return Status{}, fmt.Errorf("performance: encode custom values: %w", err)
		}
		if err := s.cfg.Store.SetSetting(ctx, settingCustom, string(data)); err != nil {
			return Status{}, err
		}
	default:
		if req.Custom != nil {
			return Status{}, invalid("custom", "values are only accepted with the custom profile")
		}
	}
	if err := s.cfg.Store.SetSetting(ctx, settingProfile, string(profile)); err != nil {
		return Status{}, err
	}
	return s.statusLocked(ctx)
}

// Preview renders every managed include, PHP fragment, and the fixed host
// tuning diff without writing anything.
func (s *Service) Preview(ctx context.Context) (Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.collectLocked()
	if err != nil {
		return Preview{}, err
	}
	profile, _, values, err := s.loadProfileLocked(ctx, res)
	if err != nil {
		return Preview{}, err
	}
	active, _, warnings := s.activeServerLocked(ctx)
	if active == "" {
		warnings = append(warnings, "no active nginx/Apache catalog service; preview includes are shown but apply will skip web-server includes")
	}

	nginx, err := RenderNginx(values)
	if err != nil {
		return Preview{}, err
	}
	apache, err := RenderApache(values)
	if err != nil {
		return Preview{}, err
	}
	php := make([]PHPPreview, 0, len(values.PHP.Sites))
	for _, site := range values.PHP.Sites {
		pool, err := RenderPHPPool(values, site.SiteID)
		if err != nil {
			return Preview{}, err
		}
		ini, err := RenderPHPINI(values)
		if err != nil {
			return Preview{}, err
		}
		php = append(php, PHPPreview{SiteID: site.SiteID, Pool: string(pool), INI: string(ini)})
	}

	plan, err := BuildApplyPlan(s.cfg.Root, values, s.cfg.Now().UTC(), PriorHost{})
	if err != nil {
		return Preview{}, err
	}
	managed := make([]ManagedFilePreview, 0, len(plan.Files))
	for _, file := range plan.Files {
		managed = append(managed, ManagedFilePreview{
			Path:    file.Path,
			Content: string(file.Content),
			Active:  managedFileActive(file.Path, active),
		})
	}

	if warning := s.baseIncludeWarningLocked(active); warning != "" {
		warnings = append(warnings, warning)
	}
	host, hostWarnings := s.hostPreviewLocked(ctx, values)
	warnings = append(warnings, hostWarnings...)
	if len(values.PHP.Sites) > 0 {
		warnings = append(warnings, "PHP-FPM pool sizing fragments are generated under services/php/performance; per-site masters keep their current pool sizing (OPcache/realpath settings are merged through the site validate/swap flow).")
	}

	return Preview{
		Profile:      profile,
		Values:       values,
		Resources:    res,
		ActiveServer: string(active),
		Nginx:        IncludePreview{Main: string(nginx.Main), Events: string(nginx.Events), HTTP: string(nginx.HTTP)},
		Apache:       ApachePreview{Config: string(apache)},
		PHP:          php,
		Managed:      managed,
		Host:         host,
		Warnings:     dedupeStrings(warnings),
	}, nil
}

// Apply writes the active web server's generated includes, merges PHP ini
// settings through ApplyPHP, and optionally asks the helper to apply fixed host
// tuning paths. Any failure restores the previous managed files and PHP maps.
func (s *Service) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	if !req.Confirm {
		return ApplyResult{}, fmt.Errorf("%w: send confirm:true to apply a performance profile", ErrConfirmationRequired)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Store == nil {
		return ApplyResult{}, errors.New("performance: store is not configured")
	}
	res, err := s.collectLocked()
	if err != nil {
		return ApplyResult{}, err
	}
	profile, _, values, err := s.loadProfileLocked(ctx, res)
	if err != nil {
		return ApplyResult{}, err
	}
	now := s.cfg.Now().UTC()
	active, _, warnings, activeErr := s.activeServerStateLocked(ctx)
	if activeErr != nil {
		result := ApplyResult{
			Profile:   profile,
			AppliedAt: now,
			Warnings:  []string{activeErr.Error()},
			Steps:     []Step{{Name: "service-status", Status: "failed", Detail: activeErr.Error()}},
		}
		return result, &ApplyError{Err: activeErr, Result: result}
	}
	if active == "" {
		warnings = append(warnings, "no active nginx/Apache catalog service; web-server includes are skipped (database-only or native serving)")
	}
	if len(values.PHP.Sites) > 0 {
		warnings = append(warnings, "PHP-FPM pool sizing fragments are generated under services/php/performance; per-site masters keep their current pool sizing (OPcache/realpath settings are merged through the site validate/swap flow).")
	}
	if warning := s.baseIncludeWarningLocked(active); warning != "" {
		result := ApplyResult{
			Profile:      profile,
			AppliedAt:    now,
			HostTuning:   req.HostTuning,
			ActiveServer: string(active),
			Warnings:     dedupeStrings(append(warnings, warning)),
			Steps:        []Step{{Name: "include-conflict", Status: "failed", Detail: warning}},
		}
		return result, &ApplyError{Err: fmt.Errorf("%w: %s", ErrProfileConflict, warning), Result: result}
	}

	result := ApplyResult{
		Profile:      profile,
		AppliedAt:    now,
		HostTuning:   req.HostTuning,
		ActiveServer: string(active),
		Warnings:     dedupeStrings(warnings),
	}
	step := func(name, status, detail string) {
		result.Steps = append(result.Steps, Step{Name: name, Status: status, Detail: detail})
	}

	plan, err := BuildApplyPlan(s.cfg.Root, values, now, PriorHost{}, s.existingPHPFragmentsLocked()...)
	if err != nil {
		step("render", "failed", err.Error())
		return result, &ApplyError{Err: err, Result: result}
	}
	files := filterApplyFiles(plan.Files, active)
	result.BackupDir = plan.BackupDir

	snapshots := make([]fileSnapshot, 0, len(files))
	for _, file := range files {
		snapshot, err := takeFileSnapshot(file.Path)
		if err != nil {
			step("snapshot", "failed", err.Error())
			return result, &ApplyError{Err: err, Result: result}
		}
		snapshots = append(snapshots, snapshot)
		if snapshot.Existed {
			rel, relErr := filepath.Rel(s.cfg.Root, file.Path)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				step("snapshot", "failed", "managed file is outside the devstack root")
				return result, &ApplyError{Err: fmt.Errorf("%w: %s is outside %s", ErrInvalid, file.Path, s.cfg.Root), Result: result}
			}
			if err := writeFileAtomic(filepath.Join(plan.BackupDir, rel), snapshot.Content, snapshot.Mode); err != nil {
				step("snapshot", "failed", err.Error())
				return result, &ApplyError{Err: err, Result: result}
			}
		}
	}

	for _, file := range files {
		if err := writeFileAtomic(file.Path, file.Content, file.Mode); err != nil {
			step("write-includes", "failed", err.Error())
			result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, nil, false, "", false))
			return result, &ApplyError{Err: err, Result: result}
		}
		result.Files = append(result.Files, ManagedFilePreview{
			Path: file.Path, Content: string(file.Content), Active: managedFileActive(file.Path, active),
		})
	}
	step("write-includes", "ok", fmt.Sprintf("%d generated include file(s)", len(files)))

	reloaded := false
	priorApplied, _ := s.loadAppliedLocked(ctx)
	phpSnapshots, phpErr := s.applyPHPIniLocked(ctx, values, priorApplied)
	if phpErr != nil {
		step("php-ini", "failed", phpErr.Error())
		result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, false, "", false))
		return result, &ApplyError{Err: phpErr, Result: result}
	}
	if len(phpSnapshots) > 0 {
		step("php-ini", "ok", fmt.Sprintf("%d PHP site(s) updated through validate/swap", len(phpSnapshots)))
	} else {
		step("php-ini", "skipped", "no running PHP sites")
	}

	if active != "" {
		if err := s.validateAndReloadLocked(ctx, active); err != nil {
			step("reload", "failed", err.Error())
			result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, false, "", false))
			return result, &ApplyError{Err: err, Result: result}
		}
		reloaded = true
		step("reload", "ok", "validated and reloaded "+string(active))
	} else {
		step("reload", "skipped", "no active catalog web server")
	}

	hostApplied := false
	if req.HostTuning {
		if s.cfg.Helper == nil {
			err := errors.New("helper is not configured")
			step("host-tuning", "failed", err.Error())
			result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, false, active, reloaded))
			return result, &ApplyError{Err: err, Result: result}
		}
		hostResp, err := s.cfg.Helper.PerformanceApply(ctx, helperproto.PerformanceApplyRequest{
			Profile: string(values.Profile),
			Host:    hostValuesForProto(values.Host),
		})
		if err != nil {
			step("host-tuning", "failed", err.Error())
			result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, false, active, reloaded))
			return result, &ApplyError{Err: err, Result: result}
		}
		if !hostResp.Applied {
			err := errors.New("helper did not apply host tuning")
			step("host-tuning", "failed", err.Error())
			result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, false, active, reloaded))
			return result, &ApplyError{Err: err, Result: result}
		}
		hostApplied = true
		if hostResp.BackupDir != "" {
			result.BackupDir = hostResp.BackupDir
		}
		for _, helperStep := range hostResp.Steps {
			step("host:"+helperStep.Name, helperStep.Status, helperStep.Detail)
		}
		step("host-tuning", "ok", "fixed sysctl/limits paths verified by helper")
	} else {
		step("host-tuning", "skipped", "host tuning is opt-in (host_tuning:true)")
	}

	if len(plan.Stale) > 0 {
		if err := removeFiles(plan.Stale); err != nil {
			result.Warnings = append(result.Warnings, "remove stale PHP fragments: "+err.Error())
		} else {
			step("remove-stale", "ok", fmt.Sprintf("%d inactive PHP fragment(s)", len(plan.Stale)))
		}
	}

	applied := AppliedState{
		Profile:    profile,
		Values:     values,
		AppliedAt:  now,
		HostTuning: hostApplied,
		BackupDir:  result.BackupDir,
		Servers:    activeServers(active),
		PHPIni:     phpSnapshots,
		Steps:      result.Steps,
	}
	data, err := json.Marshal(applied)
	if err != nil {
		step("persist", "failed", err.Error())
		result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, hostApplied, active, reloaded))
		return result, &ApplyError{Err: err, Result: result}
	}
	if err := s.cfg.Store.SetSetting(ctx, settingApplied, string(data)); err != nil {
		step("persist", "failed", err.Error())
		result.RollbackError = errorString(s.rollbackApplyLocked(ctx, snapshots, phpSnapshots, hostApplied, active, reloaded))
		return result, &ApplyError{Err: err, Result: result}
	}
	step("persist", "ok", "profile and applied state recorded")
	result.Applied = true
	s.cfg.Logger.Info("performance applied",
		"profile", profile,
		"active_server", active,
		"host_tuning", hostApplied,
		"backup_dir", result.BackupDir,
	)
	return result, nil
}

// Reset restores the pre-devstack host files and replaces the managed includes
// with safe built-in snippets, then validates/reloads the active server.
func (s *Service) Reset(ctx context.Context, req ResetRequest) (ResetResult, error) {
	if !req.Confirm {
		return ResetResult{}, fmt.Errorf("%w: send confirm:true to reset performance settings", ErrConfirmationRequired)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Store == nil {
		return ResetResult{}, errors.New("performance: store is not configured")
	}
	applied, err := s.loadAppliedLocked(ctx)
	if err != nil {
		return ResetResult{}, err
	}
	active, _, warnings := s.activeServerLocked(ctx)
	result := ResetResult{BackupDir: ""}
	if applied != nil {
		result.BackupDir = applied.BackupDir
	}
	step := func(name, status, detail string) {
		result.Steps = append(result.Steps, Step{Name: name, Status: status, Detail: detail})
	}

	siteIDs, err := s.currentPHPSiteIDsLocked(ctx)
	if err != nil {
		step("php-sites", "failed", err.Error())
		return result, &ResetError{Err: err, Result: result}
	}
	if applied != nil {
		for _, site := range applied.Values.PHP.Sites {
			siteIDs = append(siteIDs, site.SiteID)
		}
	}
	resetFiles, err := ResetManagedFiles(s.cfg.Root, siteIDs)
	if err != nil {
		step("render-reset", "failed", err.Error())
		return result, &ResetError{Err: err, Result: result}
	}
	for _, file := range resetFiles {
		if err := writeFileAtomic(file.Path, file.Content, file.Mode); err != nil {
			step("write-reset", "failed", err.Error())
			return result, &ResetError{Err: err, Result: result}
		}
	}
	step("write-reset", "ok", fmt.Sprintf("%d safe include file(s)", len(resetFiles)))
	keep := make(map[string]bool, len(resetFiles))
	for _, file := range resetFiles {
		keep[file.Path] = true
	}
	if stale, err := s.stalePHPFragmentsLocked(keep); err != nil {
		result.Warnings = append(result.Warnings, "read PHP fragments: "+err.Error())
	} else if len(stale) > 0 {
		if err := removeFiles(stale); err != nil {
			result.Warnings = append(result.Warnings, "remove stale PHP fragments: "+err.Error())
		} else {
			step("remove-stale", "ok", fmt.Sprintf("%d inactive PHP fragment(s)", len(stale)))
		}
	}

	if applied != nil && len(applied.PHPIni) > 0 {
		if err := s.restorePHPIniLocked(ctx, applied.PHPIni); err != nil {
			step("php-ini", "failed", err.Error())
			return result, &ResetError{Err: err, Result: result}
		}
		step("php-ini", "ok", fmt.Sprintf("%d PHP site(s) restored", len(applied.PHPIni)))
	} else {
		step("php-ini", "skipped", "no PHP ini snapshot")
	}

	hostApplied := applied != nil && applied.HostTuning
	if s.cfg.Helper != nil {
		if status, err := s.cfg.Helper.PerformanceStatus(ctx); err == nil && status.Managed {
			hostApplied = true
		}
	}
	if hostApplied {
		if s.cfg.Helper == nil {
			err := errors.New("helper is not configured")
			step("host-tuning", "failed", err.Error())
			return result, &ResetError{Err: err, Result: result}
		}
		resp, err := s.cfg.Helper.PerformanceReset(ctx, helperproto.PerformanceResetRequest{})
		if err != nil {
			step("host-tuning", "failed", err.Error())
			return result, &ResetError{Err: err, Result: result}
		}
		if resp.BackupDir != "" {
			result.BackupDir = resp.BackupDir
		}
		step("host-tuning", "ok", "pre-devstack files restored")
	} else {
		step("host-tuning", "skipped", "no helper-managed host tuning")
	}

	if active != "" {
		if err := s.validateAndReloadLocked(ctx, active); err != nil {
			step("reload", "failed", err.Error())
			return result, &ResetError{Err: err, Result: result}
		}
		step("reload", "ok", "validated and reloaded "+string(active))
	} else {
		step("reload", "skipped", "no active catalog web server")
	}

	if err := s.cfg.Store.DeleteSetting(ctx, settingApplied); err != nil && !errors.Is(err, store.ErrNotFound) {
		step("persist", "failed", err.Error())
		return result, &ResetError{Err: err, Result: result}
	}
	step("persist", "ok", "applied state cleared")
	result.Reset = true
	result.Warnings = dedupeStrings(warnings)
	s.cfg.Logger.Info("performance reset", "active_server", active)
	return result, nil
}

// RunBenchmark validates a registered site, runs the built-in load generator,
// and stores result.json plus raw output under logs/performance/<id>/.
func (s *Service) RunBenchmark(ctx context.Context, req BenchmarkRequest) (BenchmarkResult, error) {
	if s.cfg.Store == nil {
		return BenchmarkResult{}, errors.New("performance: store is not configured")
	}
	site, err := s.findSiteLocked(ctx, req.Site)
	if err != nil {
		return BenchmarkResult{}, err
	}
	duration := req.DurationSeconds
	if duration == 0 {
		duration = 30
	}
	if duration < 1 || duration > 3600 {
		return BenchmarkResult{}, invalid("benchmark duration", "%d is outside 1-3600 seconds", duration)
	}
	connections := req.Connections
	if connections == 0 {
		connections = 100
	}
	if connections < 1 || connections > 100000 {
		return BenchmarkResult{}, invalid("benchmark connections", "%d is outside 1-100000", connections)
	}
	if !s.benchMu.TryLock() {
		return BenchmarkResult{}, ErrBenchmarkBusy
	}
	defer s.benchMu.Unlock()

	profile := Balanced
	if raw, err := s.cfg.Store.GetSetting(ctx, settingProfile); err == nil {
		if parsed, parseErr := ParseProfile(raw); parseErr == nil {
			profile = parsed
		}
	}
	applied, _ := s.loadAppliedLocked(ctx)
	target, insecure, err := s.benchmarkTargetLocked(ctx, site)
	if err != nil {
		return BenchmarkResult{}, err
	}
	now := s.cfg.Now().UTC()
	id := now.Format("20060102T150405.000000000Z")
	dir := filepath.Join(s.cfg.Root, "logs", "performance", id)
	for suffix := 1; ; suffix++ {
		_, statErr := os.Stat(dir)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return BenchmarkResult{}, statErr
		}
		id = fmt.Sprintf("%s-%d", now.Format("20060102T150405.000000000Z"), suffix)
		dir = filepath.Join(s.cfg.Root, "logs", "performance", id)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return BenchmarkResult{}, err
	}
	result := BenchmarkResult{
		ID:              id,
		Site:            site.Name,
		Target:          target,
		Profile:         profile,
		Status:          BenchmarkCompleted,
		Tool:            LoadToolName,
		ToolVersion:     LoadToolVersion,
		Limitations:     append([]string(nil), LoadLimitations...),
		DurationSeconds: duration,
		Connections:     connections,
		StartedAt:       now,
		Directory:       dir,
		RawFile:         filepath.Join(dir, "raw.log"),
	}
	if applied != nil {
		appliedAt := applied.AppliedAt
		result.ProfileAppliedAt = &appliedAt
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(duration)*time.Second+15*time.Second)
	defer cancel()
	load, raw, loadErr := s.cfg.LoadRun(runCtx, LoadOptions{
		URL:         target,
		HostHeader:  site.Domain,
		Duration:    time.Duration(duration) * time.Second,
		Connections: connections,
		InsecureTLS: insecure,
	})
	result.FinishedAt = s.cfg.Now().UTC()
	if load.Tool != "" {
		result.Tool = load.Tool
		result.ToolVersion = load.Version
		if len(load.Limitations) > 0 {
			result.Limitations = append([]string(nil), load.Limitations...)
		}
	}
	if loadErr == nil && (load.Requests <= 0 || load.RPS <= 0) {
		loadErr = errors.New("load generator produced no measured requests")
	}
	if loadErr != nil {
		result.Status = BenchmarkFailed
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			errors.Is(loadErr, context.Canceled) || errors.Is(loadErr, context.DeadlineExceeded) {
			result.Status = BenchmarkCanceled
		}
		result.Error = loadErr.Error()
		result.Measured = nil
	} else {
		result.Measured = &BenchmarkMeasurement{
			Requests:        load.Requests,
			Errors:          load.Errors,
			RPS:             load.RPS,
			P50MS:           load.P50MS,
			P95MS:           load.P95MS,
			P99MS:           load.P99MS,
			DurationSeconds: load.Duration.Seconds(),
		}
		if !load.StartedAt.IsZero() {
			result.StartedAt = load.StartedAt
		}
		if !load.FinishedAt.IsZero() {
			result.FinishedAt = load.FinishedAt
		}
	}
	if len(raw) == 0 {
		raw = []byte(fmt.Sprintf("tool=%s/%s\nurl=%s\nstatus=%s\n", result.Tool, result.ToolVersion, result.Target, result.Status))
	}
	if err := os.WriteFile(result.RawFile, raw, 0o644); err != nil {
		return result, err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
		return result, err
	}
	if loadErr != nil {
		s.cfg.Logger.Warn("performance benchmark failed",
			"id", result.ID, "site", result.Site, "status", result.Status, "error", result.Error)
		return result, loadErr
	}
	s.cfg.Logger.Info("performance benchmark completed",
		"id", result.ID,
		"site", result.Site,
		"requests", result.Measured.Requests,
		"errors", result.Measured.Errors,
		"rps", result.Measured.RPS,
	)
	return result, nil
}

// ListBenchmarks returns stored benchmark results, newest first.
func (s *Service) ListBenchmarks(ctx context.Context) ([]BenchmarkResult, error) {
	root := filepath.Join(s.cfg.Root, "logs", "performance")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BenchmarkResult{}, nil
		}
		return nil, err
	}
	results := make([]BenchmarkResult, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := validateBenchmarkID(entry.Name()); err != nil {
			continue
		}
		result, err := s.GetBenchmark(ctx, entry.Name())
		if err != nil {
			continue
		}
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].StartedAt.Equal(results[j].StartedAt) {
			return results[i].ID > results[j].ID
		}
		return results[i].StartedAt.After(results[j].StartedAt)
	})
	return results, nil
}

// GetBenchmark returns one stored benchmark result by directory id.
func (s *Service) GetBenchmark(_ context.Context, id string) (BenchmarkResult, error) {
	if err := validateBenchmarkID(id); err != nil {
		return BenchmarkResult{}, ErrBenchmarkNotFound
	}
	path := filepath.Join(s.cfg.Root, "logs", "performance", id, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BenchmarkResult{}, ErrBenchmarkNotFound
		}
		return BenchmarkResult{}, err
	}
	var result BenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil {
		return BenchmarkResult{}, fmt.Errorf("performance: decode benchmark %q: %w", id, err)
	}
	return result, nil
}

func validateBenchmarkID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || filepath.Base(id) != id {
		return ErrBenchmarkNotFound
	}
	parsed, err := time.Parse("20060102T150405.000000000Z", id)
	if err != nil {
		// Accept the collision suffix form.
		base := id
		if index := strings.LastIndex(id, "-"); index > 0 {
			base = id[:index]
		}
		if _, err := time.Parse("20060102T150405.000000000Z", base); err != nil {
			return ErrBenchmarkNotFound
		}
	} else if parsed.IsZero() {
		return ErrBenchmarkNotFound
	}
	return nil
}

func (s *Service) collectLocked() (Resources, error) {
	res, err := s.cfg.Resources()
	if err != nil {
		return Resources{}, err
	}
	return res, nil
}

func (s *Service) loadProfileLocked(ctx context.Context, res Resources) (Profile, *Values, Values, error) {
	profile := Balanced
	if raw, err := s.cfg.Store.GetSetting(ctx, settingProfile); err == nil && strings.TrimSpace(raw) != "" {
		parsed, err := ParseProfile(raw)
		if err != nil {
			return "", nil, Values{}, err
		}
		profile = parsed
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", nil, Values{}, err
	}

	var custom *Values
	if raw, err := s.cfg.Store.GetSetting(ctx, settingCustom); err == nil && strings.TrimSpace(raw) != "" {
		var stored Values
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return "", nil, Values{}, fmt.Errorf("%w: stored custom values are invalid JSON: %v", ErrInvalid, err)
		}
		custom = &stored
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", nil, Values{}, err
	}

	if profile == Custom {
		if custom == nil {
			return "", nil, Values{}, invalid("custom", "the custom profile has no stored values")
		}
		values, err := Compute(res, Options{Profile: Custom, Custom: custom})
		if err != nil {
			return "", nil, Values{}, err
		}
		return profile, custom, values, nil
	}
	sites, err := s.currentPHPSiteIDsLocked(ctx)
	if err != nil {
		return "", nil, Values{}, err
	}
	values, err := Compute(res, Options{Profile: profile, PHPSites: sites})
	if err != nil {
		return "", nil, Values{}, err
	}
	return profile, custom, values, nil
}

func (s *Service) currentPHPSiteIDsLocked(ctx context.Context) ([]string, error) {
	if s.cfg.Sites == nil {
		return nil, nil
	}
	list, err := s.cfg.Sites.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list PHP sites: %w", err)
	}
	ids := make([]string, 0, len(list))
	for _, site := range list {
		if site.Kind == model.SitePHP && site.Status == model.StatusRunning {
			ids = append(ids, site.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Service) findSiteLocked(ctx context.Context, ref string) (model.Site, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.Site{}, fmt.Errorf("%w: site is required", ErrInvalid)
	}
	if s.cfg.Sites == nil {
		return model.Site{}, ErrBenchmarkSite
	}
	list, err := s.cfg.Sites.List(ctx)
	if err != nil {
		return model.Site{}, err
	}
	for _, site := range list {
		if site.ID == ref || site.Name == ref {
			return site, nil
		}
	}
	return model.Site{}, fmt.Errorf("%w: %q", ErrBenchmarkSite, ref)
}

func (s *Service) activeServerLocked(ctx context.Context) (services.ID, []services.View, []string) {
	active, views, warnings, err := s.activeServerStateLocked(ctx)
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	return active, views, warnings
}

func (s *Service) activeServerStateLocked(ctx context.Context) (services.ID, []services.View, []string, error) {
	if s.cfg.Services == nil {
		return "", nil, []string{"service status is unavailable; assuming no catalog web server is active"}, nil
	}
	views, err := s.cfg.Services.List(ctx)
	if err != nil {
		return "", nil, nil, fmt.Errorf("service status is unavailable: %w", err)
	}
	var warnings []string
	for _, view := range views {
		if view.Group != services.GroupWeb || !view.Active {
			continue
		}
		switch view.ID {
		case services.IDNginx, services.IDApache:
			return view.ID, views, warnings, nil
		default:
			warnings = append(warnings, fmt.Sprintf("active web service %q is not a managed nginx/Apache catalog ID; includes are skipped", view.ID))
		}
	}
	return "", views, warnings, nil
}

func (s *Service) validateAndReloadLocked(ctx context.Context, id services.ID) error {
	if s.cfg.Services == nil {
		return errors.New("service manager is not configured")
	}
	if err := s.cfg.Services.Validate(ctx, id); err != nil {
		return fmt.Errorf("validate %s: %w", id, err)
	}
	if err := s.cfg.Services.Reload(ctx, id); err != nil {
		return fmt.Errorf("reload %s: %w", id, err)
	}
	return nil
}

func (s *Service) loadAppliedLocked(ctx context.Context) (*AppliedState, error) {
	raw, err := s.cfg.Store.GetSetting(ctx, settingApplied)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var applied AppliedState
	if err := json.Unmarshal([]byte(raw), &applied); err != nil {
		return nil, fmt.Errorf("performance: stored applied state is invalid: %w", err)
	}
	return &applied, nil
}

func (s *Service) hostStatusLocked(ctx context.Context, applied *AppliedState) HostTuningStatus {
	status := HostTuningStatus{
		SysctlPath: SysctlTarget,
		LimitsPath: LimitsTarget,
	}
	if applied != nil {
		status.Applied = applied.HostTuning
		status.BackupDir = applied.BackupDir
		status.AppliedAt = &applied.AppliedAt
		status.Profile = applied.Profile
	}
	if s.cfg.Helper == nil {
		status.Error = "helper is not configured"
		return status
	}
	resp, err := s.cfg.Helper.PerformanceStatus(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Available = true
	status.Managed = resp.Managed
	status.Applied = resp.Managed
	if resp.SysctlPath != "" {
		status.SysctlPath = resp.SysctlPath
	}
	if resp.LimitsPath != "" {
		status.LimitsPath = resp.LimitsPath
	}
	if resp.BackupDir != "" {
		status.BackupDir = resp.BackupDir
	}
	if resp.AppliedAt != nil {
		status.AppliedAt = resp.AppliedAt
	}
	if resp.Profile != "" {
		status.Profile = Profile(resp.Profile)
	}
	return status
}

func (s *Service) hostPreviewLocked(ctx context.Context, values Values) (HostPreview, []string) {
	preview := HostPreview{
		OptIn:    true,
		Warnings: []string{"Host tuning is applied only when apply includes host_tuning:true."},
	}
	proposedSysctl, err := RenderHostSysctl(values.Profile, values.Host)
	if err != nil {
		preview.Warnings = append(preview.Warnings, err.Error())
		return preview, preview.Warnings
	}
	proposedLimits, err := RenderHostLimits(values.Profile, values.Host)
	if err != nil {
		preview.Warnings = append(preview.Warnings, err.Error())
		return preview, preview.Warnings
	}
	current := helperproto.PerformanceStatusResponse{}
	if s.cfg.Helper != nil {
		if status, err := s.cfg.Helper.PerformanceStatus(ctx); err == nil {
			current = status
		} else {
			preview.Warnings = append(preview.Warnings, "cannot read current host tuning: "+err.Error())
		}
	} else {
		preview.Warnings = append(preview.Warnings, "helper is not configured; current host tuning is unknown")
	}
	for _, item := range []struct {
		path     string
		exists   bool
		current  []byte
		proposed []byte
	}{
		{SysctlTarget, current.SysctlPresent, current.SysctlContent, proposedSysctl},
		{LimitsTarget, current.LimitsPresent, current.LimitsContent, proposedLimits},
	} {
		preview.Files = append(preview.Files, HostFilePreview{
			Path:     item.path,
			Exists:   item.exists,
			Current:  string(item.current),
			Proposed: string(item.proposed),
			Diff:     lineDiff(string(item.current), string(item.proposed)),
		})
	}
	return preview, preview.Warnings
}

func (s *Service) applyPHPIniLocked(ctx context.Context, values Values, prior *AppliedState) (map[string]map[string]string, error) {
	snapshots := make(map[string]map[string]string)
	if prior != nil {
		for id, ini := range prior.PHPIni {
			if ini == nil {
				ini = map[string]string{}
			}
			snapshots[id] = cloneStringMap(ini)
		}
	}
	if s.cfg.Sites == nil || len(values.PHP.Sites) == 0 {
		return snapshots, nil
	}
	list, err := s.cfg.Sites.List(ctx)
	if err != nil {
		return snapshots, err
	}
	byID := make(map[string]model.Site, len(list))
	for _, site := range list {
		byID[site.ID] = site
	}
	settings := performanceIniSettings(values)
	for _, generated := range values.PHP.Sites {
		site, ok := byID[generated.SiteID]
		if !ok || site.Kind != model.SitePHP || site.Status != model.StatusRunning {
			continue
		}
		current := cloneStringMap(site.PHPIni)
		if current == nil {
			current = map[string]string{}
		}
		if _, exists := snapshots[site.ID]; !exists {
			snapshots[site.ID] = cloneStringMap(current)
		}
		merged := cloneStringMap(current)
		for key, value := range settings {
			merged[key] = value
		}
		if _, err := s.cfg.Sites.ApplyPHP(ctx, site.ID, sites.PHPChange{Ini: merged}); err != nil {
			return snapshots, err
		}
	}
	return snapshots, nil
}

func (s *Service) restorePHPIniLocked(ctx context.Context, previous map[string]map[string]string) error {
	if s.cfg.Sites == nil || len(previous) == 0 {
		return nil
	}
	list, err := s.cfg.Sites.List(ctx)
	if err != nil {
		return fmt.Errorf("list PHP sites: %w", err)
	}
	byID := make(map[string]model.Site, len(list))
	for _, site := range list {
		byID[site.ID] = site
	}
	ids := make([]string, 0, len(previous))
	for id := range previous {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		site, ok := byID[id]
		if !ok {
			// The site was removed after the profile was applied; its config
			// directory is gone, so there is nothing to restore.
			continue
		}
		current := cloneStringMap(site.PHPIni)
		if current == nil {
			current = map[string]string{}
		}
		original := previous[id]
		for _, key := range managedPHPIniKeys() {
			if value, ok := original[key]; ok {
				current[key] = value
			} else {
				delete(current, key)
			}
		}
		if _, err := s.cfg.Sites.ApplyPHP(ctx, id, sites.PHPChange{Ini: current}); err != nil {
			errs = append(errs, fmt.Errorf("restore PHP site %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) rollbackApplyLocked(
	ctx context.Context,
	snapshots []fileSnapshot,
	phpSnapshots map[string]map[string]string,
	hostApplied bool,
	active services.ID,
	reloaded bool,
) error {
	var errs []error
	for _, snapshot := range snapshots {
		if snapshot.Existed {
			if err := writeFileAtomic(snapshot.Path, snapshot.Content, snapshot.Mode); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", snapshot.Path, err))
			}
			continue
		}
		if err := os.Remove(snapshot.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", snapshot.Path, err))
		}
	}
	if err := s.restorePHPIniLocked(ctx, phpSnapshots); err != nil {
		errs = append(errs, err)
	}
	if hostApplied && s.cfg.Helper != nil {
		if _, err := s.cfg.Helper.PerformanceReset(ctx, helperproto.PerformanceResetRequest{}); err != nil {
			errs = append(errs, fmt.Errorf("reset host tuning: %w", err))
		}
	}
	if reloaded && active != "" {
		if err := s.validateAndReloadLocked(ctx, active); err != nil {
			errs = append(errs, fmt.Errorf("reload restored includes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) baseIncludeWarningLocked(active services.ID) string {
	switch active {
	case services.IDNginx:
		base := filepath.Join(s.cfg.Root, "services", "nginx", "config", "nginx.conf")
		return includeWarning(base, []string{"/conf.d/performance/main.conf", "/conf.d/performance/events.conf", "/conf.d/performance/http.conf"})
	case services.IDApache:
		base := filepath.Join(s.cfg.Root, "services", "apache", "config", "httpd.conf")
		data, err := os.ReadFile(base)
		if err != nil {
			return ""
		}
		text := string(data)
		if strings.Contains(text, "conf.d/performance.conf") || strings.Contains(text, "conf.d/*.conf") {
			return ""
		}
		return fmt.Sprintf("%s does not include conf.d/performance.conf or conf.d/*.conf; add the include or future applies will not take effect", base)
	}
	return ""
}

func includeWarning(base string, wants []string) string {
	data, err := os.ReadFile(base)
	if err != nil {
		return ""
	}
	text := string(data)
	for _, want := range wants {
		if strings.Contains(text, want) {
			continue
		}
		// Accept an equivalent glob for the same managed directory.
		if strings.Contains(want, "/conf.d/performance/") && strings.Contains(text, "/conf.d/performance/*.conf") {
			continue
		}
		return fmt.Sprintf("%s does not include the performance snippet %q; add the include or future applies will not take effect", base, want)
	}
	return ""
}

func (s *Service) benchmarkTargetLocked(ctx context.Context, site model.Site) (string, bool, error) {
	if strings.TrimSpace(site.Domain) == "" {
		return "", false, fmt.Errorf("%w: site %q has no domain", ErrInvalid, site.Name)
	}
	active, _, _ := s.activeServerLocked(ctx)
	httpsPort := s.cfg.HTTPSPort
	httpPort := s.cfg.HTTPPort
	if httpsPort == 0 {
		httpsPort = 8443
	}
	if httpPort == 0 {
		httpPort = 8080
	}
	if active != "" || s.cfg.ServingMode == "nginx" || s.cfg.ServingMode == "apache" {
		httpsPort = 443
		httpPort = 80
	}
	scheme := "http"
	port := httpPort
	insecure := false
	if site.TLS || site.ForceHTTPS {
		scheme = "https"
		port = httpsPort
		insecure = true
	}
	target := scheme + "://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/"
	return target, insecure, nil
}

func managedPHPIniKeys() []string {
	return []string{
		"opcache.enable",
		"opcache.memory_consumption",
		"opcache.interned_strings_buffer",
		"opcache.max_accelerated_files",
		"opcache.validate_timestamps",
		"opcache.revalidate_freq",
		"realpath_cache_size",
		"realpath_cache_ttl",
	}
}

func performanceIniSettings(values Values) map[string]string {
	settings := map[string]string{
		"opcache.memory_consumption":      strconv.Itoa(values.PHP.Opcache.MemoryMB),
		"opcache.interned_strings_buffer": strconv.Itoa(values.PHP.Opcache.InternedStringsMB),
		"opcache.max_accelerated_files":   strconv.Itoa(values.PHP.Opcache.MaxAcceleratedFiles),
		"opcache.revalidate_freq":         strconv.Itoa(values.PHP.Opcache.RevalidateFreqSeconds),
		"realpath_cache_size":             strconv.Itoa(values.PHP.RealpathCacheSizeKB) + "K",
		"realpath_cache_ttl":              strconv.Itoa(values.PHP.RealpathCacheTTLSeconds),
	}
	if values.PHP.Opcache.Enabled {
		settings["opcache.enable"] = "1"
	} else {
		settings["opcache.enable"] = "0"
	}
	if values.PHP.Opcache.ValidateTimestamps {
		settings["opcache.validate_timestamps"] = "1"
	} else {
		settings["opcache.validate_timestamps"] = "0"
	}
	return settings
}

func hostValuesForProto(host HostValues) helperproto.PerformanceHostValues {
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

func filterApplyFiles(files []ManagedFile, active services.ID) []ManagedFile {
	out := make([]ManagedFile, 0, len(files))
	for _, file := range files {
		if strings.Contains(file.Path, string(filepath.Separator)+"services"+string(filepath.Separator)+"nginx"+string(filepath.Separator)) && active != services.IDNginx {
			continue
		}
		if strings.Contains(file.Path, string(filepath.Separator)+"services"+string(filepath.Separator)+"apache"+string(filepath.Separator)) && active != services.IDApache {
			continue
		}
		out = append(out, file)
	}
	return out
}

func managedFileActive(path string, active services.ID) bool {
	switch {
	case strings.Contains(path, string(filepath.Separator)+"services"+string(filepath.Separator)+"nginx"+string(filepath.Separator)):
		return active == services.IDNginx
	case strings.Contains(path, string(filepath.Separator)+"services"+string(filepath.Separator)+"apache"+string(filepath.Separator)):
		return active == services.IDApache
	default:
		return true
	}
}

type fileSnapshot struct {
	Path    string
	Existed bool
	Content []byte
	Mode    os.FileMode
}

func takeFileSnapshot(path string) (fileSnapshot, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileSnapshot{Path: path, Mode: 0o644}, nil
		}
		return fileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	return fileSnapshot{Path: path, Existed: true, Content: data, Mode: mode}, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (s *Service) existingPHPFragmentsLocked() []string {
	dir := filepath.Join(s.cfg.Root, "services", "php", "performance")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func (s *Service) stalePHPFragmentsLocked(keep map[string]bool) ([]string, error) {
	paths := s.existingPHPFragmentsLocked()
	stale := make([]string, 0)
	for _, path := range paths {
		if !keep[path] {
			stale = append(stale, path)
		}
	}
	return stale, nil
}

func removeFiles(paths []string) error {
	var errs []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func activeServers(id services.ID) []string {
	if id == "" {
		return nil
	}
	return []string{string(id)}
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func lineDiff(current, proposed string) string {
	if current == proposed {
		return ""
	}
	oldLines := splitDiffLines(current)
	newLines := splitDiffLines(proposed)
	const maxLines = 400
	if len(oldLines) > maxLines || len(newLines) > maxLines {
		return "- current\n+ " + proposed
	}
	// Longest common subsequence table for a small, bounded config diff.
	table := make([][]int, len(oldLines)+1)
	for i := range table {
		table[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			out.WriteString("  ")
			out.WriteString(oldLines[i])
			out.WriteByte('\n')
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			out.WriteString("- ")
			out.WriteString(oldLines[i])
			out.WriteByte('\n')
			i++
		default:
			out.WriteString("+ ")
			out.WriteString(newLines[j])
			out.WriteByte('\n')
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		out.WriteString("- ")
		out.WriteString(oldLines[i])
		out.WriteByte('\n')
	}
	for ; j < len(newLines); j++ {
		out.WriteString("+ ")
		out.WriteString(newLines[j])
		out.WriteByte('\n')
	}
	return out.String()
}

func splitDiffLines(value string) []string {
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}
