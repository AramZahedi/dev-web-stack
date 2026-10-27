package sites

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

const (
	// phpGenerationAnchor is the directory name used while hashing a config
	// whose final hash (and therefore directory) is not known yet.
	phpGenerationAnchor = "generation"
	phpIniFile          = "php.ini"
	phpFPMFile          = "php-fpm.conf"
	phpHealthFile       = "health.php"
	phpHealthScript     = "<?php echo \"ok\";\n"
)

// phpFiles is one fully rendered immutable PHP generation.
type phpFiles struct {
	Hash   string
	Ini    []byte
	FPM    []byte
	Health []byte
}

// phpRendered is the raw render output for one generation directory.
type phpRendered struct {
	Ini []byte
	FPM []byte
}

// composePHPFilesLocked renders the site's php.ini and php-fpm.conf and
// returns the SHA-256 hash that names the immutable generation directory. The
// hash covers the ini and the FPM configuration. FPM configuration embeds the
// generation directory in its php.ini/log/PID paths, which is not known until
// after hashing, so the hash is computed over a render anchored at a stable
// directory name and the final render is then materialised under the hash.
func (s *Service) composePHPFilesLocked(ctx context.Context, site model.Site, build model.RuntimeBuild) (phpFiles, error) {
	siteRoot, err := s.phpSiteDir(site.ID)
	if err != nil {
		return phpFiles{}, err
	}
	anchorDir := filepath.Join(siteRoot, phpGenerationAnchor)
	anchor, err := s.renderPHPFilesLocked(ctx, site, build, anchorDir)
	if err != nil {
		return phpFiles{}, err
	}
	hash := hashPHPConfig(anchor.Ini, anchor.FPM)

	finalDir := filepath.Join(siteRoot, hash)
	final, err := s.renderPHPFilesLocked(ctx, site, build, finalDir)
	if err != nil {
		return phpFiles{}, err
	}
	return phpFiles{
		Hash:   hash,
		Ini:    final.Ini,
		FPM:    final.FPM,
		Health: []byte(phpHealthScript),
	}, nil
}

// renderPHPFilesLocked composes the ini and FPM configuration for one
// generation directory.
func (s *Service) renderPHPFilesLocked(ctx context.Context, site model.Site, build model.RuntimeBuild, generationDir string) (phpRendered, error) {
	if !supportedPHPLine(site.PHPVersion) {
		return phpRendered{}, fmt.Errorf("sites: unsupported PHP version %q", site.PHPVersion)
	}
	defaults, err := s.phpDefaultsLocked(ctx, site.PHPVersion)
	if err != nil {
		return phpRendered{}, err
	}
	manifest, err := s.phpCatalog()
	if err != nil {
		return phpRendered{}, err
	}
	overrides, _, err := s.sanitizePHPOverridesLocked(site.PHPVersion, site.PHPExtensions)
	if err != nil {
		return phpRendered{}, err
	}
	resolved, err := manifest.ResolveWithDefaults(site.PHPVersion, defaults.Extensions, overrides)
	if err != nil {
		return phpRendered{}, fmt.Errorf("sites: PHP extensions for %q: %w", site.Name, err)
	}
	extensions := make([]phpini.Extension, 0, len(resolved))
	for _, extension := range resolved {
		extensions = append(extensions, phpini.Extension{
			Name: extension.Name,
			Type: string(extension.Type),
			SO:   extension.SO,
		})
	}

	base, err := readPHPBaseIni(build)
	if err != nil {
		return phpRendered{}, err
	}
	extensionDir, err := phpExtensionDir(build)
	if err != nil {
		return phpRendered{}, err
	}
	if err := validatePHPConfigInputs(site); err != nil {
		return phpRendered{}, err
	}
	caFile := filepath.Join(s.deps.Config.Home, "pki", "ca-bundle.crt")
	if site.Backend == model.BackendWSL || build.Backend == model.BackendWSL || runtime.GOOS == "windows" {
		caFile = "/data/devstack/certs/ca-bundle.crt"
	}
	ini, err := phpini.Compose(phpini.Config{
		Base:            base,
		VersionDefaults: defaults.Ini,
		Overrides:       site.PHPIni,
		Raw:             site.PHPIniRaw,
		Extensions:      extensions,
		ExtensionDir:    extensionDir,
		CAFile:          caFile,
	})
	if err != nil {
		return phpRendered{}, fmt.Errorf("sites: compose php.ini for %q: %w", site.Name, err)
	}

	documentRoot := absoluteDocumentRoot(site)
	if documentRoot == "" {
		return phpRendered{}, fmt.Errorf("sites: PHP site %q has an empty document root", site.Name)
	}
	fpm, err := phpfpm.Render(phpfpm.Config{
		SiteID:       site.ID,
		BuildRoot:    build.Path,
		PHPIni:       filepath.Join(generationDir, phpIniFile),
		ListenHost:   "127.0.0.1",
		ListenPort:   site.FastCGIPort,
		DocumentRoot: documentRoot,
		// The supervisor wires php-fpm's stderr to the site log stream, so
		// fd 2 merges master and pool output (with redaction) into
		// `devstack logs`. Only the PID file is generation-local.
		ErrorLog: "/proc/self/fd/2",
		PIDFile:  filepath.Join(generationDir, "php-fpm.pid"),
		User:     "",
		Group:    "",
		Env:      cloneEnv(site.Env),
		Pool:     site.PHPFPM,
	})
	if err != nil {
		return phpRendered{}, fmt.Errorf("sites: render php-fpm.conf for %q: %w", site.Name, err)
	}
	return phpRendered{Ini: ini, FPM: fpm}, nil
}

