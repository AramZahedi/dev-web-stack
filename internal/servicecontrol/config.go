package servicecontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/serviceconfig"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// MaxConfigFileBytes is the hard limit for a service config file. The same
// limit applies to edits submitted through the API.
const MaxConfigFileBytes = 1 << 20

// Conflict resolutions accepted by Apply.
const (
	ConfigResolutionKeep  = "keep"
	ConfigResolutionUse   = "use"
	ConfigResolutionMerge = "merge"
)

// ConfigApplyFailure kinds.
const (
	ConfigFailureValidate = "validate"
	ConfigFailureReload   = "reload"
)

var (
	// ErrConfigNotInstalled reports that a service has not been scaffolded yet.
	ErrConfigNotInstalled = errors.New("service is not installed")
	// ErrConfigPathInvalid reports a syntactically invalid config path.
	ErrConfigPathInvalid = errors.New("invalid service config path")
	// ErrConfigPathDenied reports a path outside the allowed config policy.
	ErrConfigPathDenied = errors.New("service config path is not allowed")
	// ErrConfigTooLarge reports a config file or edit above the 1 MiB limit.
	ErrConfigTooLarge = errors.New("service config file exceeds the 1 MiB limit")
	// ErrConfigBinary reports content with NUL bytes or invalid UTF-8.
	ErrConfigBinary = errors.New("service config content must be text")
	// ErrConfigNotFound reports a missing config file.
	ErrConfigNotFound = errors.New("service config file not found")
	// ErrConfigConflict reports a tracked generated file that needs an explicit
	// keep/use/merge resolution before it can be changed.
	ErrConfigConflict = errors.New("service config conflict requires an explicit resolution")
	// ErrConfigResolutionInvalid reports an unknown or inapplicable resolution.
	ErrConfigResolutionInvalid = errors.New("invalid service config conflict resolution")
	// ErrConfigResetUnavailable reports that no backup exists to restore.
	ErrConfigResetUnavailable = errors.New("no previous service config backup is available")
	// ErrConfigValidationFailed reports a failed catalog validation command.
	ErrConfigValidationFailed = errors.New("service config validation failed")
	// ErrConfigReloadFailed reports a failed service reload after validation.
	ErrConfigReloadFailed = errors.New("service config reload failed")
	// ErrConfigServiceUnavailable reports that config validation is required but
	// no validator is wired.
	ErrConfigServiceUnavailable = errors.New("service config validation is not available")
)

// ConfigConflictSummary is the metadata shown for a tracked generated file
// whose on-disk content no longer matches the manifest.
type ConfigConflictSummary struct {
	Active             bool     `json:"active"`
	ResolutionRequired bool     `json:"resolution_required"`
	Reason             string   `json:"reason,omitempty"`
	AllowedResolutions []string `json:"allowed_resolutions,omitempty"`
}

// ConfigStatus is the API-safe metadata for one config file. It never contains
// file contents.
type ConfigStatus struct {
	Path      string                `json:"path"`
	Class     serviceconfig.Class   `json:"class"`
	Generated bool                  `json:"generated"`
	Modified  bool                  `json:"modified"`
	Detached  bool                  `json:"detached"`
	Hash      string                `json:"hash"`
	Size      int64                 `json:"size"`
	Conflict  ConfigConflictSummary `json:"conflict"`
}

// ConfigListing is the GET /config response.
type ConfigListing struct {
	Service string         `json:"service"`
	Files   []ConfigStatus `json:"files"`
}

// ConfigContent is the GET /config/content response. Content is returned only
// for an allowed file.
type ConfigContent struct {
	ConfigStatus
	Content string `json:"content"`
}

// ConfigDiff is the POST /config/diff response.
type ConfigDiff struct {
	Path         string                `json:"path"`
	Diff         string                `json:"diff"`
	CurrentHash  string                `json:"current_hash"`
	ProposedHash string                `json:"proposed_hash"`
	CurrentSize  int64                 `json:"current_size"`
	ProposedSize int64                 `json:"proposed_size"`
	Status       ConfigStatus          `json:"status"`
	Conflict     ConfigConflictSummary `json:"conflict"`
}

// ConfigApplyRequest is the POST /config/apply body.
type ConfigApplyRequest struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Resolution string `json:"resolution,omitempty"`
}

// ConfigResetRequest is the POST /config/reset body.
type ConfigResetRequest struct {
	Path string `json:"path"`
}

// ConfigApplyResult reports a completed apply or reset. Paths are internal
// backup locations, never file contents.
type ConfigApplyResult struct {
	Service           string `json:"service"`
	Path              string `json:"path"`
	Action            string `json:"action"`
	Changed           bool   `json:"changed"`
	Resolution        string `json:"resolution,omitempty"`
	BackupPath        string `json:"backup_path,omitempty"`
	RestoredFrom      string `json:"restored_from,omitempty"`
	Detached          bool   `json:"detached"`
	Hash              string `json:"hash"`
	Size              int64  `json:"size"`
	Active            bool   `json:"active"`
	Live              bool   `json:"live"`
	Reloaded          bool   `json:"reloaded"`
	ValidationPending bool   `json:"validation_pending"`
	RestartRequired   bool   `json:"restart_required"`
	RolledBack        bool   `json:"rolled_back"`
	Message           string `json:"message,omitempty"`
}

