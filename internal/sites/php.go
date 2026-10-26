package sites

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// ErrRuntimeMissing reports that no installed PHP build can satisfy a site.
var ErrRuntimeMissing = errors.New("sites: PHP runtime is not installed")

// ErrPHPExecArgument reports an argv entry that would bypass the generated
// php.ini.
var ErrPHPExecArgument = errors.New("sites: PHP exec argument would bypass the generated php.ini")

// PHPExecArgumentError names the rejected argument and wraps
// ErrPHPExecArgument.
type PHPExecArgumentError struct {
	Argument string
}

// Error implements error.
func (e *PHPExecArgumentError) Error() string {
	if e == nil {
		return ErrPHPExecArgument.Error()
	}
	return fmt.Sprintf("sites: PHP exec argument %q would bypass the generated php.ini", e.Argument)
}

// Unwrap makes every rejected argument match ErrPHPExecArgument.
func (e *PHPExecArgumentError) Unwrap() error { return ErrPHPExecArgument }

// phpDefaultGeneration is the single PHP master generation used by the site
// lifecycle. Later validate-then-swap work stages extra generations.
const phpDefaultGeneration = "default"

// phpCleanupTimeout bounds teardown that must complete even when the request
// context is already canceled.
const phpCleanupTimeout = 30 * time.Second

// phpCleanupContext detaches cleanup from the caller's cancellation. A client
// that times out during an apply must not leave an FPM master listening on a
// port the allocator has released.
func phpCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(nonNilContext(ctx)), phpCleanupTimeout)
}

// stopPHPGenerationForCleanup stops one staged generation with a
// cancellation-proof context.
func (s *Service) stopPHPGenerationForCleanup(ctx context.Context, siteID, generation string) error {
	cleanupCtx, cancel := phpCleanupContext(ctx)
	defer cancel()
	return s.deps.PHPBackend.StopPHP(cleanupCtx, siteID, generation)
}

// retainPHPGenerationLocked records a generation whose stop failed and the
// FastCGI port reserved for it. The caller must hold s.mu.
func (s *Service) retainPHPGenerationLocked(siteID, generation string, port int) {
	if siteID == "" || generation == "" {
		return
	}
	if s.retainedPHP == nil {
		s.retainedPHP = make(map[string]map[string]int)
	}
	generations := s.retainedPHP[siteID]
	if generations == nil {
		generations = make(map[string]int)
		s.retainedPHP[siteID] = generations
	}
	if existing, ok := generations[generation]; ok && existing != 0 {
		// The generation name still refers to the master that failed to stop;
		// keep the port recorded for its first failed stop instead of the port
		// of a later row.
		port = existing
	}
	generations[generation] = port
}

// dropRetainedPHPGenerationLocked stops tracking one retained generation. The
// caller must hold s.mu.
func (s *Service) dropRetainedPHPGenerationLocked(siteID, generation string) int {
	generations := s.retainedPHP[siteID]
	if generations == nil {
		return 0
	}
	port := generations[generation]
	delete(generations, generation)
	if len(generations) == 0 {
		delete(s.retainedPHP, siteID)
	}
	return port
}

// retainedPHPGenerationsLocked returns a copy of a site's retained generations
// and their ports. The caller must hold s.mu.
func (s *Service) retainedPHPGenerationsLocked(siteID string) map[string]int {
	generations := s.retainedPHP[siteID]
	if len(generations) == 0 {
		return nil
	}
	retained := make(map[string]int, len(generations))
	for generation, port := range generations {
		retained[generation] = port
	}
	return retained
}

// releaseRetainedPHPGenerationsLocked releases every retained port that no
// live row still owns, then forgets the entries. A stopped PHP row keeps its
// FastCGI port for a later Start, so that port stays reserved even though the
// retained entry is removed. The caller must hold s.mu.
func (s *Service) releaseRetainedPHPGenerationsLocked(siteID string, ownedPorts ...int) {
	generations := s.retainedPHP[siteID]
	if len(generations) == 0 {
		return
	}
	owned := make(map[int]bool, len(ownedPorts))
	for _, port := range ownedPorts {
		if port != 0 {
			owned[port] = true
		}
	}
	if s.deps.Ports != nil {
		for _, port := range generations {
			if port != 0 && !owned[port] {
				s.releasePort(context.Background(), siteID, port)
			}
		}
	}
	delete(s.retainedPHP, siteID)
}

