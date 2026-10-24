package sites

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// ErrRuntimeInUse reports that an installed PHP build is still pinned by a
// site and cannot be removed safely.
var ErrRuntimeInUse = errors.New("sites: PHP runtime is in use")

// PHPChange is a partial PHP configuration change. Empty Version and Build
// keep the current row; nil maps and a nil raw pointer keep the current
// overrides, while non-nil values (including empty ones) replace them.
type PHPChange struct {
	Version    string
	Build      string
	Extensions map[string]bool
	Ini        map[string]string
	IniRaw     *string
}

// PHPInfo is the effective PHP configuration of one site: the persisted row,
// the pinned build, version defaults, resolved extensions, and the rendered
// configuration files.
type PHPInfo struct {
	Site                model.Site                  `json:"site"`
	Build               model.RuntimeBuild          `json:"build"`
	Defaults            model.PHPDefaults           `json:"defaults"`
	Extensions          []catalog.ResolvedExtension `json:"extensions"`
	AvailableExtensions []catalog.Extension         `json:"available_extensions"`
	Builtins            []string                    `json:"builtins"`
	Ini                 map[string]string           `json:"ini"`
	IniRaw              string                      `json:"ini_raw"`
	GeneratedIni        string                      `json:"generated_ini"`
	FPMConfig           string                      `json:"fpm_config"`
	ConfigHash          string                      `json:"config_hash"`
}

// PHPMigrationRequest asks to move every site on FromVersion to a target
// build. ToBuild is optional; ToVersion selects the newest installed build for
// that line when ToBuild is empty.
type PHPMigrationRequest struct {
	FromVersion string
	ToVersion   string
	ToBuild     string
}