// validatePHPConfigInputs checks the values config rendering relies on.
func validatePHPConfigInputs(site model.Site) error {
	if site.FastCGIPort < 43000 || site.FastCGIPort > 43999 {
		return fmt.Errorf("sites: PHP site %q FastCGI port %d is outside 43000-43999", site.Name, site.FastCGIPort)
	}
	return nil
}

// phpDefaultsLocked returns the editable version defaults, seeding the store
// from the embedded manifest on first use. Edited defaults are never
// overwritten by later site creation.
func (s *Service) phpDefaultsLocked(ctx context.Context, version string) (model.PHPDefaults, error) {
	defaults, err := s.deps.Store.GetPHPDefaults(ctx, version)
	if err == nil {
		return s.sanitizeStoredPHPDefaultsLocked(ctx, defaults, version)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.PHPDefaults{}, fmt.Errorf("sites: read PHP %s defaults: %w", version, err)
	}
	manifest, err := s.phpCatalog()
	if err != nil {
		return model.PHPDefaults{}, err
	}
	seeded, ok := manifest.DefaultsFor(version)
	if !ok {
		return model.PHPDefaults{}, fmt.Errorf("sites: no PHP defaults for version %q", version)
	}
	defaults = model.PHPDefaults{
		Version:    version,
		Ini:        seeded.Ini,
		Extensions: seeded.Extensions,
	}
	if defaults.Ini == nil {
		defaults.Ini = map[string]string{}
	}
	if defaults.Extensions == nil {
		defaults.Extensions = map[string]bool{}
	}
	if err := s.deps.Store.SavePHPDefaults(ctx, defaults); err != nil {
		return model.PHPDefaults{}, fmt.Errorf("sites: seed PHP %s defaults: %w", version, err)
	}
	return defaults, nil
}

// sanitizeStoredPHPDefaultsLocked removes built-in extension keys from a stored
// defaults row and persists the cleaned row. A row written before a line made
// an extension built-in (for example opcache on 8.5) must not poison every
// site on that line.
func (s *Service) sanitizeStoredPHPDefaultsLocked(ctx context.Context, defaults model.PHPDefaults, version string) (model.PHPDefaults, error) {
	cleaned, dropped, err := s.sanitizePHPOverridesLocked(version, defaults.Extensions)
	if err != nil {
		return model.PHPDefaults{}, err
	}
	if len(dropped) == 0 {
		return defaults, nil
	}
	defaults.Extensions = cleaned
	if err := s.deps.Store.SavePHPDefaults(ctx, defaults); err != nil {
		return model.PHPDefaults{}, fmt.Errorf("sites: sanitize PHP %s defaults: %w", version, err)
	}
	return defaults, nil
}

// phpExtensionDir locates the extension directory inside an installed build.
// Relocatable builds either keep the shared objects directly under
// lib/php/extensions or in exactly one architecture subdirectory.
func phpExtensionDir(build model.RuntimeBuild) (string, error) {
	if build.Backend == model.BackendWSL || runtime.GOOS == "windows" {
		return path.Join(build.Path, "lib", "php", "extensions"), nil
	}
	root := filepath.Join(build.Path, "lib", "php", "extensions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("sites: PHP build %q extension directory: %w", build.ID, err)
	}
	var subdirectories []string
	hasSharedObject := false
	for _, entry := range entries {
		if entry.IsDir() {
			subdirectories = append(subdirectories, entry.Name())
			continue
		}
		if strings.HasSuffix(entry.Name(), ".so") {
			hasSharedObject = true
		}
	}
	switch {
	case hasSharedObject:
		return filepath.Clean(root), nil
	case len(subdirectories) == 1:
		return filepath.Clean(filepath.Join(root, subdirectories[0])), nil
	default:
		return "", fmt.Errorf("sites: PHP build %q has no unambiguous extension directory under %s", build.ID, root)
	}
}

