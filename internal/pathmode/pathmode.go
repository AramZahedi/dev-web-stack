// Package pathmode translates and validates site source paths between the
// Windows-visible namespace and the Linux/WSL backend namespace.
package pathmode

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// WSLProjectsRoot is the fixed managed projects root inside the devstack WSL
// distro. The agent owns the same directory beneath its /data/devstack root.
const WSLProjectsRoot = "/data/devstack/projects"

var (
	// ErrInvalidSourceMode is returned for a mode the platform cannot use.
	ErrInvalidSourceMode = errors.New("pathmode: invalid source mode")
	// ErrInvalidPath is returned for an unsafe or unusable source path.
	ErrInvalidPath = errors.New("pathmode: invalid source path")
	// ErrOutsideProjectsRoot is returned when a site path is not a strict
	// descendant of the managed projects root for its source mode.
	ErrOutsideProjectsRoot = errors.New("pathmode: project folder is outside the managed projects root")

	// distroPattern is the WSL distro-name shape used anywhere a name becomes
	// part of a UNC path or a wsl.exe argument.
	distroPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ManagedProjectsRoot resolves the canonical projects root for a source mode.
// Windows and local mode use the configured projects_root, falling back to
// <home>/projects. WSL always uses WSLProjectsRoot.
func ManagedProjectsRoot(mode model.SourceMode, projectsRoot, home string) (string, error) {
	switch mode {
	case model.SourceWSL:
		return WSLProjectsRoot, nil
	case model.SourceWindows, model.SourceLocal:
		if value := strings.TrimSpace(projectsRoot); value != "" {
			return filepath.Clean(value), nil
		}
		if value := strings.TrimSpace(home); value != "" {
			return filepath.Join(value, "projects"), nil
		}
		return "", fmt.Errorf("%w: projects root is not configured", ErrOutsideProjectsRoot)
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidSourceMode, mode)
	}
}

// ValidateManagedProjectPath confirms that a site path is a strict descendant
// of the managed projects root for its source mode. WSL uses the backend path
// because the agent owns symlink resolution there; Windows and local modes use
// the host path and reject a resolved escape.
func ValidateManagedProjectPath(mode model.SourceMode, hostPath, backendPath, root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("%w: projects root is empty", ErrOutsideProjectsRoot)
	}
	switch mode {
	case model.SourceWSL:
		target := strings.TrimSpace(backendPath)
		if !strictlyContains(root, target, false) {
			return fmt.Errorf("%w: %q must be a subfolder of %q", ErrOutsideProjectsRoot, target, root)
		}
		return nil
	case model.SourceWindows, model.SourceLocal:
		target := strings.TrimSpace(hostPath)
		windows := runtime.GOOS == "windows"
		if strictlyContains(root, target, windows) {
			if err := ValidateStrictlyContainedPath(root, target, windows); err != nil {
				return fmt.Errorf("%w: %v", ErrOutsideProjectsRoot, err)
			}
			return nil
		}
		// Local mode canonicalizes the project path before it is persisted, so
		// a configured projects_root that is itself a symlink or junction would
		// otherwise look like an escape. Compare the resolved forms as well;
		// the target must still be a strict descendant of the resolved root.
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("%w: %q must be a subfolder of %q", ErrOutsideProjectsRoot, target, root)
		}
		resolvedTarget, err := resolveExistingPrefix(target)
		if err != nil || !strictlyContains(resolvedRoot, resolvedTarget, windows) {
			return fmt.Errorf("%w: %q must be a subfolder of %q", ErrOutsideProjectsRoot, target, root)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidSourceMode, mode)
	}
}