// ConfigConflictError carries the diff metadata for a tracked conflict. It
// never contains the current or proposed file bodies as error text; the diff
// itself is returned by the dedicated diff endpoint.
type ConfigConflictError struct {
	Path         string                `json:"path"`
	Reason       string                `json:"reason"`
	CurrentHash  string                `json:"current_hash"`
	ProposedHash string                `json:"proposed_hash"`
	Diff         string                `json:"diff"`
	Conflict     ConfigConflictSummary `json:"conflict"`
}

func (e *ConfigConflictError) Error() string {
	if e == nil {
		return ErrConfigConflict.Error()
	}
	return fmt.Sprintf("%s: %s has tracked modifications; choose keep, use, or merge", ErrConfigConflict, e.Path)
}

// Is reports ErrConfigConflict for errors.Is.
func (e *ConfigConflictError) Is(target error) bool { return target == ErrConfigConflict }

// ConfigApplyFailure reports a validation or reload failure after a write. The
// previous file is restored before this error is returned when RolledBack is
// true.
type ConfigApplyFailure struct {
	Kind          string `json:"operation"`
	Path          string `json:"path"`
	Err           error  `json:"-"`
	BackupPath    string `json:"backup_path,omitempty"`
	RolledBack    bool   `json:"rolled_back"`
	RollbackError error  `json:"-"`
}

func (e *ConfigApplyFailure) Error() string {
	if e == nil {
		return "service config apply failed"
	}
	message := fmt.Sprintf("%s: %s: %s", e.Kind, e.Path, RedactSecrets(fmt.Sprint(e.Err)))
	if e.RolledBack {
		message += "; previous config restored"
	} else if e.RollbackError != nil {
		message += "; rollback failed: " + RedactSecrets(e.RollbackError.Error())
	}
	return message
}

// Unwrap exposes the validation/reload cause.
func (e *ConfigApplyFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is maps the failure kind onto its stable sentinel.
func (e *ConfigApplyFailure) Is(target error) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ConfigFailureValidate:
		return target == ErrConfigValidationFailed
	case ConfigFailureReload:
		return target == ErrConfigReloadFailed
	default:
		return false
	}
}

// ConfigLifecycle is the service-view surface used to decide whether a write is
// live or needs pending validation.
type ConfigLifecycle interface {
	Get(ctx context.Context, id services.ID) (services.View, error)
}

// ConfigValidator is the helper's fixed catalog validation/reload surface.
type ConfigValidator interface {
	Validate(ctx context.Context, id string) error
	Reload(ctx context.Context, id string) error
}

// ConfigEventStore records metadata-only audit events.
type ConfigEventStore interface {
	AddEvent(ctx context.Context, event model.Event) error
}

// AgentConfigAPI defines the agent client subset for service config operations.
type AgentConfigAPI interface {
	ServiceConfig(ctx context.Context, id string) (agentproto.ServiceConfigResponse, error)
	ServiceContent(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error)
	ServiceApply(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error)
	ServiceReset(ctx context.Context, req agentproto.ServiceConfigRequest) error
}

// ConfigDeps configures the service config service.
type ConfigDeps struct {
	Root      string
	Catalog   services.Catalog
	Services  ConfigLifecycle
	Validator ConfigValidator
	Events    ConfigEventStore
	Agent     AgentConfigAPI
	Logger    *slog.Logger
}

// ConfigService implements the service config manifest/read/diff/apply/reset
// workflow. Every user-requested read and write is confined to
// <root>/services/<id>/config.
type ConfigService struct {
	root      string
	catalog   services.Catalog
	services  ConfigLifecycle
	validator ConfigValidator
	events    ConfigEventStore
	agent     AgentConfigAPI
	logger    *slog.Logger

	writeMu sync.Mutex
}