// PHPCommands validates generated PHP configuration. The production
// implementation shells out to the build's binaries; tests inject fakes.
type PHPCommands interface {
	Lint(ctx context.Context, phpBin, iniPath string) error
	ValidateFPM(ctx context.Context, fpmBin, configPath, iniPath string) error
}

// defaultPHPCommands is the production validator. It never runs during
// NewService: the binaries are only invoked when a PHP operation needs them.
type defaultPHPCommands struct{}

// NewPHPCommands returns the production PHP configuration validator.
func NewPHPCommands() PHPCommands { return defaultPHPCommands{} }

var _ PHPCommands = defaultPHPCommands{}

// Lint validates iniPath with the build's PHP binary.
func (defaultPHPCommands) Lint(ctx context.Context, phpBin, iniPath string) error {
	return phpini.Lint(ctx, phpini.ExecRunner{}, phpBin, iniPath)
}

// ValidateFPM runs the build's php-fpm configuration test with the generated
// ini file. The test is bounded so a hanging binary cannot block the service.
func (defaultPHPCommands) ValidateFPM(ctx context.Context, fpmBin, configPath, iniPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return phpfpm.Validate(ctx, fpmBin, configPath, iniPath)
}

// checkPHPBackend reports a missing generation-aware PHP backend.
func (s *Service) checkPHPBackend() error {
	if s.deps.PHPBackend == nil {
		return errors.New("sites: PHP backend is not configured; provide Deps.PHPBackend or a backend that implements backend.PHPStager")
	}
	return nil
}

// phpCatalog returns the embedded PHP manifest, loading it on first use. A
// broken embedded manifest is reported as an actionable error, never a panic.
// The caller must hold s.mu.
func (s *Service) phpCatalog() (*catalog.PHPManifest, error) {
	if s.deps.PHPManifest != nil {
		return s.deps.PHPManifest, nil
	}
	if s.phpManifest != nil {
		return s.phpManifest, nil
	}
	if s.phpManifestErr != nil {
		return nil, s.phpManifestErr
	}
	manifest, err := catalog.LoadPHP()
	if err != nil {
		s.phpManifestErr = fmt.Errorf("sites: load the embedded PHP manifest: %w", err)
		return nil, s.phpManifestErr
	}
	s.phpManifest = &manifest
	return s.phpManifest, nil
}

// resolvePHPBuildLocked selects an installed PHP build. An explicit id must be
// installed and match the requested line; otherwise the newest installed
// version and revision for the line wins. Nothing is installed here.
func (s *Service) resolvePHPBuildLocked(ctx context.Context, line, buildID string) (model.RuntimeBuild, error) {
	if !supportedPHPLine(line) {
		return model.RuntimeBuild{}, fmt.Errorf("sites: unsupported PHP version %q (want 8.2, 8.3, 8.4, or 8.5)", line)
	}
	if buildID != "" {
		build, err := s.deps.Store.GetRuntime(ctx, buildID)
		if errors.Is(err, store.ErrNotFound) {
			return model.RuntimeBuild{}, fmt.Errorf("sites: PHP build %q is not installed: %w", buildID, ErrRuntimeMissing)
		}
		if err != nil {
			return model.RuntimeBuild{}, fmt.Errorf("sites: read PHP build %q: %w", buildID, err)
		}
		if err := validatePHPBuildRow(build, line); err != nil {
			return model.RuntimeBuild{}, fmt.Errorf("%w: %v", ErrRuntimeMissing, err)
		}
		return build, nil
	}

	builds, err := s.deps.Store.ListRuntimes(ctx)
	if err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("sites: list installed runtimes: %w", err)
	}
	var best model.RuntimeBuild
	var bestID catalog.ID
	found := false
	for _, build := range builds {
		if build.Kind != "php" || !phpBuildLineMatches(build, line) {
			continue
		}
		parsed, err := catalog.ParseID(build.ID)
		if err != nil {
			continue
		}
		if !found ||
			comparePHPVersions(parsed.Version, bestID.Version) > 0 ||
			(comparePHPVersions(parsed.Version, bestID.Version) == 0 && parsed.Revision > bestID.Revision) {
			best, bestID, found = build, parsed, true
		}
	}
	if !found {
		return model.RuntimeBuild{}, fmt.Errorf("sites: no installed PHP %s build: %w", line, ErrRuntimeMissing)
	}
	if err := validatePHPBuildRow(best, line); err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("%w: %v", ErrRuntimeMissing, err)
	}
	return best, nil
}