// InferSourceMode derives the source mode for a persisted site row that has no
// explicit mode. Drive paths are Windows, UNC paths are WSL, and everything else
// follows the platform default. It mirrors the doctor's legacy-row inference so
// start/reconcile and diagnostics agree on the allowed root.
func InferSourceMode(hostPath, platform string) model.SourceMode {
	host := strings.TrimSpace(hostPath)
	if len(host) >= 2 && host[1] == ':' {
		c := host[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return model.SourceWindows
		}
	}
	if strings.HasPrefix(strings.ReplaceAll(host, "/", `\`), `\\`) {
		return model.SourceWSL
	}
	if strings.TrimSpace(platform) == "windows" {
		return model.SourceWindows
	}
	return model.SourceLocal
}

// strictlyContains is ContainsPath plus an explicit rejection of the root
// itself, independent of trailing separators or case.
func strictlyContains(root, target string, windows bool) bool {
	if !ContainsPath(root, target, windows) {
		return false
	}
	clean := func(value string) string {
		value = strings.TrimSpace(value)
		if windows {
			value = strings.ReplaceAll(value, `\`, "/")
		}
		value = path.Clean(value)
		if windows {
			value = strings.ToLower(value)
		}
		return strings.TrimRight(value, "/")
	}
	return clean(root) != clean(target)
}

// ValidateSourceMode enforces the platform matrix: Linux only local; Windows
// only windows/wsl.
func ValidateSourceMode(mode model.SourceMode, platform string) error {
	switch platform {
	case "linux":
		if mode == model.SourceLocal {
			return nil
		}
	case "windows":
		if mode == model.SourceWindows || mode == model.SourceWSL {
			return nil
		}
	}
	return fmt.Errorf("%w: %q on %s", ErrInvalidSourceMode, mode, platform)
}

// DefaultSourceMode selects the documented default for a new site.
func DefaultSourceMode(kind model.SiteKind, platform string) model.SourceMode {
	if platform != "windows" {
		return model.SourceLocal
	}
	if kind == model.SiteStatic {
		return model.SourceWindows
	}
	return model.SourceWSL
}

// TranslateSource converts one source path into the Windows-visible host path
// and the Linux/WSL backend path. It rejects device paths, arbitrary UNC
// shares, alternate data streams, and any ".." component.
func TranslateSource(mode model.SourceMode, input string) (hostPath, backendPath string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	if hasParentComponent(input) {
		return "", "", fmt.Errorf("%w: %q contains a parent component", ErrInvalidPath, input)
	}
	switch mode {
	case model.SourceLocal:
		if !strings.HasPrefix(input, "/") || strings.HasPrefix(input, "//") {
			return "", "", fmt.Errorf("%w: local path %q must be an absolute Linux path", ErrInvalidPath, input)
		}
		cleaned := path.Clean(input)
		if cleaned == "/" {
			return "", "", fmt.Errorf("%w: local path must not be the filesystem root", ErrInvalidPath)
		}
		return cleaned, cleaned, nil
	case model.SourceWindows:
		return translateWindows(input)
	case model.SourceWSL:
		return translateWSL(input)
	default:
		return "", "", fmt.Errorf("%w: %q", ErrInvalidSourceMode, mode)
	}
}

// TranslateSourceForDistro applies the managed-distro boundary in addition to
// the lexical source path validation. The legacy alias is canonicalized to the
// localhost UNC form only after its distro matches.
func TranslateSourceForDistro(mode model.SourceMode, input, configuredDistro string) (string, string, error) {
	host, backend, err := TranslateSource(mode, input)
	if err != nil {
		return "", "", err
	}
	if mode != model.SourceWSL {
		return host, backend, nil
	}
	configuredDistro = strings.TrimSpace(configuredDistro)
	if configuredDistro == "" {
		return "", "", fmt.Errorf("%w: managed WSL distro is not configured", ErrInvalidPath)
	}
	if !distroPattern.MatchString(configuredDistro) {
		return "", "", fmt.Errorf("%w: invalid managed WSL distro %q", ErrInvalidPath, configuredDistro)
	}
	if strings.HasPrefix(strings.TrimSpace(input), "/") && !strings.HasPrefix(strings.TrimSpace(input), "//") {
		host = `\\wsl.localhost\` + configuredDistro + `\` + strings.ReplaceAll(strings.TrimPrefix(backend, "/"), "/", `\`)
		return host, backend, nil
	}
	const prefix = `\\wsl.localhost\`
	if !strings.HasPrefix(strings.ToLower(host), strings.ToLower(prefix)) {
		return "", "", fmt.Errorf("%w: source is not in the managed WSL distro", ErrInvalidPath)
	}
	distro := strings.SplitN(host[len(prefix):], `\`, 2)[0]
	if !strings.EqualFold(distro, configuredDistro) {
		return "", "", fmt.Errorf("%w: WSL distro %q does not match configured distro %q", ErrInvalidPath, distro, configuredDistro)
	}
	return host, backend, nil
}

// ValidateCreatedPath rejects dot-prefixed basenames for directories created by
// devstack. Dot files inside an existing project tree remain unaffected.
func ValidateCreatedPath(value string) error {
	value = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/"), "/")
	if value == "" || hasParentComponent(value) {
		return fmt.Errorf("%w: invalid destination path", ErrInvalidPath)
	}
	base := value[strings.LastIndex(value, "/")+1:]
	if base == "" || strings.HasPrefix(base, ".") {
		return fmt.Errorf("%w: created directory basename must not start with a dot", ErrInvalidPath)
	}
	return nil
}

// ContainsPath reports whether target is root or a descendant of root. Windows
// paths use case-insensitive comparison and a component boundary. Backslashes
// are path separators only on Windows; on Linux they are literal filename
// characters, so a "projects\\app" alias must not look contained.
func ContainsPath(root, target string, windows bool) bool {
	clean := func(value string) string {
		value = strings.TrimSpace(value)
		if windows {
			value = strings.ReplaceAll(value, `\`, "/")
		}
		value = path.Clean(value)
		if windows {
			value = strings.ToLower(value)
		}
		return strings.TrimRight(value, "/")
	}
	r, t := clean(root), clean(target)
	if r == "" || t == "" || r == "." || t == "." {
		return false
	}
	return t == r || strings.HasPrefix(t, r+"/")
}

// ValidateContainedPath confirms lexical containment and resolves symlinks or
// Windows reparse-point ancestors before allowing an operation on a path. The
// root itself is allowed; use ValidateStrictlyContainedPath when the operation
// requires a descendant.
func ValidateContainedPath(root, target string, windows bool) error {
	return validateContainedPath(root, target, windows, false)
}

// ValidateStrictlyContainedPath is ValidateContainedPath plus an explicit
// rejection of the root itself, in both its lexical and resolved spelling. It
// closes aliases such as a junction inside the root that points back at the
// root, which would otherwise register the root as a site or copy destination.
func ValidateStrictlyContainedPath(root, target string, windows bool) error {
	return validateContainedPath(root, target, windows, true)
}

func validateContainedPath(root, target string, windows, strict bool) error {
	contained := func(base, candidate string) bool {
		if strict {
			return strictlyContains(base, candidate, windows)
		}
		return ContainsPath(base, candidate, windows)
	}
	if !contained(root, target) {
		if strict {
			return fmt.Errorf("%w: %q is not a strict descendant of %q", ErrInvalidPath, target, root)
		}
		return fmt.Errorf("%w: %q is outside %q", ErrInvalidPath, target, root)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("%w: resolve root %q: %v", ErrInvalidPath, root, err)
	}
	resolvedTarget, err := resolveExistingPrefix(target)
	if err != nil {
		return fmt.Errorf("%w: resolve target %q: %v", ErrInvalidPath, target, err)
	}
	if !contained(resolvedRoot, resolvedTarget) {
		return fmt.Errorf("%w: resolved path %q escapes %q", ErrInvalidPath, target, root)
	}
	return nil
}

func resolveExistingPrefix(value string) (string, error) {
	current := value
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func translateWindows(input string) (string, string, error) {
	slashed := strings.ReplaceAll(input, "/", `\`)
	if strings.HasPrefix(slashed, `\\`) || strings.HasPrefix(slashed, `\Device\`) {
		return "", "", fmt.Errorf("%w: device or UNC path %q is not a Windows drive path", ErrInvalidPath, input)
	}
	if len(slashed) < 4 || slashed[1] != ':' || slashed[2] != '\\' {
		return "", "", fmt.Errorf("%w: %q must be an absolute drive path", ErrInvalidPath, input)
	}
	drive := slashed[0]
	if !((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) {
		return "", "", fmt.Errorf("%w: %q has no drive letter", ErrInvalidPath, input)
	}
	rest := strings.TrimPrefix(slashed[3:], `\`)
	rest = strings.TrimRight(rest, `\`)
	if rest == "" {
		return "", "", fmt.Errorf("%w: %q is a drive root", ErrInvalidPath, input)
	}
	for _, component := range strings.Split(rest, `\`) {
		if component == "" || component == "." || component == ".." {
			return "", "", fmt.Errorf("%w: %q has an invalid component", ErrInvalidPath, input)
		}
		if strings.Contains(component, ":") {
			return "", "", fmt.Errorf("%w: %q contains an alternate data stream", ErrInvalidPath, input)
		}
	}
	upper := strings.ToUpper(string(drive))
	host := fmt.Sprintf(`%s:\%s`, upper, rest)
	backend := "/mnt/" + strings.ToLower(upper) + "/" + strings.ReplaceAll(rest, `\`, "/")
	return host, backend, nil
}

func translateWSL(input string) (string, string, error) {
	if strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "//") {
		// A backslash is a legal Linux filename character, but the Windows UNC
		// host spelling would read it as a separator. Reject it instead of
		// persisting a site whose host and backend paths disagree.
		if strings.ContainsRune(input, '\\') {
			return "", "", fmt.Errorf("%w: %q contains a backslash; use a WSL UNC path to separate components", ErrInvalidPath, input)
		}
		cleaned := path.Clean(input)
		if cleaned == "/" {
			return "", "", fmt.Errorf("%w: WSL path must not be the filesystem root", ErrInvalidPath)
		}
		for _, component := range strings.Split(strings.Trim(cleaned, "/"), "/") {
			if component == "" || component == "." || component == ".." {
				return "", "", fmt.Errorf("%w: %q has an invalid component", ErrInvalidPath, input)
			}
		}
		host := `\\wsl.localhost\devstack\` + strings.ReplaceAll(strings.TrimPrefix(cleaned, "/"), "/", `\`)
		return host, cleaned, nil
	}
	slashed := strings.ReplaceAll(input, "/", `\`)
	const localhost = `\\wsl.localhost\`
	const legacy = `\\wsl$\`
	var rest string
	switch {
	case strings.HasPrefix(strings.ToLower(slashed), strings.ToLower(localhost)):
		rest = slashed[len(localhost):]
	case strings.HasPrefix(strings.ToLower(slashed), strings.ToLower(legacy)):
		rest = slashed[len(legacy):]
	default:
		return "", "", fmt.Errorf("%w: %q is not a managed WSL UNC path", ErrInvalidPath, input)
	}
	parts := strings.Split(rest, `\`)
	if len(parts) < 2 || parts[0] == "" {
		return "", "", fmt.Errorf("%w: %q must name a distro and a path inside it", ErrInvalidPath, input)
	}
	distro := parts[0]
	if !distroPattern.MatchString(distro) {
		return "", "", fmt.Errorf("%w: invalid WSL distro %q", ErrInvalidPath, distro)
	}
	inside := strings.Join(parts[1:], "/")
	inside = strings.Trim(inside, "/")
	if inside == "" {
		return "", "", fmt.Errorf("%w: %q names a distro root", ErrInvalidPath, input)
	}
	for _, component := range strings.Split(inside, "/") {
		if component == "" || component == "." || component == ".." {
			return "", "", fmt.Errorf("%w: %q has an invalid component", ErrInvalidPath, input)
		}
	}
	host := `\\wsl.localhost\` + distro + `\` + strings.ReplaceAll(inside, "/", `\`)
	return host, "/" + inside, nil
}

func hasParentComponent(input string) bool {
	for _, component := range strings.FieldsFunc(input, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

// ProxyDocumentRoot derives the Windows-visible absolute document root used by
// the Go proxy for PHP static-file and front-controller checks.
func ProxyDocumentRoot(site model.Site) (string, error) {
	if strings.TrimSpace(site.HostPath) == "" {
		return "", fmt.Errorf("%w: host path is empty", ErrInvalidPath)
	}
	root := strings.TrimSpace(site.DocumentRoot)
	if root == "" || root == "." {
		return site.HostPath, nil
	}
	slashed := strings.ReplaceAll(root, `\`, "/")
	if strings.HasPrefix(slashed, "/") || (len(slashed) > 1 && slashed[1] == ':') {
		return "", fmt.Errorf("%w: document root %q must be relative", ErrInvalidPath, root)
	}
	for _, component := range strings.Split(slashed, "/") {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("%w: document root %q has an invalid component", ErrInvalidPath, root)
		}
		// ':' is a legal Linux filename character. It is only a stream
		// separator in Windows source mode (and for legacy rows with no mode).
		if strings.Contains(component, ":") &&
			site.SourceMode != model.SourceWSL && site.SourceMode != model.SourceLocal {
			return "", fmt.Errorf("%w: document root %q contains a stream name", ErrInvalidPath, root)
		}
	}
	host := strings.TrimRight(site.HostPath, `\/`)
	if site.SourceMode == model.SourceLocal {
		return path.Join(host, slashed), nil
	}
	return host + `\` + strings.ReplaceAll(slashed, "/", `\`), nil
}