// NewConfigService returns a config service rooted at a devstack home.
func NewConfigService(deps ConfigDeps) (*ConfigService, error) {
	root := strings.TrimSpace(deps.Root)
	if root == "" {
		return nil, errors.New("service config root cannot be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve service config root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ConfigService{
		root:      abs,
		catalog:   deps.Catalog,
		services:  deps.Services,
		validator: deps.Validator,
		events:    deps.Events,
		agent:     deps.Agent,
		logger:    logger,
	}, nil
}

// List returns metadata for every safe, regular file under the service config
// root. File contents are never included.
func (s *ConfigService) List(ctx context.Context, id services.ID) (ConfigListing, error) {
	if err := ctx.Err(); err != nil {
		return ConfigListing{}, err
	}
	if s.agent != nil {
		resp, err := s.agent.ServiceConfig(ctx, string(id))
		if err != nil {
			return ConfigListing{}, err
		}
		files := make([]ConfigStatus, 0, len(resp.Files))
		for _, f := range resp.Files {
			files = append(files, ConfigStatus{
				Path:     f.Path,
				Class:    serviceconfig.Class(f.Class),
				Hash:     f.Hash,
				Modified: f.Modified,
				Detached: f.Detached,
				Conflict: ConfigConflictSummary{
					Active:             false,
					ResolutionRequired: false,
				},
			})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		return ConfigListing{Service: string(id), Files: files}, nil
	}
	_, configRoot, manager, err := s.prepare(ctx, id)
	if err != nil {
		return ConfigListing{}, err
	}

	files := []ConfigStatus{}
	err = filepath.WalkDir(configRoot, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == configRoot {
			return nil
		}
		rel, err := filepath.Rel(configRoot, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			for _, segment := range strings.Split(rel, "/") {
				if deniedConfigName(segment) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if _, err := cleanConfigRel(rel); err != nil {
			return nil
		}
		// The manifest and backup tree are internal state, not user-editable
		// service configuration. The manifest is no longer dot-prefixed, so
		// exclude it explicitly.
		if rel == "config-manifest.json" || rel == ".config-manifest.json" || rel == "backups" || strings.HasPrefix(rel, "backups/") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxConfigFileBytes {
			return nil
		}
		data, err := readConfigFile(full)
		if err != nil {
			if errors.Is(err, ErrConfigTooLarge) || errors.Is(err, ErrConfigBinary) {
				return nil
			}
			return err
		}
		status, err := configStatus(manager, rel, data, info)
		if err != nil {
			return err
		}
		files = append(files, status)
		return nil
	})
	if err != nil {
		return ConfigListing{}, fmt.Errorf("list service config: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return ConfigListing{Service: string(id), Files: files}, nil
}

// Content returns one allowed file's content and metadata.
func (s *ConfigService) Content(ctx context.Context, id services.ID, rel string) (ConfigContent, error) {
	if err := ctx.Err(); err != nil {
		return ConfigContent{}, err
	}
	if s.agent != nil {
		clean, err := cleanConfigRel(rel)
		if err != nil {
			return ConfigContent{}, err
		}
		resp, err := s.agent.ServiceContent(ctx, agentproto.ServiceConfigRequest{
			ID:  string(id),
			Rel: clean,
		})
		if err != nil {
			return ConfigContent{}, err
		}
		return ConfigContent{
			ConfigStatus: ConfigStatus{
				Path: resp.Path,
				Hash: resp.Hash,
				Size: int64(len(resp.Content)),
			},
			Content: resp.Content,
		}, nil
	}
	_, configRoot, manager, err := s.prepare(ctx, id)
	if err != nil {
		return ConfigContent{}, err
	}
	clean, full, info, _, err := s.resolve(configRoot, rel, false)
	if err != nil {
		return ConfigContent{}, err
	}
	data, err := readConfigFile(full)
	if err != nil {
		return ConfigContent{}, err
	}
	status, err := configStatus(manager, clean, data, info)
	if err != nil {
		return ConfigContent{}, err
	}
	return ConfigContent{ConfigStatus: status, Content: string(data)}, nil
}

// Diff returns a unified diff between the current file and proposed content.
// A missing catalog-declared file is treated as empty current content.
func (s *ConfigService) Diff(ctx context.Context, id services.ID, rel, content string) (ConfigDiff, error) {
	if err := ctx.Err(); err != nil {
		return ConfigDiff{}, err
	}
	if s.agent != nil {
		if err := validateConfigContent(content); err != nil {
			return ConfigDiff{}, err
		}
		clean, err := cleanConfigRel(rel)
		if err != nil {
			return ConfigDiff{}, err
		}
		var current []byte
		resp, err := s.agent.ServiceContent(ctx, agentproto.ServiceConfigRequest{
			ID:  string(id),
			Rel: clean,
		})
		if err == nil {
			current = []byte(resp.Content)
		}
		proposed := []byte(content)
		return ConfigDiff{
			Path:         clean,
			Diff:         serviceconfig.UnifiedDiff(clean, current, proposed),
			CurrentHash:  configHash(current),
			ProposedHash: configHash(proposed),
			CurrentSize:  int64(len(current)),
			ProposedSize: int64(len(proposed)),
			Status: ConfigStatus{
				Path: clean,
				Hash: configHash(current),
				Size: int64(len(current)),
			},
		}, nil
	}
	svc, configRoot, manager, err := s.prepare(ctx, id)
	if err != nil {
		return ConfigDiff{}, err
	}
	if err := validateConfigContent(content); err != nil {
		return ConfigDiff{}, err
	}
	clean, err := cleanConfigRel(rel)
	if err != nil {
		return ConfigDiff{}, err
	}
	catalogPath := catalogAllows(svc, clean)
	clean, full, info, exists, err := s.resolve(configRoot, clean, true)
	if err != nil {
		if !catalogPath || !errors.Is(err, ErrConfigNotFound) {
			return ConfigDiff{}, err
		}
		full = ""
		info = nil
		exists = false
	}
	if !exists && !catalogPath {
		return ConfigDiff{}, fmt.Errorf("%w: %s", ErrConfigNotFound, clean)
	}

	var current []byte
	if exists {
		current, err = readConfigFile(full)
		if err != nil {
			return ConfigDiff{}, err
		}
	}
	status, err := configStatus(manager, clean, current, info)
	if err != nil {
		return ConfigDiff{}, err
	}
	proposed := []byte(content)
	return ConfigDiff{
		Path:         clean,
		Diff:         serviceconfig.UnifiedDiff(clean, current, proposed),
		CurrentHash:  configHash(current),
		ProposedHash: configHash(proposed),
		CurrentSize:  int64(len(current)),
		ProposedSize: int64(len(proposed)),
		Status:       status,
		Conflict:     status.Conflict,
	}, nil
}

// Apply writes proposed content atomically. Tracked generated conflicts require
// an explicit keep/use/merge choice. Active services with a catalog validation
// command are validated and reloaded; a failure restores the previous file.
func (s *ConfigService) Apply(ctx context.Context, id services.ID, req ConfigApplyRequest) (ConfigApplyResult, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := ctx.Err(); err != nil {
		return ConfigApplyResult{}, err
	}
	if s.agent != nil {
		if err := validateConfigContent(req.Content); err != nil {
			return ConfigApplyResult{}, err
		}
		clean, err := cleanConfigRel(req.Path)
		if err != nil {
			return ConfigApplyResult{}, err
		}
		resp, err := s.agent.ServiceApply(ctx, agentproto.ServiceConfigRequest{
			ID:      string(id),
			Rel:     clean,
			Content: req.Content,
		})
		if err != nil {
			s.audit(ctx, "config_apply", id, clean, false, err.Error())
			return ConfigApplyResult{}, err
		}
		s.audit(ctx, "config_apply", id, clean, true, "applied config through agent")
		return ConfigApplyResult{
			Service: string(id),
			Path:    resp.Path,
			Action:  "edit",
			Changed: true,
			Hash:    resp.Hash,
			Size:    int64(len(resp.Content)),
			Live:    true,
		}, nil
	}
	svc, configRoot, manager, err := s.prepare(ctx, id)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if err := validateConfigContent(req.Content); err != nil {
		return ConfigApplyResult{}, err
	}

	clean, err := cleanConfigRel(req.Path)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	catalogPath := catalogAllows(svc, clean)
	if catalogPath {
		if err := ensureConfigParents(configRoot, clean); err != nil {
			return ConfigApplyResult{}, err
		}
	}
	clean, full, info, exists, err := s.resolve(configRoot, clean, true)
	if err != nil {
		if !catalogPath && errors.Is(err, ErrConfigNotFound) {
			return ConfigApplyResult{}, fmt.Errorf("%w: %s", ErrConfigPathDenied, clean)
		}
		return ConfigApplyResult{}, err
	}
	if !exists && !catalogPath {
		return ConfigApplyResult{}, fmt.Errorf("%w: %s", ErrConfigPathDenied, clean)
	}

	var current []byte
	if exists {
		current, err = readConfigFile(full)
		if err != nil {
			return ConfigApplyResult{}, err
		}
	}
	status, err := configStatus(manager, clean, current, info)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	resolution := strings.ToLower(strings.TrimSpace(req.Resolution))
	if resolution != "" &&
		resolution != ConfigResolutionKeep &&
		resolution != ConfigResolutionUse &&
		resolution != ConfigResolutionMerge {
		return ConfigApplyResult{}, fmt.Errorf("%w: %q", ErrConfigResolutionInvalid, req.Resolution)
	}
	trackedConflict := status.Conflict.Active
	if trackedConflict {
		if resolution == "" {
			s.audit(ctx, "apply", svc.ID, clean, false, "conflict_unresolved=true")
			return ConfigApplyResult{}, &ConfigConflictError{
				Path:         clean,
				Reason:       status.Conflict.Reason,
				CurrentHash:  status.Hash,
				ProposedHash: configHash([]byte(req.Content)),
				Diff:         serviceconfig.UnifiedDiff(clean, current, []byte(req.Content)),
				Conflict:     status.Conflict,
			}
		}
	} else if resolution != "" {
		return ConfigApplyResult{}, fmt.Errorf("%w: %s is not a tracked conflict", ErrConfigResolutionInvalid, clean)
	}

	if !trackedConflict && exists && bytes.Equal(current, []byte(req.Content)) {
		return ConfigApplyResult{
			Service: string(id),
			Path:    clean,
			Action:  "apply",
			Changed: false,
			Hash:    status.Hash,
			Size:    status.Size,
			Message: "content is unchanged",
		}, nil
	}

	view, err := s.lifecycleView(ctx, svc)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if view.Active && len(svc.ValidateCommand) > 0 && s.validator == nil {
		return ConfigApplyResult{}, fmt.Errorf("%w: no validator is configured", ErrConfigServiceUnavailable)
	}

	previousEntry, previousTracked, err := manager.Entry(clean)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	mode := os.FileMode(0o644)
	if exists {
		mode = info.Mode().Perm()
	}

	if resolution == ConfigResolutionKeep {
		if err := manager.KeepMine(clean); err != nil {
			return ConfigApplyResult{}, fmt.Errorf("detach service config %s: %w", clean, err)
		}
		result := ConfigApplyResult{
			Service:    string(id),
			Path:       clean,
			Action:     "apply",
			Changed:    false,
			Resolution: ConfigResolutionKeep,
			Detached:   true,
			Hash:       status.Hash,
			Size:       status.Size,
			Active:     view.Active,
			Message:    "kept current content and detached the file from generated updates",
		}
		s.audit(ctx, "keep", svc.ID, clean, true, "resolution=keep")
		return result, nil
	}

	var backupPath string
	switch resolution {
	case ConfigResolutionUse:
		backupPath, err = manager.UseGenerated(clean, []byte(req.Content))
	case ConfigResolutionMerge:
		backupPath, err = manager.MergeWithBackup(clean, []byte(req.Content))
	default:
		backupPath, err = manager.WriteUser(clean, []byte(req.Content), mode)
	}
	if err != nil {
		return ConfigApplyResult{}, fmt.Errorf("write service config %s: %w", clean, err)
	}

	detached := status.Detached
	switch resolution {
	case ConfigResolutionUse:
		detached = false
	case ConfigResolutionMerge:
		detached = true
	}
	result := ConfigApplyResult{
		Service:    string(id),
		Path:       clean,
		Action:     "apply",
		Changed:    true,
		Resolution: resolution,
		BackupPath: backupPath,
		Detached:   detached,
		Hash:       configHash([]byte(req.Content)),
		Size:       int64(len(req.Content)),
		Active:     view.Active,
	}
	return s.finalizeWrite(ctx, svc, manager, clean, full, current, exists, mode, previousEntry, previousTracked, result)
}

// Reset restores the newest backup for path. It never deletes an untracked
// custom file. A write to an active service follows the same validation,
// reload, and rollback contract as Apply.
func (s *ConfigService) Reset(ctx context.Context, id services.ID, rel string) (ConfigApplyResult, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := ctx.Err(); err != nil {
		return ConfigApplyResult{}, err
	}
	if s.agent != nil {
		clean, err := cleanConfigRel(rel)
		if err != nil {
			return ConfigApplyResult{}, err
		}
		err = s.agent.ServiceReset(ctx, agentproto.ServiceConfigRequest{
			ID:  string(id),
			Rel: clean,
		})
		if err != nil {
			s.audit(ctx, "config_reset", id, clean, false, err.Error())
			return ConfigApplyResult{}, err
		}
		result := ConfigApplyResult{
			Service: string(id),
			Path:    clean,
			Action:  "reset",
			Changed: true,
		}
		// Reset restores the file but the agent call returns no metadata. Read
		// the restored content back so JSON clients see the same hash/size a
		// normal apply reports.
		restored, readErr := s.agent.ServiceContent(ctx, agentproto.ServiceConfigRequest{
			ID:  string(id),
			Rel: clean,
		})
		if readErr != nil {
			result.Message = "reset succeeded; restored metadata is unavailable: " + readErr.Error()
		} else {
			result.Hash = restored.Hash
			result.Size = int64(len(restored.Content))
		}
		s.audit(ctx, "config_reset", id, clean, true, "reset config through agent")
		return result, nil
	}
	svc, configRoot, manager, err := s.prepare(ctx, id)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	clean, full, info, exists, err := s.resolve(configRoot, rel, false)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if !exists {
		return ConfigApplyResult{}, fmt.Errorf("%w: %s", ErrConfigNotFound, clean)
	}
	current, err := readConfigFile(full)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	status, err := configStatus(manager, clean, current, info)
	if err != nil {
		return ConfigApplyResult{}, err
	}

	source, err := manager.LatestBackup(clean)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if source == "" && status.Modified {
		source, err = manager.LatestBaseline(clean)
		if err != nil {
			return ConfigApplyResult{}, err
		}
	}
	if source == "" {
		if !status.Modified && status.Class != serviceconfig.ClassCustom {
			return ConfigApplyResult{
				Service: string(id),
				Path:    clean,
				Action:  "reset",
				Changed: false,
				Hash:    status.Hash,
				Size:    status.Size,
				Message: "already at the managed baseline",
			}, nil
		}
		s.audit(ctx, "reset", svc.ID, clean, false, "no_backup=true")
		return ConfigApplyResult{}, fmt.Errorf("%w: %s", ErrConfigResetUnavailable, clean)
	}
	restored, err := readConfigFile(source)
	if err != nil {
		return ConfigApplyResult{}, err
	}

	view, err := s.lifecycleView(ctx, svc)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if view.Active && len(svc.ValidateCommand) > 0 && s.validator == nil {
		return ConfigApplyResult{}, fmt.Errorf("%w: no validator is configured", ErrConfigServiceUnavailable)
	}

	previousEntry, previousTracked, err := manager.Entry(clean)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	backupPath, err := manager.BackupReset(clean)
	if err != nil {
		return ConfigApplyResult{}, fmt.Errorf("back up service config %s before reset: %w", clean, err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := manager.RestoreFile(clean, restored, mode); err != nil {
		return ConfigApplyResult{}, fmt.Errorf("restore service config %s: %w", clean, err)
	}

	result := ConfigApplyResult{
		Service:      string(id),
		Path:         clean,
		Action:       "reset",
		Changed:      true,
		Resolution:   "backup",
		BackupPath:   backupPath,
		RestoredFrom: source,
		Hash:         configHash(restored),
		Size:         int64(len(restored)),
		Active:       view.Active,
	}
	return s.finalizeWrite(ctx, svc, manager, clean, full, current, exists, mode, previousEntry, previousTracked, result)
}

func (s *ConfigService) finalizeWrite(
	ctx context.Context,
	svc services.Service,
	manager *serviceconfig.Manager,
	rel string,
	full string,
	previous []byte,
	previousExists bool,
	mode os.FileMode,
	previousEntry serviceconfig.ManifestEntry,
	previousTracked bool,
	result ConfigApplyResult,
) (ConfigApplyResult, error) {
	if !result.Active {
		result.ValidationPending = true
		result.Live = false
		s.audit(ctx, result.Action, svc.ID, rel, true,
			"validation_pending=true active=false")
		return result, nil
	}
	if len(svc.ValidateCommand) == 0 {
		result.ValidationPending = true
		result.RestartRequired = true
		result.Live = false
		s.audit(ctx, result.Action, svc.ID, rel, true,
			"validation_pending=true restart_required=true")
		return result, nil
	}

	if err := s.validator.Validate(ctx, string(svc.ID)); err != nil {
		rollbackErr := s.rollback(manager, rel, full, previous, previousExists, mode, previousEntry, previousTracked)
		failure := &ConfigApplyFailure{
			Kind:          ConfigFailureValidate,
			Path:          rel,
			Err:           err,
			BackupPath:    result.BackupPath,
			RolledBack:    rollbackErr == nil,
			RollbackError: rollbackErr,
		}
		s.audit(ctx, result.Action, svc.ID, rel, false, "validation=failed rolled_back="+boolString(rollbackErr == nil))
		return result, failure
	}
	if err := s.validator.Reload(ctx, string(svc.ID)); err != nil {
		rollbackErr := s.rollback(manager, rel, full, previous, previousExists, mode, previousEntry, previousTracked)
		var reloadRollbackErr error
		if rollbackErr == nil {
			reloadRollbackErr = s.validator.Reload(ctx, string(svc.ID))
		}
		failure := &ConfigApplyFailure{
			Kind:          ConfigFailureReload,
			Path:          rel,
			Err:           err,
			BackupPath:    result.BackupPath,
			RolledBack:    rollbackErr == nil,
			RollbackError: errors.Join(rollbackErr, reloadRollbackErr),
		}
		s.audit(ctx, result.Action, svc.ID, rel, false, "reload=failed rolled_back="+boolString(rollbackErr == nil))
		return result, failure
	}

	result.Live = true
	result.Reloaded = true
	result.ValidationPending = false
	s.audit(ctx, result.Action, svc.ID, rel, true, "validated=true reloaded=true")
	return result, nil
}

// rollback restores the previous file and manifest entry after a failed
// validation or reload.
func (s *ConfigService) rollback(
	manager *serviceconfig.Manager,
	rel string,
	full string,
	previous []byte,
	previousExists bool,
	mode os.FileMode,
	previousEntry serviceconfig.ManifestEntry,
	previousTracked bool,
) error {
	var fileErr error
	if previousExists {
		fileErr = manager.RestoreFile(rel, previous, mode)
	} else if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		fileErr = fmt.Errorf("remove new config %s: %w", rel, err)
	}
	entryErr := manager.RestoreEntry(rel, previousEntry, previousTracked)
	return errors.Join(fileErr, entryErr)
}

func (s *ConfigService) lifecycleView(ctx context.Context, svc services.Service) (services.View, error) {
	if s.services == nil {
		return services.View{ID: svc.ID, Installed: true}, nil
	}
	view, err := s.services.Get(ctx, svc.ID)
	if err != nil {
		return services.View{}, fmt.Errorf("read service %s state: %w", svc.ID, err)
	}
	return view, nil
}

func (s *ConfigService) prepare(ctx context.Context, id services.ID) (services.Service, string, *serviceconfig.Manager, error) {
	svc, ok := s.catalog.Get(id)
	if !ok {
		return services.Service{}, "", nil, fmt.Errorf("%w: %q", services.ErrNotFound, id)
	}
	serviceRoot := filepath.Join(s.root, "services", string(id))
	if err := checkServiceScaffold(serviceRoot); err != nil {
		return services.Service{}, "", nil, err
	}
	view, err := s.lifecycleView(ctx, svc)
	if err != nil {
		return services.Service{}, "", nil, err
	}
	if !view.Installed {
		return services.Service{}, "", nil, ErrConfigNotInstalled
	}
	configRoot := filepath.Join(serviceRoot, "config")
	if err := serviceconfig.MigrateManifest(serviceRoot, configRoot, "config/"); err != nil {
		return services.Service{}, "", nil, fmt.Errorf("migrate service config manifest: %w", err)
	}
	manager, err := serviceconfig.New(configRoot)
	if err != nil {
		return services.Service{}, "", nil, fmt.Errorf("open service config manager: %w", err)
	}
	return svc, configRoot, manager, nil
}

func checkServiceScaffold(serviceRoot string) error {
	servicesRoot := filepath.Dir(serviceRoot)
	for _, dir := range []string{servicesRoot, serviceRoot} {
		info, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrConfigNotInstalled
			}
			return fmt.Errorf("inspect service scaffold %s: %w", dir, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrConfigNotInstalled
		}
	}
	for _, name := range []string{"config", "data", "logs"} {
		info, err := os.Lstat(filepath.Join(serviceRoot, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrConfigNotInstalled
			}
			return fmt.Errorf("inspect service scaffold %s: %w", name, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrConfigNotInstalled
		}
	}
	return nil
}

// ensureConfigParents creates missing catalog-declared parent directories.
// Every existing component must already be a real directory, never a symlink.
func ensureConfigParents(configRoot, rel string) error {
	parts := strings.Split(rel, "/")
	current := configRoot
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("%w: %s has a non-directory parent", ErrConfigPathDenied, rel)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect service config parent: %w", err)
		}
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create service config directory: %w", err)
		}
	}
	return nil
}

// resolve validates rel against the config policy, rejects symlinks and
// non-regular files, and returns the cleaned slash path. A missing final
// component is allowed only when allowMissing is true; intermediate
// directories must already exist.
func (s *ConfigService) resolve(configRoot, rel string, allowMissing bool) (string, string, os.FileInfo, bool, error) {
	clean, err := cleanConfigRel(rel)
	if err != nil {
		return "", "", nil, false, err
	}
	full := filepath.Join(configRoot, filepath.FromSlash(clean))
	if !pathWithin(configRoot, full) {
		return "", "", nil, false, fmt.Errorf("%w: %s escapes the config root", ErrConfigPathInvalid, rel)
	}

	current := configRoot
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if i == len(parts)-1 && allowMissing {
					return clean, full, nil, false, nil
				}
				return "", "", nil, false, fmt.Errorf("%w: %s", ErrConfigNotFound, clean)
			}
			return "", "", nil, false, fmt.Errorf("inspect service config %s: %w", clean, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", nil, false, fmt.Errorf("%w: %s is a symlink", ErrConfigPathDenied, clean)
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return "", "", nil, false, fmt.Errorf("%w: %s has a non-directory parent", ErrConfigPathInvalid, clean)
			}
			continue
		}
		if info.IsDir() {
			return "", "", nil, false, fmt.Errorf("%w: %s is a directory", ErrConfigPathInvalid, clean)
		}
		if !info.Mode().IsRegular() {
			return "", "", nil, false, fmt.Errorf("%w: %s is not a regular file", ErrConfigPathDenied, clean)
		}
		return clean, full, info, true, nil
	}
	return "", "", nil, false, fmt.Errorf("%w: %s", ErrConfigPathInvalid, rel)
}