// validatePHPBuildRow verifies a store row is a usable build for the line.
func validatePHPBuildRow(build model.RuntimeBuild, line string) error {
	if build.Kind != "php" {
		return fmt.Errorf("build %q is %q, not php", build.ID, build.Kind)
	}
	parsed, err := catalog.ParseID(build.ID)
	if err != nil {
		return err
	}
	if parsed.Kind != "php" {
		return fmt.Errorf("build %q is not a PHP build", build.ID)
	}
	if catalog.LineOf(parsed.Version) != line {
		return fmt.Errorf("build %q is PHP %s, not %s", build.ID, catalog.LineOf(parsed.Version), line)
	}
	if catalog.LineOf(build.Version) != line {
		return fmt.Errorf("build %q records version %q, not line %s", build.ID, build.Version, line)
	}
	path := strings.TrimSpace(build.Path)
	if path == "" {
		return fmt.Errorf("build %q has no install path", build.ID)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("build %q has invalid install path %q", build.ID, build.Path)
	}
	return nil
}

// sanitizePHPOverridesLocked drops extension overrides that are built into the
// requested PHP line. A pre-fix row can carry a stale override (for example an
// opcache override inherited from PHP 8.4 when the site moved to 8.5); dropping
// it keeps rendering, inspection, and migration usable instead of failing with
// ErrInvalidExtension. The dropped names are returned sorted for diagnostics.
func (s *Service) sanitizePHPOverridesLocked(version string, overrides map[string]bool) (map[string]bool, []string, error) {
	if len(overrides) == 0 {
		return overrides, nil, nil
	}
	manifest, err := s.phpCatalog()
	if err != nil {
		return nil, nil, err
	}
	cleaned := cloneBoolMap(overrides)
	var dropped []string
	for _, name := range manifest.BuiltinsFor(version) {
		if _, ok := cleaned[name]; !ok {
			continue
		}
		delete(cleaned, name)
		dropped = append(dropped, name)
	}
	sort.Strings(dropped)
	return cleaned, dropped, nil
}

// phpBuildForSiteLocked resolves the build pinned by a site row.
func (s *Service) phpBuildForSiteLocked(ctx context.Context, site model.Site) (model.RuntimeBuild, error) {
	line := strings.TrimSpace(site.PHPVersion)
	if line == "" {
		if strings.TrimSpace(site.PHPBuild) == "" {
			return model.RuntimeBuild{}, fmt.Errorf("sites: PHP site %q has no build: %w", site.Name, ErrRuntimeMissing)
		}
		parsed, err := catalog.ParseID(site.PHPBuild)
		if err != nil {
			return model.RuntimeBuild{}, fmt.Errorf("sites: PHP site %q build: %w", site.Name, err)
		}
		line = catalog.LineOf(parsed.Version)
	}
	return s.resolvePHPBuildLocked(ctx, line, strings.TrimSpace(site.PHPBuild))
}

// phpSiteSpecFromBuildLocked converts a generated site and its build into the
// backend's PHP specification.
func (s *Service) phpSiteSpecFromBuildLocked(site model.Site, build model.RuntimeBuild) (backend.SiteSpec, error) {
	generationDir, err := s.phpGenerationDir(site.ID, site.PHPConfigHash)
	if err != nil {
		return backend.SiteSpec{}, err
	}
	if !phpGenerationComplete(generationDir) {
		return backend.SiteSpec{}, fmt.Errorf("sites: PHP configuration for %q is not written", site.Name)
	}
	spec := backendSpec(site)
	spec.PHPVersion = site.PHPVersion
	spec.PHPBuild = build.ID
	spec.PHPBuildPath = build.Path
	spec.PHPIniPath = filepath.Join(generationDir, phpIniFile)
	spec.FPMConfigPath = filepath.Join(generationDir, phpFPMFile)
	spec.PHPHealthScript = filepath.Join(generationDir, phpHealthFile)
	spec.FastCGIPort = site.FastCGIPort
	return spec, nil
}

// phpSiteSpecLocked resolves the pinned build and the current generation for
// a persisted PHP site.
func (s *Service) phpSiteSpecLocked(ctx context.Context, site model.Site) (backend.SiteSpec, error) {
	build, err := s.phpBuildForSiteLocked(ctx, site)
	if err != nil {
		return backend.SiteSpec{}, err
	}
	return s.phpSiteSpecFromBuildLocked(site, build)
}

