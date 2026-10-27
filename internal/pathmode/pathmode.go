// Package pathmode translates and validates site source paths between the
// Windows-visible namespace and the Linux/WSL backend namespace.
package pathmode

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var (
	// ErrInvalidSourceMode is returned for a mode the platform cannot use.
	ErrInvalidSourceMode = errors.New("pathmode: invalid source mode")
	// ErrInvalidPath is returned for an unsafe or unusable source path.
	ErrInvalidPath = errors.New("pathmode: invalid source path")
)

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
// paths use case-insensitive comparison and a component boundary.
func ContainsPath(root, target string, windows bool) bool {
	clean := func(value string) string {
		value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
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
// Windows reparse-point ancestors before allowing an operation on a path.
func ValidateContainedPath(root, target string, windows bool) error {
	if !ContainsPath(root, target, windows) {
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
	if !ContainsPath(resolvedRoot, resolvedTarget, windows) {
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
	if drive != 'C' && drive != 'c' {
		return "", "", fmt.Errorf("%w: only the C: drive is supported, got %q", ErrInvalidPath, drive)
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
	inside := strings.Join(parts[1:], "/")
	inside = strings.Trim(inside, "/")
	if inside == "" {
		return "", "", fmt.Errorf("%w: %q names a distro root", ErrInvalidPath, input)
	}
	for _, component := range strings.Split(inside, "/") {
		if component == "" || component == "." || component == ".." {
			return "", "", fmt.Errorf("%w: %q has an invalid component", ErrInvalidPath, input)
		}
		if strings.Contains(component, ":") {
			return "", "", fmt.Errorf("%w: %q contains an alternate data stream", ErrInvalidPath, input)
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
	if root == "" {
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
		if strings.Contains(component, ":") {
			return "", fmt.Errorf("%w: document root %q contains a stream name", ErrInvalidPath, root)
		}
	}
	host := strings.TrimRight(site.HostPath, `\/`)
	if site.SourceMode == model.SourceLocal {
		return path.Join(host, slashed), nil
	}
	return host + `\` + strings.ReplaceAll(slashed, "/", `\`), nil
}