func cleanConfigRel(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("%w: path is required", ErrConfigPathInvalid)
	}
	if !utf8.ValidString(rel) || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("%w: path contains invalid characters", ErrConfigPathInvalid)
	}
	if strings.ContainsRune(rel, '\\') {
		return "", fmt.Errorf("%w: backslashes are not allowed", ErrConfigPathInvalid)
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.HasPrefix(rel, "~") {
		return "", fmt.Errorf("%w: path must be relative", ErrConfigPathInvalid)
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: path traversal is not allowed", ErrConfigPathInvalid)
		}
		if deniedConfigName(segment) {
			return "", fmt.Errorf("%w: %s is reserved or sensitive", ErrConfigPathDenied, rel)
		}
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: path escapes the config root", ErrConfigPathInvalid)
	}
	// Performance-managed includes are generated by the performance apply/reset
	// flow, not this editor. Exposing them here would let an edit be silently
	// replaced by the next profile apply.
	if cleaned == "conf.d/performance.conf" || strings.HasPrefix(cleaned, "conf.d/performance/") {
		return "", fmt.Errorf("%w: %s is reserved for the performance profile", ErrConfigPathDenied, rel)
	}
	return cleaned, nil
}

func deniedConfigName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, ".") {
		return true
	}
	switch lower {
	case "backup", "backups", "secret", "secrets", "cert", "certs",
		"certificate", "certificates", "key", "keys", "private",
		"data", "logs", "socket", "sockets", "sock":
		return true
	}
	for _, marker := range []string{
		"secret", "credential", "password", "passwd", "api_key", "apikey",
		"private_key", "access_key", "token",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	switch strings.ToLower(filepath.Ext(lower)) {
	case ".pem", ".key", ".crt", ".cer", ".csr", ".p12", ".pfx", ".jks",
		".keystore", ".sock", ".socket", ".bak", ".backup", ".tmp", ".swp",
		".old", ".orig":
		return true
	}
	return strings.HasSuffix(lower, "~")
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		relative != "."
}