// startPHPLocked starts (or restarts) the default FPM master. The stored
// generation is reused only when its port is set and all three files exist;
// otherwise the configuration is recomposed, validated, and written.
func (s *Service) startPHPLocked(ctx context.Context, site model.Site) (model.Site, error) {
	if err := s.checkPHPBackend(); err != nil {
		return site, err
	}
	previous := site
	build, err := s.phpBuildForSiteLocked(ctx, site)
	if err != nil {
		return site, err
	}
	// Heal a pre-fix row that still overrides a module built into this line.
	version := strings.TrimSpace(site.PHPVersion)
	if version == "" {
		version = catalog.LineOf(build.Version)
	}
	cleaned, dropped, err := s.sanitizePHPOverridesLocked(version, site.PHPExtensions)
	if err != nil {
		return site, err
	}
	site.PHPExtensions = cleaned
	if len(dropped) > 0 {
		s.emitEvent(ctx, site.ID, "site.updated", fmt.Sprintf(
			"site %s: dropped extension overrides built into PHP %s: %s",
			site.Name, version, strings.Join(dropped, ", ")))
	}
	// A previously applied site serves from a hash generation, not "default".
	// Start is idempotent: never stage a second master when one is running.
	if previous.Status == model.StatusRunning && previous.FastCGIPort != 0 && s.deps.Backend != nil {
		status, err := s.deps.Backend.Status(ctx, site.ID)
		if err != nil {
			return site, fmt.Errorf("sites: status for %q: %w", site.Name, err)
		}
		if status.State == model.StatusRunning {
			return site, nil
		}
	}

	allocated := 0
	if site.FastCGIPort == 0 {
		port, err := s.allocatePort(ctx, site.ID, domain.PortFastCGI)
		if err != nil {
			return site, fmt.Errorf("sites: allocate FastCGI port for %q: %w", site.Name, err)
		}
		site.FastCGIPort = port
		allocated = port
	}
	releaseAllocated := func() {
		if allocated != 0 {
			s.releasePort(ctx, site.ID, allocated)
		}
	}

	// Recompute the current configuration instead of trusting the stored hash:
	// edited version defaults must take effect on the next start of a stopped
	// site. Reuse is safe only when the rendered hash still matches and every
	// generation file is present.
	files, err := s.composePHPFilesLocked(ctx, site, build)
	if err != nil {
		releaseAllocated()
		return site, err
	}
	reused := previous.FastCGIPort != 0 &&
		site.PHPConfigHash == files.Hash &&
		s.phpConfigFilesExistLocked(site)
	if !reused {
		site.PHPConfigHash = files.Hash
		generationDir, err := s.writePHPFilesLocked(site.ID, files)
		if err != nil {
			releaseAllocated()
			return site, err
		}
		if err := s.validatePHPFilesLocked(ctx, site, build, generationDir); err != nil {
			releaseAllocated()
			return site, err
		}
	}

	spec, err := s.phpSiteSpecFromBuildLocked(site, build)
	if err != nil {
		releaseAllocated()
		return site, err
	}
	prepared, err := s.deps.PHPBackend.PreparePHP(ctx, site.ID, phpDefaultGeneration, spec)
	if err != nil {
		releaseAllocated()
		return site, fmt.Errorf("sites: prepare %q: %w", site.Name, err)
	}
	s.recordBackendHost(site.ID, prepared)
	if _, err := s.deps.PHPBackend.StartPHP(ctx, site.ID, phpDefaultGeneration); err != nil {
		cause := fmt.Errorf("sites: start %q: %w", site.Name, err)
		if stopErr := s.stopPHPGenerationForCleanup(ctx, site.ID, phpDefaultGeneration); stopErr != nil {
			s.retainPHPGenerationLocked(site.ID, phpDefaultGeneration, site.FastCGIPort)
			return site, errors.Join(cause,
				fmt.Errorf("sites: stop %q after a failed start: %w", site.Name, stopErr))
		}
		releaseAllocated()
		return site, cause
	}

	oldStatus := site.Status
	site.DesiredState = model.DesiredRunning
	site.Status = model.StatusRunning
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		cause := fmt.Errorf("sites: record running state for %q: %w", site.Name, err)
		if stopErr := s.stopPHPGenerationForCleanup(ctx, site.ID, phpDefaultGeneration); stopErr != nil {
			s.retainPHPGenerationLocked(site.ID, phpDefaultGeneration, site.FastCGIPort)
			return previous, errors.Join(cause,
				fmt.Errorf("sites: stop %q after a failed state write: %w", site.Name, stopErr))
		}
		releaseAllocated()
		return previous, cause
	}
	if !reused {
		if err := s.saveAppliedPHPConfigLocked(ctx, site); err != nil {
			cause := fmt.Errorf("sites: save PHP configuration for %q: %w", site.Name, err)
			stopErr := s.stopPHPGenerationForCleanup(ctx, site.ID, phpDefaultGeneration)
			restoreErr := s.deps.Store.UpdateSite(ctx, previous)
			if stopErr != nil {
				s.retainPHPGenerationLocked(site.ID, phpDefaultGeneration, site.FastCGIPort)
				return previous, errors.Join(cause, restoreErr,
					fmt.Errorf("sites: stop %q after a failed config save: %w", site.Name, stopErr))
			}
			releaseAllocated()
			return previous, errors.Join(cause, restoreErr)
		}
	}
	s.commitPHPFilesBestEffortLocked(ctx, site)
	s.emitStatus(ctx, site, oldStatus, model.StatusRunning, "")
	return site, nil
}