// PHPMigrationResult reports one site's migration or defaults-apply outcome.
type PHPMigrationResult struct {
	Site  string `json:"site"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// PHPDefaultsResult reports the saved defaults and the per-site outcome of
// applying them to running sites on the line.
type PHPDefaultsResult struct {
	Defaults model.PHPDefaults    `json:"defaults"`
	Sites    []PHPMigrationResult `json:"sites"`
}

// RuntimeInUseError names the build and the sites that still pin it.
type RuntimeInUseError struct {
	BuildID string
	Sites   []string
}

// Error implements error.
func (e *RuntimeInUseError) Error() string {
	if e == nil {
		return ErrRuntimeInUse.Error()
	}
	if len(e.Sites) == 0 {
		return fmt.Sprintf("sites: PHP runtime %q is in use", e.BuildID)
	}
	return fmt.Sprintf("sites: PHP runtime %q is in use by: %s", e.BuildID, strings.Join(e.Sites, ", "))
}

// Unwrap makes every in-use failure match ErrRuntimeInUse.
func (e *RuntimeInUseError) Unwrap() error { return ErrRuntimeInUse }

// ApplyPHP merges a PHP configuration change with the current row, validates
// it, and swaps a running site onto a newly staged FPM master. A failure
// before the new master is published leaves the old row, route, vhost, and
// master untouched. A stopped site only writes and validates configuration.
func (s *Service) ApplyPHP(ctx context.Context, ref string, change PHPChange) (model.Site, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return model.Site{}, errors.New("sites: missing dependencies: store")
	}
	if s.deps.PHP == nil {
		return model.Site{}, errors.New("sites: missing dependencies: PHP validator")
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return model.Site{}, err
	}
	// A stopped site needs only configuration validation and the store. A
	// running apply also publishes routes and vhosts and stages a master.
	if site.Status == model.StatusRunning {
		if err := s.checkDeps(false, false); err != nil {
			return model.Site{}, err
		}
		if err := s.checkPHPBackend(); err != nil {
			return model.Site{}, err
		}
	}
	return s.applyPHPChangeLocked(ctx, site, change)
}

// applyPHPChangeLocked performs one staged apply. The caller must hold s.mu.
func (s *Service) applyPHPChangeLocked(ctx context.Context, current model.Site, change PHPChange) (model.Site, error) {
	if current.Kind != model.SitePHP {
		return current, fmt.Errorf("sites: site %q is not a PHP site", current.Name)
	}
	running := current.Status == model.StatusRunning
	if running {
		if err := s.checkPHPBackend(); err != nil {
			return current, err
		}
	}

	next, build, err := s.mergePHPChangeLocked(ctx, current, change)
	if err != nil {
		return current, err
	}
	if _, err := s.phpSiteDir(current.ID); err != nil {
		return current, err
	}

	oldHash, oldPort := current.PHPConfigHash, current.FastCGIPort

	// A running apply first probes the merged configuration against the port
	// the current master owns. A byte-identical render means only row fields
	// changed (for example a build alias with the same install path), so no
	// second master is needed.
	if running && oldPort != 0 {
		probe := next
		probe.FastCGIPort = oldPort
		files, err := s.composePHPFilesLocked(ctx, probe, build)
		if err != nil {
			return current, err
		}
		if files.Hash == oldHash {
			next.PHPConfigHash = oldHash
			return s.persistPHPRowOnlyLocked(ctx, current, next)
		}
	}

	newPort := current.FastCGIPort
	allocated := 0
	if s.deps.Ports == nil && (running || newPort == 0) {
		return current, errors.New("sites: missing dependencies: port allocator")
	}
	if running {
		port, err := s.deps.Ports.Allocate(domain.PortFastCGI)
		if err != nil {
			return current, fmt.Errorf("sites: allocate FastCGI port for %q: %w", current.Name, err)
		}
		newPort, allocated = port, port
	} else if newPort == 0 {
		port, err := s.deps.Ports.Allocate(domain.PortFastCGI)
		if err != nil {
			return current, fmt.Errorf("sites: allocate FastCGI port for %q: %w", current.Name, err)
		}
		newPort, allocated = port, port
	}
	next.FastCGIPort = newPort
	releaseNewPort := func() {
		if allocated != 0 && allocated != oldPort {
			s.deps.Ports.Release(allocated)
		}
	}

	files, err := s.composePHPFilesLocked(ctx, next, build)
	if err != nil {
		releaseNewPort()
		return current, err
	}
	if files.Hash == oldHash {
		next.PHPConfigHash = oldHash
		releaseNewPort()
		return s.persistPHPRowOnlyLocked(ctx, current, next)
	}
	next.PHPConfigHash = files.Hash

	generationDir, err := s.phpGenerationDir(next.ID, files.Hash)
	if err != nil {
		releaseNewPort()
		return current, err
	}
	preexisting := phpGenerationComplete(generationDir)
	written, err := s.writePHPFilesLocked(next.ID, files)
	if err != nil {
		releaseNewPort()
		if !preexisting {
			_ = os.RemoveAll(generationDir)
		}
		return current, err
	}

	removeNewGeneration := func() {
		if !preexisting {
			_ = os.RemoveAll(written)
		}
	}
	// A discard must run even when the caller's context was canceled, or a
	// staged master can outlive the request that failed and keep listening on a
	// port the allocator has already handed back.
	stopNewGeneration := func() error {
		if !running {
			return nil
		}
		return s.stopPHPGenerationForCleanup(ctx, next.ID, files.Hash)
	}
	// discardNewGeneration removes the generation and releases its port only
	// after the staged master stopped successfully. On a failed stop both are
	// retained and the error is joined so the leak is visible.
	discardNewGeneration := func(cause error) error {
		if err := stopNewGeneration(); err != nil {
			return errors.Join(cause,
				fmt.Errorf("sites: stop staged PHP generation for %q: %w", next.Name, err))
		}
		removeNewGeneration()
		releaseNewPort()
		return cause
	}

	if err := s.validatePHPFilesLocked(ctx, next, build, written); err != nil {
		return current, discardNewGeneration(err)
	}

	if !running {
		next.UpdatedAt = s.currentTime()
		if err := s.deps.Store.UpdateSite(ctx, next); err != nil {
			removeNewGeneration()
			releaseNewPort()
			return current, fmt.Errorf("sites: update %q: %w", next.Name, err)
		}
		if err := s.saveAppliedPHPConfigLocked(ctx, next); err != nil {
			_ = s.deps.Store.UpdateSite(ctx, current)
			removeNewGeneration()
			releaseNewPort()
			return current, fmt.Errorf("sites: save PHP configuration for %q: %w", next.Name, err)
		}
		s.commitPHPFilesBestEffortLocked(ctx, next)
		s.prunePHPGenerationsLocked(ctx, next, files.Hash, oldHash)
		return next, nil
	}

	spec, err := s.phpSiteSpecFromBuildLocked(next, build)
	if err != nil {
		return current, discardNewGeneration(err)
	}
	if _, err := s.deps.PHPBackend.PreparePHP(ctx, next.ID, files.Hash, spec); err != nil {
		return current, discardNewGeneration(fmt.Errorf("sites: prepare %q: %w", next.Name, err))
	}
	if _, err := s.deps.PHPBackend.StartPHP(ctx, next.ID, files.Hash); err != nil {
		return current, discardNewGeneration(fmt.Errorf("sites: start %q: %w", next.Name, err))
	}

	next.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, next); err != nil {
		return current, discardNewGeneration(fmt.Errorf("sites: update %q: %w", next.Name, err))
	}
	if err := s.saveAppliedPHPConfigLocked(ctx, next); err != nil {
		cause := discardNewGeneration(fmt.Errorf("sites: save PHP configuration for %q: %w", next.Name, err))
		restoreErr := s.restorePHPApplyLocked(ctx, current, false)
		return current, errors.Join(cause, restoreErr)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		cause := discardNewGeneration(fmt.Errorf("sites: publish routes for %q: %w", next.Name, err))
		restoreErr := s.restorePHPApplyLocked(ctx, current, true)
		return current, errors.Join(cause, restoreErr)
	}
	if err := s.syncVhostsLocked(ctx, ""); err != nil {
		cause := discardNewGeneration(fmt.Errorf("sites: publish vhost for %q: %w", next.Name, err))
		restoreErr := s.restorePHPApplyLocked(ctx, current, true)
		return current, errors.Join(cause, restoreErr)
	}

	s.commitPHPFilesBestEffortLocked(ctx, next)
	drained := s.drainPHPGenerationsLocked(ctx, next, files.Hash, oldHash)
	if drained {
		if oldPort != 0 && oldPort != newPort {
			s.deps.Ports.Release(oldPort)
		}
		// Pruning is unsafe after a failed stop: a directory that is not the
		// current or previous row hash may still back the live master.
		s.prunePHPGenerationsLocked(ctx, next, files.Hash, oldHash)
	}
	return next, nil
}

// persistPHPRowOnlyLocked records merged row fields whose rendered config is
// identical to the applied generation. No master is started or stopped.
func (s *Service) persistPHPRowOnlyLocked(ctx context.Context, current, next model.Site) (model.Site, error) {
	next.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, next); err != nil {
		return current, fmt.Errorf("sites: update %q: %w", next.Name, err)
	}
	if err := s.saveAppliedPHPConfigLocked(ctx, next); err != nil {
		_ = s.deps.Store.UpdateSite(ctx, current)
		return current, fmt.Errorf("sites: save PHP configuration for %q: %w", next.Name, err)
	}
	s.commitPHPFilesBestEffortLocked(ctx, next)
	return next, nil
}

// restorePHPApplyLocked restores the row and snapshot after a failed publish
// and, when requested, republishes routes and vhosts from the restored row.
func (s *Service) restorePHPApplyLocked(ctx context.Context, previous model.Site, republish bool) error {
	var errs []error
	if err := s.deps.Store.UpdateSite(ctx, previous); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore row %q: %w", previous.Name, err))
	}
	if err := s.saveAppliedPHPConfigLocked(ctx, previous); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore PHP configuration for %q: %w", previous.Name, err))
	}
	if republish {
		if err := s.rebuildRoutesLocked(ctx); err != nil {
			errs = append(errs, fmt.Errorf("rollback: publish routes for %q: %w", previous.Name, err))
		}
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			errs = append(errs, fmt.Errorf("rollback: publish vhost for %q: %w", previous.Name, err))
		}
	}
	return errors.Join(errs...)
}

// drainPHPGenerationsLocked stops the default generation and the previous
// hash generation after the new master is live. It reports whether every stop
// succeeded; a failed stop keeps the old port reserved.
func (s *Service) drainPHPGenerationsLocked(ctx context.Context, site model.Site, newHash, oldHash string) bool {
	generations := []string{phpDefaultGeneration}
	if oldHash != "" && oldHash != newHash {
		generations = append(generations, oldHash)
	}
	stopped := true
	for _, generation := range generations {
		if err := s.deps.PHPBackend.StopPHP(ctx, site.ID, generation); err != nil {
			stopped = false
			s.emitEvent(ctx, site.ID, "site.error",
				fmt.Sprintf("site %s: stop drained PHP generation %s: %v", site.Name, generation, err))
		}
	}
	return stopped
}

// prunePHPGenerationsLocked removes hash-named generation directories other
// than the current and previous ones. Stable copies and unrelated site files
// are never touched.
func (s *Service) prunePHPGenerationsLocked(ctx context.Context, site model.Site, keep ...string) {
	dir, err := s.phpSiteDir(site.ID)
	if err != nil {
		s.emitEvent(ctx, site.ID, "site.error", fmt.Sprintf("site %s: prune PHP generations: %v", site.Name, err))
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.emitEvent(ctx, site.ID, "site.error", fmt.Sprintf("site %s: prune PHP generations: %v", site.Name, err))
		}
		return
	}
	protected := make(map[string]bool, len(keep))
	for _, hash := range keep {
		if hash != "" {
			protected[hash] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != 64 || protected[name] {
			continue
		}
		if _, err := hex.DecodeString(name); err != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			s.emitEvent(ctx, site.ID, "site.error",
				fmt.Sprintf("site %s: prune PHP generation %s: %v", site.Name, name, err))
		}
	}
}

// mergePHPChangeLocked overlays a change on the current row and resolves the
// target installed build without ever installing anything.
func (s *Service) mergePHPChangeLocked(ctx context.Context, current model.Site, change PHPChange) (model.Site, model.RuntimeBuild, error) {
	next := current
	targetVersion := strings.TrimSpace(change.Version)
	if targetVersion == "" {
		targetVersion = strings.TrimSpace(current.PHPVersion)
	}
	buildID := strings.TrimSpace(change.Build)
	if buildID == "" && targetVersion == strings.TrimSpace(current.PHPVersion) {
		buildID = strings.TrimSpace(current.PHPBuild)
	}
	if targetVersion == "" {
		if buildID == "" {
			return current, model.RuntimeBuild{}, fmt.Errorf("sites: PHP site %q has no build: %w", current.Name, ErrRuntimeMissing)
		}
		line, err := s.phpLineForBuildLocked(ctx, buildID)
		if err != nil {
			return current, model.RuntimeBuild{}, err
		}
		targetVersion = line
	}
	build, err := s.resolvePHPBuildLocked(ctx, targetVersion, buildID)
	if err != nil {
		return current, model.RuntimeBuild{}, err
	}
	next.PHPVersion = targetVersion
	next.PHPBuild = build.ID
	if change.Extensions != nil {
		next.PHPExtensions = cloneBoolMap(change.Extensions)
	}
	if change.Ini != nil {
		next.PHPIni = cloneStringMap(change.Ini)
	}
	if change.IniRaw != nil {
		next.PHPIniRaw = *change.IniRaw
	}
	// A stale override can become a built-in when the target line changes
	// (opcache is built into 8.5). Drop those overrides instead of failing the
	// whole apply with ErrInvalidExtension.
	cleaned, dropped, err := s.sanitizePHPOverridesLocked(targetVersion, next.PHPExtensions)
	if err != nil {
		return current, model.RuntimeBuild{}, err
	}
	next.PHPExtensions = cleaned
	if len(dropped) > 0 {
		s.emitEvent(ctx, current.ID, "site.updated", fmt.Sprintf(
			"site %s: dropped extension overrides built into PHP %s: %s",
			current.Name, targetVersion, strings.Join(dropped, ", ")))
	}
	return next, build, nil
}

// phpLineForBuildLocked derives the PHP line from an installed build row.
func (s *Service) phpLineForBuildLocked(ctx context.Context, buildID string) (string, error) {
	build, err := s.deps.Store.GetRuntime(ctx, buildID)
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("sites: PHP build %q is not installed: %w", buildID, ErrRuntimeMissing)
	}
	if err != nil {
		return "", fmt.Errorf("sites: read PHP build %q: %w", buildID, err)
	}
	return catalog.LineOf(build.Version), nil
}

// PHPInfo returns the site's effective PHP configuration.
func (s *Service) PHPInfo(ctx context.Context, ref string) (PHPInfo, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return PHPInfo{}, errors.New("sites: missing dependencies: store")
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return PHPInfo{}, err
	}
	if site.Kind != model.SitePHP {
		return PHPInfo{}, fmt.Errorf("sites: site %q is not a PHP site", site.Name)
	}
	build, err := s.phpBuildForSiteLocked(ctx, site)
	if err != nil {
		return PHPInfo{}, err
	}
	version := strings.TrimSpace(site.PHPVersion)
	if version == "" {
		version = catalog.LineOf(build.Version)
	}
	defaults, err := s.phpDefaultsLocked(ctx, version)
	if err != nil {
		return PHPInfo{}, err
	}
	manifest, err := s.phpCatalog()
	if err != nil {
		return PHPInfo{}, err
	}
	overrides, _, err := s.sanitizePHPOverridesLocked(version, site.PHPExtensions)
	if err != nil {
		return PHPInfo{}, err
	}
	site.PHPExtensions = overrides
	resolved, err := manifest.ResolveWithDefaults(version, defaults.Extensions, overrides)
	if err != nil {
		return PHPInfo{}, fmt.Errorf("sites: PHP extensions for %q: %w", site.Name, err)
	}
	builtins := manifest.BuiltinsFor(version)
	builtinSet := make(map[string]bool, len(builtins))
	for _, name := range builtins {
		builtinSet[name] = true
	}
	available := make([]catalog.Extension, 0, len(manifest.Extensions))
	for _, extension := range manifest.Extensions {
		if versionSupported(extension.Versions, version) && !builtinSet[extension.Name] {
			available = append(available, extension)
		}
	}
	sort.Slice(available, func(i, j int) bool { return available[i].Name < available[j].Name })

	info := PHPInfo{
		Site:                site,
		Build:               build,
		Defaults:            defaults,
		Extensions:          resolved,
		AvailableExtensions: available,
		Builtins:            builtins,
		Ini:                 cloneStringMap(site.PHPIni),
		IniRaw:              site.PHPIniRaw,
	}
	if info.Ini == nil {
		info.Ini = map[string]string{}
	}
	if site.PHPConfigHash != "" {
		generationDir, err := s.phpGenerationDir(site.ID, site.PHPConfigHash)
		if err != nil {
			return PHPInfo{}, err
		}
		if phpGenerationComplete(generationDir) {
			ini, err := os.ReadFile(filepath.Join(generationDir, phpIniFile))
			if err != nil {
				return PHPInfo{}, fmt.Errorf("sites: read PHP %s for %q: %w", phpIniFile, site.Name, err)
			}
			fpm, err := os.ReadFile(filepath.Join(generationDir, phpFPMFile))
			if err != nil {
				return PHPInfo{}, fmt.Errorf("sites: read PHP %s for %q: %w", phpFPMFile, site.Name, err)
			}
			info.GeneratedIni = string(ini)
			info.FPMConfig = string(fpm)
			info.ConfigHash = site.PHPConfigHash
			return info, nil
		}
	}

	renderedSite := site
	allocated := 0
	if renderedSite.FastCGIPort == 0 {
		if s.deps.Ports == nil {
			return PHPInfo{}, errors.New("sites: missing dependencies: port allocator")
		}
		port, err := s.deps.Ports.Allocate(domain.PortFastCGI)
		if err != nil {
			return PHPInfo{}, fmt.Errorf("sites: allocate FastCGI port for inspection: %w", err)
		}
		renderedSite.FastCGIPort = port
		allocated = port
	}
	if allocated != 0 {
		defer s.deps.Ports.Release(allocated)
	}
	files, err := s.composePHPFilesLocked(ctx, renderedSite, build)
	if err != nil {
		return PHPInfo{}, err
	}
	info.GeneratedIni = string(files.Ini)
	info.FPMConfig = string(files.FPM)
	info.ConfigHash = files.Hash
	return info, nil
}

// PHPDefaults returns the editable defaults for a version line, seeding the
// store from the embedded manifest on first use.
func (s *Service) PHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return model.PHPDefaults{}, errors.New("sites: missing dependencies: store")
	}
	version = strings.TrimSpace(version)
	if !supportedPHPLine(version) {
		return model.PHPDefaults{}, fmt.Errorf("sites: unsupported PHP version %q (want 8.2, 8.3, 8.4, or 8.5)", version)
	}
	return s.phpDefaultsLocked(ctx, version)
}

// SavePHPDefaults merges a payload with the embedded baseline, saves it, and
// applies it to every running PHP site on the line independently.
func (s *Service) SavePHPDefaults(ctx context.Context, d model.PHPDefaults) (PHPDefaultsResult, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return PHPDefaultsResult{}, errors.New("sites: missing dependencies: store")
	}
	if s.deps.PHP == nil {
		return PHPDefaultsResult{}, errors.New("sites: missing dependencies: PHP validator")
	}
	version := strings.TrimSpace(d.Version)
	if !supportedPHPLine(version) {
		return PHPDefaultsResult{}, fmt.Errorf("sites: unsupported PHP version %q (want 8.2, 8.3, 8.4, or 8.5)", version)
	}
	manifest, err := s.phpCatalog()
	if err != nil {
		return PHPDefaultsResult{}, err
	}
	baseline, ok := manifest.DefaultsFor(version)
	if !ok {
		return PHPDefaultsResult{}, fmt.Errorf("sites: no PHP defaults for version %q", version)
	}
	merged := model.PHPDefaults{
		Version:    version,
		Ini:        baseline.Ini,
		Extensions: baseline.Extensions,
	}
	if merged.Ini == nil {
		merged.Ini = map[string]string{}
	}
	if merged.Extensions == nil {
		merged.Extensions = map[string]bool{}
	}
	for key, value := range d.Ini {
		merged.Ini[key] = value
	}
	for key, value := range d.Extensions {
		merged.Extensions[key] = value
	}
	// A defaults row or client payload must never toggle a module that the
	// target line compiles in (for example opcache on 8.5).
	cleaned, _, err := s.sanitizePHPOverridesLocked(version, merged.Extensions)
	if err != nil {
		return PHPDefaultsResult{}, err
	}
	merged.Extensions = cleaned
	if err := phpini.ValidateOverrides(merged.Ini); err != nil {
		return PHPDefaultsResult{}, fmt.Errorf("sites: PHP %s ini defaults: %w", version, err)
	}
	if _, err := manifest.ResolveWithDefaults(version, merged.Extensions, nil); err != nil {
		return PHPDefaultsResult{}, fmt.Errorf("sites: PHP %s extension defaults: %w", version, err)
	}
	if err := s.deps.Store.SavePHPDefaults(ctx, merged); err != nil {
		return PHPDefaultsResult{}, fmt.Errorf("sites: save PHP %s defaults: %w", version, err)
	}

	result := PHPDefaultsResult{Defaults: merged, Sites: []PHPMigrationResult{}}
	sites, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return result, fmt.Errorf("sites: list sites for PHP defaults: %w", err)
	}
	for _, site := range sites {
		if site.Kind != model.SitePHP ||
			strings.TrimSpace(site.PHPVersion) != version ||
			site.Status != model.StatusRunning {
			continue
		}
		_, applyErr := s.applyPHPChangeLocked(ctx, site, PHPChange{})
		result.Sites = append(result.Sites, PHPMigrationResult{
			Site:  site.Name,
			OK:    applyErr == nil,
			Error: errorText(applyErr),
		})
	}
	return result, nil
}

// MigratePHP moves every site on FromVersion to the resolved target build and
// reports each site's outcome independently.
func (s *Service) MigratePHP(ctx context.Context, req PHPMigrationRequest) ([]PHPMigrationResult, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return nil, errors.New("sites: missing dependencies: store")
	}
	if s.deps.PHP == nil {
		return nil, errors.New("sites: missing dependencies: PHP validator")
	}
	fromVersion := strings.TrimSpace(req.FromVersion)
	toVersion := strings.TrimSpace(req.ToVersion)
	toBuild := strings.TrimSpace(req.ToBuild)
	if toVersion == "" && toBuild == "" {
		return nil, errors.New("sites: PHP migration requires ToVersion or ToBuild")
	}
	targetVersion := toVersion
	if toBuild != "" && targetVersion == "" {
		line, err := s.phpLineForBuildLocked(ctx, toBuild)
		if err != nil {
			return nil, err
		}
		targetVersion = line
	}
	build, err := s.resolvePHPBuildLocked(ctx, targetVersion, toBuild)
	if err != nil {
		return nil, err
	}
	targetVersion = catalog.LineOf(build.Version)

	sites, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("sites: list sites for PHP migration: %w", err)
	}
	results := []PHPMigrationResult{}
	for _, site := range sites {
		if site.Kind != model.SitePHP || strings.TrimSpace(site.PHPVersion) != fromVersion {
			continue
		}
		_, applyErr := s.applyPHPChangeLocked(ctx, site, PHPChange{Version: targetVersion, Build: build.ID})
		results = append(results, PHPMigrationResult{
			Site:  site.Name,
			OK:    applyErr == nil,
			Error: errorText(applyErr),
		})
	}
	return results, nil
}

// CheckRuntimeRemoval reports the sites that still pin a PHP build. It returns
// store.ErrNotFound when the build is not installed.
func (s *Service) CheckRuntimeRemoval(ctx context.Context, buildID string) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return errors.New("sites: missing dependencies: store")
	}
	build, err := s.deps.Store.GetRuntime(ctx, strings.TrimSpace(buildID))
	if errors.Is(err, store.ErrNotFound) {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sites: read runtime %q: %w", buildID, err)
	}
	sites, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list sites for runtime check: %w", err)
	}
	line := catalog.LineOf(build.Version)
	names := make(map[string]bool, len(sites))
	for _, site := range sites {
		if site.Kind != model.SitePHP {
			continue
		}
		if site.PHPBuild == build.ID || (site.PHPBuild == "" && site.PHPVersion == line) {
			names[site.Name] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return &RuntimeInUseError{BuildID: build.ID, Sites: sorted}
}

// errorText renders a per-site failure without losing nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// versionSupported reports whether a manifest extension lists a PHP line.
func versionSupported(versions []string, version string) bool {
	for _, candidate := range versions {
		if candidate == version {
			return true
		}
	}
	return false
}