func catalogAllows(svc services.Service, rel string) bool {
	for _, mount := range svc.Config {
		area := mount.Area
		if area == "" {
			area = services.MountService
		}
		if area != services.MountService {
			continue
		}
		sub := filepath.ToSlash(filepath.Clean(mount.HostSubdir))
		switch {
		case sub == "." || sub == "":
			continue
		case sub == "config":
			// A whole-directory mount permits existing safe files, but not
			// creating arbitrary new files through the editor.
			continue
		case !strings.HasPrefix(sub, "config/"):
			continue
		}
		base := strings.TrimPrefix(sub, "config/")
		if base == "" {
			return true
		}
		// Directory mounts are prefixes; file mounts are exact. The catalog
		// has no explicit kind, so use the same convention as the Docker
		// config tree: ".d" directories and extensionless directories.
		if strings.HasSuffix(base, ".d") || filepath.Ext(base) == "" {
			if rel == base {
				// The base itself is a directory mount target; never create a
				// regular file where the service expects a directory.
				return false
			}
			if strings.HasPrefix(rel, base+"/") {
				return true
			}
			continue
		}
		if rel == base {
			return true
		}
	}
	return false
}

func configStatus(manager *serviceconfig.Manager, rel string, data []byte, info os.FileInfo) (ConfigStatus, error) {
	status, err := manager.Status(rel)
	if err != nil {
		return ConfigStatus{}, fmt.Errorf("inspect service config %s: %w", rel, err)
	}
	size := int64(len(data))
	if info != nil {
		size = info.Size()
	}
	modified := status.Modified
	if status.Class == serviceconfig.ClassCustom {
		// Manager.Status marks an untracked existing file as "modified" because
		// it has content. For an intentionally custom file that is not drift,
		// and showing a conflict would be misleading.
		modified = false
	}
	out := ConfigStatus{
		Path:      rel,
		Class:     status.Class,
		Generated: status.Generated,
		Modified:  modified,
		Detached:  status.Detached,
		Hash:      configHash(data),
		Size:      size,
		Conflict:  conflictSummary(status),
	}
	return out, nil
}

