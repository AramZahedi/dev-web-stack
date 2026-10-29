package tooling

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// currentEUID is a seam so tests can exercise the root refusal without
// changing process credentials.
var currentEUID = os.Geteuid

func init() {
	// The site service cannot import tooling directly because tooling consumes
	// sites for the PHP environment types. Installing the resolver here keeps
	// the production global-default rules in ResolveNodeDefault.
	sites.NodeDefaultResolver = ResolveNodeDefault
}

// EnvDeps carries the runtime inventory and generated site configuration used
// to build a shell environment.
type EnvDeps struct {
	Paths   config.Paths
	Builds  []model.RuntimeBuild
	PHPInfo *sites.PHPInfo
	// NodeDefault is the resolved machine-wide default used when a site has no
	// pin. When nil, the newest installed Node build acts as the default.
	NodeDefault *model.RuntimeBuild
	BasePATH    string
}

// SiteEnvironment is a fully resolved site shell environment.
type SiteEnvironment struct {
	Site      model.Site
	Values    map[string]string
	PATH      string
	ShimDir   string
	PHPIni    string
	PHPBuild  *model.RuntimeBuild
	NodeBuild *model.RuntimeBuild
}

// ResolveSite resolves a site by explicit name/id or by matching cwd to a
// registered host path, including subdirectories. An explicit ref always wins.
func ResolveSite(sitesList []model.Site, ref, cwd string) (model.Site, error) {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		for _, site := range sitesList {
			if site.ID == ref || site.Name == ref {
				return site, nil
			}
		}
		return model.Site{}, fmt.Errorf("tooling: site %q is not registered; registered sites: %s", ref, siteListSummary(sitesList))
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return model.Site{}, fmt.Errorf("tooling: resolve current directory: %w", err)
		}
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return model.Site{}, fmt.Errorf("tooling: resolve current directory: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}

	matches := make([]model.Site, 0, 1)
	seen := make(map[string]struct{})
	for _, site := range sitesList {
		host := strings.TrimSpace(site.HostPath)
		if host == "" {
			continue
		}
		hostAbs, err := filepath.Abs(host)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(hostAbs); err == nil {
			hostAbs = resolved
		}
		if !pathWithin(hostAbs, absolute) {
			continue
		}
		if _, ok := seen[site.ID]; ok {
			continue
		}
		seen[site.ID] = struct{}{}
		matches = append(matches, site)
	}

	switch len(matches) {
	case 0:
		return model.Site{}, fmt.Errorf("tooling: no registered site matches %s; registered sites: %s", absolute, siteListSummary(sitesList))
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, site := range matches {
			names = append(names, site.Name)
		}
		sort.Strings(names)
		return model.Site{}, fmt.Errorf("tooling: %s belongs to multiple sites (%s); pass --site <name>", absolute, strings.Join(names, ", "))
	}
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func siteListSummary(list []model.Site) string {
	if len(list) == 0 {
		return "none are registered"
	}
	parts := make([]string, 0, len(list))
	for _, site := range list {
		parts = append(parts, fmt.Sprintf("%s (%s)", site.Name, site.HostPath))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func validateSiteID(id string) error {
	if id == "" || filepath.Clean(id) != id || !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid site id %q", id)
	}
	return nil
}

// validateEnvironment rejects names that cannot be exported safely and values
// that contain NUL. Callers render these entries into shell scripts (site shims
// and shell-init), so an unvalidated name such as "X; command" would become
// executable shell syntax rather than an environment assignment.
func validateEnvironment(env map[string]string) error {
	for key, value := range env {
		if !envNamePattern.MatchString(key) {
			return fmt.Errorf("invalid environment variable name %q", key)
		}
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("environment variable %q contains a NUL byte", key)
		}
	}
	return nil
}

// BuildSiteEnv resolves the site's PHP and Node builds, writes site-scoped
// shims, and returns the environment for a shell or exec command. It never
// runs as root and never falls back to a different PHP for a site.
func BuildSiteEnv(site model.Site, deps EnvDeps) (SiteEnvironment, error) {
	if currentEUID() == 0 {
		return SiteEnvironment{}, errors.New("tooling: refusing to run a project shell as root; run devstack as the installing user")
	}
	if strings.TrimSpace(site.HostPath) == "" {
		return SiteEnvironment{}, fmt.Errorf("tooling: site %q has no project directory", site.Name)
	}
	if err := validateSiteID(site.ID); err != nil {
		return SiteEnvironment{}, fmt.Errorf("tooling: site %q: %w", site.Name, err)
	}

	if err := validateEnvironment(site.Env); err != nil {
		return SiteEnvironment{}, fmt.Errorf("tooling: site %q: %w", site.Name, err)
	}
	env := SiteEnvironment{Site: site, ShimDir: SiteShimDir(deps.Paths, site.Name)}
	values := make(map[string]string, len(site.Env)+8)
	for key, value := range site.Env {
		values[key] = value
	}

	var phpBuild *model.RuntimeBuild
	switch site.Kind {
	case model.SitePHP:
		build, err := ResolvePHPBuild(deps.Builds, site.PHPBuild, site.PHPVersion)
		if err != nil {
			return SiteEnvironment{}, err
		}
		phpBuild = &build
		hash := strings.TrimSpace(site.PHPConfigHash)
		if deps.PHPInfo != nil && strings.TrimSpace(deps.PHPInfo.ConfigHash) != "" {
			hash = strings.TrimSpace(deps.PHPInfo.ConfigHash)
		}
		if err := validateHash(hash); err != nil {
			return SiteEnvironment{}, fmt.Errorf("tooling: site %q PHP configuration hash: %w", site.Name, err)
		}
		ini := filepath.Join(deps.Paths.Sites, site.ID, hash, "php.ini")
		if info, err := os.Stat(ini); err != nil || !info.Mode().IsRegular() {
			return SiteEnvironment{}, fmt.Errorf("tooling: site %q php.ini is not written at %s; run `devstack site restart %s` to apply its PHP configuration", site.Name, ini, site.Name)
		}
		env.PHPIni = ini
		values["PHPRC"] = ini
	case model.SiteStatic, model.SiteNode:
	default:
		return SiteEnvironment{}, fmt.Errorf("tooling: unsupported site kind %q", site.Kind)
	}
	nodeBuild, nodeSource, err := resolveShellNodeBuild(site, deps)
	if err != nil {
		return SiteEnvironment{}, err
	}
	env.PHPBuild = phpBuild
	env.NodeBuild = nodeBuild
	if nodeBuild != nil {
		values["DEVSTACK_NODE_VERSION"] = nodeBuild.Version
		values["DEVSTACK_NODE_SOURCE"] = nodeSource
	}

	if err := ensureSiteShims(env.ShimDir, phpBuild, nodeBuild, env.PHPIni); err != nil {
		return SiteEnvironment{}, err
	}

	values["DEVSTACK_SITE"] = site.Name
	values["DEVSTACK_HOME"] = deps.Paths.Home
	values["DEVSTACK_PROJECT"] = site.HostPath

	basePATH := deps.BasePATH
	if basePATH == "" {
		basePATH = os.Getenv("PATH")
	}
	env.PATH = composePATH(deps.Paths, env.ShimDir, basePATH)
	values["PATH"] = env.PATH
	env.Values = values
	return env, nil
}

// resolveShellNodeBuild applies the shell/exec order: the exact site pin wins,
// then the global default. A static build profile is deliberately ignored; it
// only selects the Node runtime for build execution. PHP sites without any Node
// toolchain still get a usable shell, and Node-kind sites require a pin.
func resolveShellNodeBuild(site model.Site, deps EnvDeps) (*model.RuntimeBuild, string, error) {
	pin := strings.TrimSpace(site.NodeVersion)
	if pin != "" {
		build, err := ResolveNodeBuild(deps.Builds, pin)
		if err != nil {
			return nil, "", err
		}
		return &build, model.NodeSourceSite, nil
	}
	if site.Kind == model.SiteNode {
		return nil, "", fmt.Errorf("%w: Node version is empty", ErrRuntimeMissing)
	}
	if deps.NodeDefault != nil {
		if deps.NodeDefault.Kind != "" && deps.NodeDefault.Kind != "node" {
			return nil, "", fmt.Errorf("tooling: global Node default %q is not a Node build", deps.NodeDefault.ID)
		}
		if strings.TrimSpace(deps.NodeDefault.Version) == "" {
			return nil, "", errors.New("tooling: global Node default has no version")
		}
		build := *deps.NodeDefault
		return &build, model.NodeSourceGlobal, nil
	}
	if build, ok := newestInstalledNodeBuild(deps.Builds); ok {
		return &build, model.NodeSourceGlobal, nil
	}
	return nil, "", nil
}

// newestInstalledNodeBuild mirrors the no-setting fallback of the global Node
// default: the newest installed compatible build.
func newestInstalledNodeBuild(builds []model.RuntimeBuild) (model.RuntimeBuild, bool) {
	var best model.RuntimeBuild
	found := false
	for _, build := range builds {
		if build.Kind != "node" || !isCompatibleBackend(build.Backend) {
			continue
		}
		if !found || compareVersions(build.Version, best.Version) > 0 {
			best, found = build, true
		}
	}
	return best, found
}

func validateHash(hash string) error {
	if len(hash) != 64 {
		return errors.New("must be 64 hex characters")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return errors.New("must be 64 hex characters")
	}
	return nil
}

// ensureSiteShims writes wrappers for the site's selected runtimes and removes
// stale shims from a previous selection.
func ensureSiteShims(dir string, phpBuild, nodeBuild *model.RuntimeBuild, iniPath string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tooling: create site shim directory: %w", err)
	}
	expected := map[string]bool{}
	if phpBuild != nil {
		for _, name := range []string{"php", "php-config", "phpize"} {
			binary := filepath.Join(phpBuild.Path, "bin", name)
			info, err := os.Stat(binary)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("tooling: inspect PHP binary %s: %w", binary, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			script := "#!/bin/sh\nexec " + shellQuote(binary) + " \"$@\"\n"
			if name == "php" {
				script = "#!/bin/sh\nexec " + shellQuote(binary) + " -c " + shellQuote(iniPath) + " \"$@\"\n"
			}
			if err := writeScriptAtomic(filepath.Join(dir, name), script); err != nil {
				return err
			}
			expected[name] = true
		}
	}
	if nodeBuild != nil {
		for _, name := range []string{"node", "npm", "npx"} {
			binary := filepath.Join(nodeBuild.Path, "bin", name)
			info, err := os.Stat(binary)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("tooling: inspect Node binary %s: %w", binary, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			script := "#!/bin/sh\nexec " + shellQuote(binary) + " \"$@\"\n"
			if err := writeScriptAtomic(filepath.Join(dir, name), script); err != nil {
				return err
			}
			expected[name] = true
		}
	}
	for _, name := range []string{"php", "php-config", "phpize", "node", "npm", "npx"} {
		if expected[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("tooling: remove stale site shim %s: %w", name, err)
		}
	}
	return nil
}

// composePATH prepends the site shim, the managed Composer wrapper, and the
// global PHP shims to the inherited PATH, preserving order and removing
// duplicates.
func composePATH(paths config.Paths, siteShim, base string) string {
	candidates := []string{siteShim}
	if info, err := os.Stat(ComposerWrapperPath(paths)); err == nil && info.Mode().IsRegular() {
		candidates = append(candidates, ComposerBinDir(paths))
	}
	if info, err := os.Stat(filepath.Join(ShimDir(paths), "php")); err == nil && info.Mode().IsRegular() {
		candidates = append(candidates, ShimDir(paths))
	}
	seen := make(map[string]struct{})
	parts := make([]string, 0, len(candidates)+4)
	for _, candidate := range append(candidates, filepath.SplitList(base)...) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		parts = append(parts, candidate)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// Environment merges values over base, replacing existing keys and appending
// new ones deterministically.
func Environment(base []string, values map[string]string) []string {
	env := make([]string, 0, len(base)+len(values))
	seen := make(map[string]struct{}, len(base))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			continue
		}
		if _, replace := values[key]; replace {
			if _, already := seen[key]; already {
				continue
			}
			env = append(env, key+"="+values[key])
			seen[key] = struct{}{}
			continue
		}
		env = append(env, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// ShellExports renders sorted POSIX export statements for shell-init.
func ShellExports(env SiteEnvironment) string {
	keys := make([]string, 0, len(env.Values))
	for key := range env.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		// Values normally come from BuildSiteEnv/WindowsSiteEnvironment, which
		// validate names. Keep this renderer fail-closed as well because callers
		// can construct SiteEnvironment directly.
		if !envNamePattern.MatchString(key) {
			continue
		}
		builder.WriteString("export ")
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(shellQuote(env.Values[key]))
		builder.WriteString("\n")
	}
	return builder.String()
}
