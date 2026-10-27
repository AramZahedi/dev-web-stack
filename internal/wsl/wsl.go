// Package wsl isolates the wsl.exe process boundary: distro discovery, path
// translation, agent-side path validation, verified copies, and the
// localhost-forwarding probe.
package wsl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
)

var (
	// ErrInvalidPath is returned when a WSL path fails validation.
	ErrInvalidPath = errors.New("wsl: invalid path")
	// ErrDestinationNotEmpty is returned when a copy target already has data.
	ErrDestinationNotEmpty = errors.New("wsl: destination is not empty")
	// ErrLocalhostForwarding is returned when WSL cannot reach the Windows
	// daemon through the host gateway.
	ErrLocalhostForwarding = errors.New("wsl: localhost forwarding is unavailable")
)

// Runner executes wsl.exe. Implementations are injected so unit tests never
// require a real WSL installation.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner runs the real wsl.exe.
type ExecRunner struct{}

// Run invokes wsl.exe with args and returns stdout.
func (ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("wsl: no arguments")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, fmt.Errorf("wsl: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return out, fmt.Errorf("wsl: %w", err)
	}
	return out, nil
}

// ParseDistroList decodes `wsl -l -q` output. wsl.exe emits UTF-16LE with a
// BOM on Windows, but some builds emit UTF-8; accept both.
func ParseDistroList(raw []byte) []string {
	text := string(raw)
	if len(raw) >= 2 && ((raw[0] == 0xff && raw[1] == 0xfe) || (raw[1] == 0 && raw[0] != 0)) {
		text = decodeUTF16LE(raw)
	}
	text = strings.TrimPrefix(text, "\ufeff")
	var out []string
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(strings.Trim(line, "\x00"))
		if line == "" || line == "NAME" || strings.EqualFold(line, "STATE") || strings.EqualFold(line, "VERSION") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func decodeUTF16LE(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		raw = raw[2:]
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

// DistroPath maps a Windows path to the path the WSL agent sees.
func DistroPath(windowsPath string) (string, error) {
	path := strings.TrimSpace(windowsPath)
	mode := model.SourceWindows
	if strings.HasPrefix(strings.ToLower(path), `\\wsl.localhost\`) || strings.HasPrefix(strings.ToLower(path), `\\wsl$\`) {
		mode = model.SourceWSL
	}
	_, backend, err := pathmode.TranslateSource(mode, path)
	if err != nil {
		return "", err
	}
	return backend, nil
}

// ValidatedPath is the canonical path returned by agent-side validation.
type ValidatedPath struct {
	Path   string
	Exists bool
}

const validateScript = `set -eu
p="$1"
case "$p" in /*) ;; *) exit 2 ;; esac
real=$(realpath -e -- "$p") || exit 3
[ "$real" = "$p" ] || exit 4
cur=""
oldIFS=$IFS
IFS=/
for part in $p; do
  [ -z "$part" ] && continue
  cur="$cur/$part"
  [ -L "$cur" ] && exit 5
done
IFS=$oldIFS
printf '%s\n' "$real"
`

// ValidateWSLPath runs lstat/readlink/realpath inside the distro. Any symlink
// component, missing path, or non-canonical path is rejected.
func ValidateWSLPath(ctx context.Context, runner Runner, distro, path string) (ValidatedPath, error) {
	if runner == nil {
		return ValidatedPath{}, errors.New("wsl: nil runner")
	}
	if !strings.HasPrefix(path, "/") {
		return ValidatedPath{}, fmt.Errorf("%w: %q must be absolute", ErrInvalidPath, path)
	}
	out, err := runner.Run(ctx, "-d", distro, "--", "bash", "-lc", validateScript, "devstack-validate", path)
	if err != nil {
		return ValidatedPath{}, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	validated := strings.TrimSpace(string(out))
	if validated == "" || validated != path {
		return ValidatedPath{}, fmt.Errorf("%w: %q resolved to %q", ErrInvalidPath, path, validated)
	}
	return ValidatedPath{Path: validated, Exists: true}, nil
}

const probeScript = `# devstack-probe
set -eu
port=%d
gw=$(ip route show default 2>/dev/null | awk '{print $3; exit}')
[ -n "$gw" ] || gw=$(awk '/^nameserver/{print $2; exit}' /etc/resolv.conf)
[ -n "$gw" ] || exit 1
timeout 3 bash -c "exec 3<>/dev/tcp/$gw/$port" || exit 2
printf 'ok\n'
`

// ProbeLocalhostForwarding verifies that the distro can reach the Windows
// daemon proxy through the WSL host gateway. It is a required prerequisite for
// every Windows site backend.
func ProbeLocalhostForwarding(ctx context.Context, runner Runner, distro string, port int) error {
	if runner == nil {
		return errors.New("wsl: nil runner")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("wsl: invalid port %d", port)
	}
	script := fmt.Sprintf(probeScript, port)
	out, err := runner.Run(ctx, "-d", distro, "--", "bash", "-lc", script)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLocalhostForwarding, err)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("%w: unexpected probe output %q", ErrLocalhostForwarding, strings.TrimSpace(string(out)))
	}
	return nil
}

// CopyResult describes a verified source copy.
type CopyResult struct {
	Files  int
	Bytes  int64
	SHA256 string
}

// CopySource copies src into an absent/empty dst with a SHA-256 manifest,
// file-count/size verification, and symlink/special-file rejection. It copies
// to a temporary sibling and switches only after verification. The source is
// kept unless removeSource is true.
func CopySource(ctx context.Context, src, dst, mode string, removeSource bool) (CopyResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch mode {
	case "local", "windows", "wsl":
	default:
		return CopyResult{}, fmt.Errorf("wsl: unknown copy mode %q", mode)
	}
	info, err := os.Lstat(src)
	if err != nil {
		return CopyResult{}, fmt.Errorf("wsl: source %s: %w", src, err)
	}
	if !info.IsDir() {
		return CopyResult{}, fmt.Errorf("%w: source %s is not a directory", ErrInvalidPath, src)
	}
	if err := ctx.Err(); err != nil {
		return CopyResult{}, err
	}
	if err := ensureEmptyDestination(dst); err != nil {
		return CopyResult{}, err
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return CopyResult{}, fmt.Errorf("wsl: create destination parent: %w", err)
	}
	temp, err := os.MkdirTemp(parent, filepath.Base(dst)+".copy-")
	if err != nil {
		return CopyResult{}, fmt.Errorf("wsl: create copy staging: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(temp)
		}
	}()

	hash := sha256.New()
	var result CopyResult
	err = filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(rel, `\`) {
			return fmt.Errorf("%w: %q has an unnormalised separator", ErrInvalidPath, rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(temp, filepath.FromSlash(rel))
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %q", ErrInvalidPath, rel)
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: special file %q", ErrInvalidPath, rel)
		}
		return copyFile(ctx, path, target, rel, info.Mode().Perm(), hash, &result)
	})
	if err != nil {
		return CopyResult{}, err
	}
	// Verify the staged tree before publishing it.
	verify := 0
	verifyBytes := int64(0)
	if err := filepath.WalkDir(temp, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		verify++
		verifyBytes += info.Size()
		return nil
	}); err != nil {
		return CopyResult{}, fmt.Errorf("wsl: verify copy: %w", err)
	}
	if verify != result.Files || verifyBytes != result.Bytes {
		return CopyResult{}, fmt.Errorf("wsl: copy verification mismatch: files %d/%d bytes %d/%d", verify, result.Files, verifyBytes, result.Bytes)
	}
	if err := os.RemoveAll(dst); err != nil {
		return CopyResult{}, fmt.Errorf("wsl: clear destination: %w", err)
	}
	if err := os.Rename(temp, dst); err != nil {
		return CopyResult{}, fmt.Errorf("wsl: publish copy: %w", err)
	}
	cleanup = false
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if removeSource {
		if err := os.RemoveAll(src); err != nil {
			return result, fmt.Errorf("wsl: remove source: %w", err)
		}
	}
	return result, nil
}

func ensureEmptyDestination(dst string) error {
	info, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("wsl: destination %s: %w", dst, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: destination %s is not a directory", ErrDestinationNotEmpty, dst)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		return fmt.Errorf("wsl: read destination %s: %w", dst, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("%w: %s", ErrDestinationNotEmpty, dst)
	}
	return nil
}

func copyFile(ctx context.Context, src, dst, rel string, mode os.FileMode, hash io.Writer, result *CopyResult) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("wsl: open %s: %w", rel, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("wsl: create %s: %w", rel, err)
	}
	fileHash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, fileHash), &contextReader{ctx: ctx, reader: in})
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("wsl: copy %s: %w", rel, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("wsl: close %s: %w", rel, closeErr)
	}
	if err := os.Chmod(dst, mode); err != nil {
		return fmt.Errorf("wsl: chmod %s: %w", rel, err)
	}
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%s\x00", rel, written, hex.EncodeToString(fileHash.Sum(nil)))
	result.Files++
	result.Bytes += written
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