// stopPHPLocked stops the FPM master and records the stopped state.
func (s *Service) stopPHPLocked(ctx context.Context, site model.Site) (model.Site, error) {
	if err := s.stopPHPProcessLocked(ctx, site.ID); err != nil {
		return site, fmt.Errorf("sites: stop %q: %w", site.Name, err)
	}
	// Local.Stop below terminates every staged generation, including ones kept
	// after an earlier failed cleanup. Release their ports, but keep the port
	// the stopped row still owns: Start reuses it without reallocating.
	if s.deps.Backend != nil {
		s.releaseRetainedPHPGenerationsLocked(site.ID, site.FastCGIPort)
	}
	oldStatus := site.Status
	site.DesiredState = model.DesiredStopped
	site.Status = model.StatusStopped
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return site, fmt.Errorf("sites: record stopped state for %q: %w", site.Name, err)
	}
	s.emitStatus(ctx, site, oldStatus, model.StatusStopped, "")
	return site, nil
}

// stopPHPProcessLocked stops the default generation and any supervisor
// record that predates this daemon's in-memory staging.
func (s *Service) stopPHPProcessLocked(ctx context.Context, siteID string) error {
	if err := s.checkPHPBackend(); err != nil {
		return err
	}
	if err := s.deps.PHPBackend.StopPHP(ctx, siteID, phpDefaultGeneration); err != nil {
		return err
	}
	if s.deps.Backend != nil {
		// Local.Stop also stops the bare supervisor id and any staged
		// generations, including after a daemon restart.
		if err := s.deps.Backend.Stop(ctx, siteID, true); err != nil {
			return err
		}
	}
	return nil
}

// phpUpdatePlan describes generation work performed for a PHP update. The new
// generation is written before the row is published and removed again if the
// update fails; the previous generation is always preserved.
type phpUpdatePlan struct {
	applied         bool
	generationDir   string
	removeOnFailure bool
}

// cleanup removes only a generation directory created by this update.
func (p phpUpdatePlan) cleanup() {
	if p.removeOnFailure && p.generationDir != "" {
		_ = os.RemoveAll(p.generationDir)
	}
}

// preparePHPUpdateLocked recomposes a PHP generation when the document root or
// environment changed. It does not start or restart the master; the running
// master keeps the generation it was started with, and the next start uses the
// new files.
func (s *Service) preparePHPUpdateLocked(ctx context.Context, current model.Site, updated *model.Site) (phpUpdatePlan, error) {
	if updated == nil || updated.Kind != model.SitePHP {
		return phpUpdatePlan{}, nil
	}
	documentRootChanged := strings.TrimSpace(current.DocumentRoot) != strings.TrimSpace(updated.DocumentRoot)
	environmentChanged := !sameStringMap(current.Env, updated.Env)
	if !documentRootChanged && !environmentChanged {
		return phpUpdatePlan{}, nil
	}
	if updated.FastCGIPort == 0 {
		// No applied generation exists; the next start composes with the new
		// values and allocates a port.
		updated.PHPConfigHash = ""
		return phpUpdatePlan{}, nil
	}

	build, err := s.phpBuildForSiteLocked(ctx, *updated)
	if err != nil {
		return phpUpdatePlan{}, err
	}
	files, err := s.composePHPFilesLocked(ctx, *updated, build)
	if err != nil {
		return phpUpdatePlan{}, err
	}
	newDir, err := s.phpGenerationDir(updated.ID, files.Hash)
	if err != nil {
		return phpUpdatePlan{}, err
	}
	existing := phpGenerationComplete(newDir)
	updated.PHPConfigHash = files.Hash
	generationDir, err := s.writePHPFilesLocked(updated.ID, files)
	if err != nil {
		return phpUpdatePlan{}, err
	}
	if err := s.validatePHPFilesLocked(ctx, *updated, build, generationDir); err != nil {
		if !existing {
			_ = os.RemoveAll(generationDir)
		}
		return phpUpdatePlan{}, err
	}
	return phpUpdatePlan{
		applied:         true,
		generationDir:   generationDir,
		removeOnFailure: !existing,
	}, nil
}