// readPHPBaseIni returns the build's base php.ini when present.
func readPHPBaseIni(build model.RuntimeBuild) ([]byte, error) {
	path := filepath.Join(build.Path, "etc", phpIniFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sites: read PHP build %q base php.ini: %w", build.ID, err)
	}
	return data, nil
}

// hashPHPConfig hashes the generated ini and FPM configuration.
func hashPHPConfig(ini, fpm []byte) string {
	digest := sha256.New()
	digest.Write(ini)
	digest.Write([]byte{0})
	digest.Write(fpm)
	return hex.EncodeToString(digest.Sum(nil))
}

// writePHPFilesLocked writes one immutable generation at sites/<id>/<hash>/.
// The write is atomic: files are created in a temporary directory and the
// directory is renamed into place. Existing complete generations are reused.
func (s *Service) writePHPFilesLocked(siteID string, files phpFiles) (string, error) {
	siteDir, err := s.phpSiteDir(siteID)
	if err != nil {
		return "", err
	}
	generationDir, err := s.phpGenerationDir(siteID, files.Hash)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(siteDir, 0o700); err != nil {
		return "", fmt.Errorf("sites: create PHP config directory: %w", err)
	}
	if phpGenerationComplete(generationDir) {
		return generationDir, nil
	}
	// A partially written generation (for example after an interrupted
	// rename or external tampering) is replaced by a complete one.
	if err := os.RemoveAll(generationDir); err != nil {
		return "", fmt.Errorf("sites: remove incomplete PHP generation: %w", err)
	}
	temporary, err := os.MkdirTemp(siteDir, "gen-")
	if err != nil {
		return "", fmt.Errorf("sites: create temporary PHP generation: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	if err := writePHPGeneration(temporary, files); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, generationDir); err != nil {
		if phpGenerationComplete(generationDir) {
			return generationDir, nil
		}
		return "", fmt.Errorf("sites: publish PHP generation: %w", err)
	}
	return generationDir, nil
}

// writePHPGeneration writes the three generation files with their documented
// modes.
func writePHPGeneration(dir string, files phpFiles) error {
	content := []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{name: phpIniFile, body: files.Ini, mode: 0o600},
		{name: phpFPMFile, body: files.FPM, mode: 0o600},
		{name: phpHealthFile, body: files.Health, mode: 0o644},
	}
	for _, file := range content {
		if err := os.WriteFile(filepath.Join(dir, file.name), file.body, file.mode); err != nil {
			return fmt.Errorf("sites: write %s: %w", file.name, err)
		}
		if err := os.Chmod(filepath.Join(dir, file.name), file.mode); err != nil {
			return fmt.Errorf("sites: set %s permissions: %w", file.name, err)
		}
	}
	return nil
}