func conflictSummary(status serviceconfig.Status) ConfigConflictSummary {
	if !status.Generated || status.Detached || !status.Modified {
		return ConfigConflictSummary{Active: false}
	}
	return ConfigConflictSummary{
		Active:             true,
		ResolutionRequired: true,
		Reason:             "tracked generated file has user modifications",
		AllowedResolutions: []string{
			ConfigResolutionKeep,
			ConfigResolutionUse,
			ConfigResolutionMerge,
		},
	}
}

func validateConfigContent(content string) error {
	if len(content) > MaxConfigFileBytes {
		return ErrConfigTooLarge
	}
	if strings.ContainsRune(content, 0) || !utf8.ValidString(content) {
		return ErrConfigBinary
	}
	return nil
}

func readConfigFile(full string) ([]byte, error) {
	file, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrConfigNotFound
		}
		return nil, fmt.Errorf("read service config: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, MaxConfigFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read service config: %w", err)
	}
	if len(data) > MaxConfigFileBytes {
		return nil, ErrConfigTooLarge
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil, ErrConfigBinary
	}
	return data, nil
}

func configHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// audit records a metadata-only config action. File contents and credential
// values are never included in logs or events.
func (s *ConfigService) audit(ctx context.Context, action string, id services.ID, rel string, success bool, detail string) {
	if s.logger != nil {
		s.logger.Info("service config access",
			"audit", "service.config",
			"service", string(id),
			"action", action,
			"path", rel,
			"success", success,
			"detail", detail,
		)
	}
	if s.events == nil {
		return
	}
	message := fmt.Sprintf("service=%s action=%s path=%s success=%t", id, action, rel, success)
	if detail != "" {
		message += " " + detail
	}
	if err := s.events.AddEvent(ctx, model.Event{
		Type:    "service.config." + action,
		Message: message,
	}); err != nil && s.logger != nil {
		s.logger.Warn("service config audit event failed", "service", string(id), "error", err)
	}
}
