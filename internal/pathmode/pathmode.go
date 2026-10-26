// Package pathmode translates and validates site source paths between the
// Windows-visible namespace and the Linux/WSL backend namespace.
package pathmode

import (
	"errors"
	"fmt"
	"path"
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