// sameStringMap compares two environment maps as unordered key/value sets.
func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		other, ok := b[key]
		if !ok || other != value {
			return false
		}
	}
	return true
}

// ensurePHPConfigLocked returns a usable site configuration for PHPExec,
// composing and persisting one when the stored generation is missing. A
// stopped site persists the new generation so it starts from it later. A
// running site only uses the composed generation for this exec: its master
// still serves the stored generation, so the row keeps pointing at that
// generation until the next Start or ApplyPHP swaps the master.
func (s *Service) ensurePHPConfigLocked(ctx context.Context, site model.Site) (model.Site, model.RuntimeBuild, error) {
	build, err := s.phpBuildForSiteLocked(ctx, site)
	if err != nil {
		return site, model.RuntimeBuild{}, err
	}
	previous := site
	version := strings.TrimSpace(site.PHPVersion)
	if version == "" {
		version = catalog.LineOf(build.Version)
	}
	cleaned, dropped, err := s.sanitizePHPOverridesLocked(version, site.PHPExtensions)
	if err != nil {
		return site, model.RuntimeBuild{}, err
	}
	site.PHPExtensions = cleaned
	if len(dropped) > 0 {
		s.emitEvent(ctx, site.ID, "site.updated", fmt.Sprintf(
			"site %s: dropped extension overrides built into PHP %s: %s",
			site.Name, version, strings.Join(dropped, ", ")))
	}
	running := site.Status == model.StatusRunning
	allocated := 0
	if running && site.FastCGIPort == 0 {
		return site, model.RuntimeBuild{}, fmt.Errorf(
			"sites: running PHP site %q has no FastCGI port", site.Name)
	}
	if site.FastCGIPort == 0 {
		port, err := s.allocatePort(ctx, site.ID, domain.PortFastCGI)
		if err != nil {
			return site, model.RuntimeBuild{}, fmt.Errorf("sites: allocate FastCGI port for %q: %w", site.Name, err)
		}
		site.FastCGIPort = port
		allocated = port
	}
	releaseAllocated := func() {
		if allocated != 0 {
			s.releasePort(ctx, site.ID, allocated)
		}
	}

	// Compare the current inputs with the stored generation so PHPExec picks up
	// edited version defaults even when the site is stopped.
	files, err := s.composePHPFilesLocked(ctx, site, build)
	if err != nil {
		releaseAllocated()
		return site, model.RuntimeBuild{}, err
	}
	reused := allocated == 0 &&
		site.PHPConfigHash == files.Hash &&
		s.phpConfigFilesExistLocked(site)
	if reused {
		return site, build, nil
	}
	site.PHPConfigHash = files.Hash
	generationDir, err := s.phpGenerationDir(site.ID, files.Hash)
	if err != nil {
		releaseAllocated()
		return site, model.RuntimeBuild{}, err
	}
	preexisting := phpGenerationComplete(generationDir)
	written, err := s.writePHPFilesLocked(site.ID, files)
	if err != nil {
		if !preexisting {
			_ = os.RemoveAll(generationDir)
		}
		releaseAllocated()
		return site, model.RuntimeBuild{}, err
	}
	if err := s.validatePHPFilesLocked(ctx, site, build, written); err != nil {
		if !preexisting {
			_ = os.RemoveAll(generationDir)
		}
		releaseAllocated()
		return site, model.RuntimeBuild{}, err
	}
	if running {
		// The master was started with the stored generation. Leave the row and
		// the applied-config snapshot untouched so the next ApplyPHP recomposes
		// and swaps the master instead of taking a false no-op.
		return site, build, nil
	}
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		releaseAllocated()
		return previous, model.RuntimeBuild{}, fmt.Errorf("sites: record PHP config for %q: %w", site.Name, err)
	}
	if err := s.saveAppliedPHPConfigLocked(ctx, site); err != nil {
		_ = s.deps.Store.UpdateSite(ctx, previous)
		releaseAllocated()
		return previous, model.RuntimeBuild{}, fmt.Errorf("sites: save PHP configuration for %q: %w", site.Name, err)
	}
	s.commitPHPFilesBestEffortLocked(ctx, site)
	return site, build, nil
}