// commitPHPFilesLocked copies the active generation to the documented stable
// per-site paths. A running master keeps using its immutable generation file.
func (s *Service) commitPHPFilesLocked(site model.Site) error {
	generationDir, err := s.phpGenerationDir(site.ID, site.PHPConfigHash)
	if err != nil {
		return err
	}
	siteDir, err := s.phpSiteDir(site.ID)
	if err != nil {
		return err
	}
	files := []struct {
		name string
		mode os.FileMode
	}{
		{name: phpIniFile, mode: 0o600},
		{name: phpFPMFile, mode: 0o600},
		{name: phpHealthFile, mode: 0o644},
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(generationDir, file.name))
		if err != nil {
			return fmt.Errorf("sites: read active PHP %s: %w", file.name, err)
		}
		if err := atomicWriteFile(filepath.Join(siteDir, file.name), data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

// commitPHPFilesBestEffortLocked saves the stable copies and records a copy
// failure as a diagnostic event. The healthy master and its immutable
// generation are unaffected, so this never fails a lifecycle operation.
func (s *Service) commitPHPFilesBestEffortLocked(ctx context.Context, site model.Site) {
	if err := s.commitPHPFilesLocked(site); err != nil {
		s.emitEvent(ctx, site.ID, "site.error", fmt.Sprintf("site %s PHP config copy: %v", site.Name, err))
	}
}

// atomicWriteFile replaces path with data atomically.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("sites: create %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "tmp-*.tmp")
	if err != nil {
		return fmt.Errorf("sites: create temporary file for %s: %w", path, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sites: write %s: %w", path, err)
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sites: set %s permissions: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("sites: close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("sites: publish %s: %w", path, err)
	}
	return nil
}

// phpGenerationComplete reports whether every generation file exists.
func phpGenerationComplete(dir string) bool {
	for _, name := range []string{phpIniFile, phpFPMFile, phpHealthFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// phpGenerationDir returns sites/<id>/<hash>, rejecting ids and hashes that
// could escape the sites root.
func (s *Service) phpGenerationDir(siteID, hash string) (string, error) {
	siteDir, err := s.phpSiteDir(siteID)
	if err != nil {
		return "", err
	}
	if len(hash) != sha256.Size*2 {
		return "", fmt.Errorf("sites: invalid PHP config hash for site %q", siteID)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("sites: invalid PHP config hash for site %q", siteID)
	}
	return filepath.Join(siteDir, hash), nil
}

// phpSiteDir returns the per-site config directory, never the sites root.
func (s *Service) phpSiteDir(siteID string) (string, error) {
	home := strings.TrimSpace(s.deps.Config.Home)
	if home == "" {
		return "", errors.New("sites: config home is not set")
	}
	// filepath.IsLocal accepts "." and trailing separators, which would make
	// the site directory the sites root and let RemoveAll wipe every site.
	cleaned := filepath.Clean(siteID)
	if siteID == "" ||
		cleaned == "." ||
		cleaned == ".." ||
		cleaned != siteID ||
		!filepath.IsLocal(cleaned) ||
		strings.ContainsAny(siteID, `/\`) {
		return "", fmt.Errorf("sites: invalid PHP site id %q", siteID)
	}
	root := config.NewPaths(home).Sites
	dir := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel != cleaned {
		return "", fmt.Errorf("sites: invalid PHP site id %q", siteID)
	}
	return dir, nil
}

// phpConfigFilesExistLocked reports whether the site's stored generation is
// complete and can be reused.
func (s *Service) phpConfigFilesExistLocked(site model.Site) bool {
	if site.PHPConfigHash == "" {
		return false
	}
	dir, err := s.phpGenerationDir(site.ID, site.PHPConfigHash)
	if err != nil {
		return false
	}
	return phpGenerationComplete(dir)
}

// validatePHPFilesLocked runs both configuration validators against written
// generation files.
func (s *Service) validatePHPFilesLocked(ctx context.Context, site model.Site, build model.RuntimeBuild, generationDir string) error {
	if site.Backend == model.BackendWSL || build.Backend == model.BackendWSL || runtime.GOOS == "windows" {
		return nil
	}
	iniPath := filepath.Join(generationDir, phpIniFile)
	fpmPath := filepath.Join(generationDir, phpFPMFile)
	phpBin := filepath.Join(build.Path, "bin", "php")
	fpmBin := filepath.Join(build.Path, "sbin", "php-fpm")
	if err := s.deps.PHP.Lint(ctx, phpBin, iniPath); err != nil {
		return fmt.Errorf("sites: validate php.ini for %q: %w", site.Name, err)
	}
	if err := s.deps.PHP.ValidateFPM(ctx, fpmBin, fpmPath, iniPath); err != nil {
		return fmt.Errorf("sites: validate php-fpm.conf for %q: %w", site.Name, err)
	}
	return nil
}

// saveAppliedPHPConfigLocked records the applied per-site configuration
// snapshot.
func (s *Service) saveAppliedPHPConfigLocked(ctx context.Context, site model.Site) error {
	return s.deps.Store.SaveSitePHPConfig(ctx, model.SitePHPConfig{
		SiteID:     site.ID,
		Ini:        cloneStringMap(site.PHPIni),
		Extensions: cloneBoolMap(site.PHPExtensions),
		ConfigHash: site.PHPConfigHash,
		AppliedAt:  s.currentTime(),
	})
}

// removePHPConfigLocked removes only sites/<id>, never the sites root.
func (s *Service) removePHPConfigLocked(siteID string) error {
	dir, err := s.phpSiteDir(siteID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("sites: remove PHP config directory: %w", err)
	}
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// comparePHPVersions compares dotted numeric versions component by component.
func comparePHPVersions(a, b string) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// supportedPHPLine reports whether version is one of the shipped PHP lines.
func supportedPHPLine(version string) bool {
	switch version {
	case "8.2", "8.3", "8.4", "8.5":
		return true
	default:
		return false
	}
}

// phpBuildInstall usable? helper below keeps catalog imported for build
// selection; the function is deliberately small.
func phpBuildLineMatches(build model.RuntimeBuild, line string) bool {
	parsed, err := catalog.ParseID(build.ID)
	if err != nil || parsed.Kind != "php" {
		return false
	}
	return catalog.LineOf(parsed.Version) == line && catalog.LineOf(build.Version) == line
}