// PHPExec runs the site's PHP binary with the generated php.ini. It works for
// stopped sites and never starts an FPM master. Forwarded arguments may not
// override or disable the generated ini.
func (s *Service) PHPExec(ctx context.Context, ref string, argv []string) (backend.Result, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, false); err != nil {
		return backend.Result{}, err
	}
	if err := validatePHPExecArgv(argv); err != nil {
		return backend.Result{}, err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return backend.Result{}, err
	}
	if site.Kind != model.SitePHP {
		return backend.Result{}, fmt.Errorf("sites: site %q is not a PHP site", site.Name)
	}
	site, build, err := s.ensurePHPConfigLocked(ctx, site)
	if err != nil {
		return backend.Result{}, err
	}
	spec, err := s.phpSiteSpecFromBuildLocked(site, build)
	if err != nil {
		return backend.Result{}, err
	}
	if _, err := s.deps.Backend.Prepare(ctx, spec); err != nil {
		return backend.Result{}, fmt.Errorf("sites: prepare %q for exec: %w", site.Name, err)
	}
	command := append([]string{
		filepath.Join(build.Path, "bin", "php"),
		"-c",
		spec.PHPIniPath,
	}, argv...)
	result, err := s.deps.Backend.Exec(ctx, site.ID, command, nil)
	if err != nil {
		return result, fmt.Errorf("sites: exec in %q: %w", site.Name, err)
	}
	return result, nil
}

// validatePHPExecArgv rejects flags that would bypass the generated php.ini.
func validatePHPExecArgv(argv []string) error {
	for _, argument := range argv {
		blocked := argument == "-n" ||
			argument == "--no-php-ini" ||
			argument == "--php-ini" ||
			strings.HasPrefix(argument, "--php-ini=") ||
			(strings.HasPrefix(argument, "-c") && !strings.HasPrefix(argument, "--"))
		if blocked {
			return &PHPExecArgumentError{Argument: argument}
		}
	}
	return nil
}

// ValidatePHP validates the site's current configuration in a temporary
// directory without starting a master or changing the site row.
func (s *Service) ValidatePHP(ctx context.Context, site model.Site) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Store == nil {
		return errors.New("sites: missing dependencies: store")
	}
	if s.deps.PHP == nil {
		return errors.New("sites: missing dependencies: PHP validator")
	}
	if site.Kind != model.SitePHP {
		return fmt.Errorf("sites: site %q is not a PHP site", site.Name)
	}
	build, err := s.phpBuildForSiteLocked(ctx, site)
	if err != nil {
		return err
	}

	allocated := 0
	if site.FastCGIPort == 0 {
		if s.deps.Ports == nil {
			return errors.New("sites: missing dependencies: port allocator")
		}
		port, err := s.allocatePort(ctx, site.ID, domain.PortFastCGI)
		if err != nil {
			return fmt.Errorf("sites: allocate FastCGI port for validation: %w", err)
		}
		site.FastCGIPort = port
		allocated = port
	}
	if allocated != 0 {
		defer s.releasePort(ctx, site.ID, allocated)
	}

	siteDir, err := s.phpSiteDir(site.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(siteDir, 0o700); err != nil {
		return fmt.Errorf("sites: create PHP config directory: %w", err)
	}
	temporary, err := os.MkdirTemp(siteDir, "validate-")
	if err != nil {
		return fmt.Errorf("sites: create validation directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	rendered, err := s.renderPHPFilesLocked(ctx, site, build, temporary)
	if err != nil {
		return err
	}
	if err := writePHPGeneration(temporary, phpFiles{
		Ini:    rendered.Ini,
		FPM:    rendered.FPM,
		Health: []byte(phpHealthScript),
	}); err != nil {
		return err
	}
	return s.validatePHPFilesLocked(ctx, site, build, temporary)
}
