// Package wsl isolates the wsl.exe process boundary: distro discovery, path
// translation, agent-side path validation, verified copies, and the
// localhost-forwarding probe.
package wsl

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
	"github.com/AramZahedi/dev-web-stack/internal/platform"
)

var (
	// ErrInvalidPath is returned when a WSL path fails validation.
	ErrInvalidPath = errors.New("wsl: invalid path")
	// ErrDestinationNotEmpty is returned when a copy target already has data.
	ErrDestinationNotEmpty = errors.New("wsl: destination is not empty")
	// ErrDestinationInUse is returned when another copy owns the destination.
	ErrDestinationInUse = errors.New("wsl: destination copy is already in progress")
	// ErrOverlappingCopy is returned when source and destination nest.
	ErrOverlappingCopy = errors.New("wsl: source and destination must not overlap")
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

// defaultExecTimeout bounds a wsl.exe call whose caller did not set a
// deadline. WSL can wedge (service restart, distro boot, a stuck 9P mount) and
// a hung wsl.exe would otherwise pin the calling goroutine forever.
const defaultExecTimeout = 3 * time.Minute

// Run invokes wsl.exe with args and returns stdout.
func (ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("wsl: no arguments")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultExecTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.WaitDelay = 3 * time.Second
	// The daemon and CLI are GUI-less or background parents; without
	// CREATE_NO_WINDOW every wsl.exe call flashes a console window.
	platform.ConfigureProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, fmt.Errorf("wsl: %w", ctxErr)
		}
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
	normalized := strings.ReplaceAll(path, "/", `\`)
	normLower := strings.ToLower(normalized)
	mode := model.SourceWindows
	if strings.HasPrefix(normLower, `\\wsl.localhost\`) || strings.HasPrefix(normLower, `\\wsl$\`) {
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

const probeScript = `set -eu
port=%d
gw=$(ip -4 route show default 2>/dev/null | awk '{print $3; exit}')
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
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	cmd := fmt.Sprintf("echo %s | base64 -d | bash", b64)
	out, err := runner.Run(ctx, "-d", distro, "--", "sh", "-c", cmd, "devstack-probe")
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
	src, err := canonicalCopyPath(src)
	if err != nil {
		return CopyResult{}, err
	}
	dst, err = canonicalCopyPath(dst)
	if err != nil {
		return CopyResult{}, err
	}
	if err := rejectOverlappingCopy(src, dst); err != nil {
		return CopyResult{}, err
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
	// A destination lock makes the check-then-publish sequence exclusive. Two
	// concurrent copies must never both pass the empty check, because the
	// second publish would delete the first copy.
	lockPath := filepath.Join(parent, "."+filepath.Base(dst)+".copy.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return CopyResult{}, fmt.Errorf("%w: %s", ErrDestinationInUse, dst)
		}
		return CopyResult{}, fmt.Errorf("wsl: lock destination %s: %w", dst, err)
	}
	defer func() {
		_ = lockFile.Close()
		_ = os.Remove(lockPath)
	}()

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
	dirModes := make(map[string]os.FileMode)
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
			// Keep the staging directory writable while children are copied;
			// the source mode is applied after the walk.
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return err
			}
			dirModes[target] = info.Mode().Perm()
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
	for target, mode := range dirModes {
		if err := os.Chmod(target, mode); err != nil {
			return CopyResult{}, fmt.Errorf("wsl: chmod staged directory %s: %w", target, err)
		}
	}
	// Verify the staged tree, including a fresh hash of every file. A count and
	// size check alone accepts a same-size corrupted copy.
	verify, err := verifyCopyTree(ctx, temp)
	if err != nil {
		return CopyResult{}, fmt.Errorf("wsl: verify copy: %w", err)
	}
	if verify.Files != result.Files || verify.Bytes != result.Bytes {
		return CopyResult{}, fmt.Errorf("wsl: copy verification mismatch: files %d/%d bytes %d/%d", verify.Files, result.Files, verify.Bytes, result.Bytes)
	}
	if !strings.EqualFold(verify.SHA256, hex.EncodeToString(hash.Sum(nil))) {
		return CopyResult{}, errors.New("wsl: copy verification mismatch: staged content hash differs from the copied manifest")
	}
	// Re-check under the lock: a destination populated after the first check
	// must not be deleted.
	if err := ensureEmptyDestination(dst); err != nil {
		return CopyResult{}, err
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

func canonicalCopyPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %v", ErrInvalidPath, value, err)
	}
	return filepath.Clean(abs), nil
}

// rejectOverlappingCopy refuses copies where one tree contains the other. The
// staging directory lives beside the destination, so a destination inside the
// source would also make the walk copy its own staging output.
func rejectOverlappingCopy(src, dst string) error {
	if samePath(src, dst) || pathContains(src, dst) || pathContains(dst, src) {
		return fmt.Errorf("%w: %s and %s", ErrOverlappingCopy, src, dst)
	}
	return nil
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	// Windows paths are case-insensitive.
	return strings.EqualFold(a, b)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// verifyCopyTree re-hashes the staged tree using the same manifest encoding as
// copyFile, so the published copy is exactly the bytes that were hashed.
func verifyCopyTree(ctx context.Context, root string) (CopyResult, error) {
	hash := sha256.New()
	var result CopyResult
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("%w: unexpected special file %q", ErrInvalidPath, path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fileHash, written, err := hashFile(path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%s\x00", filepath.ToSlash(rel), written, fileHash)
		if written != info.Size() {
			return fmt.Errorf("wsl: verify %q: read %d bytes, want %d", rel, written, info.Size())
		}
		result.Files++
		result.Bytes += written
		return nil
	})
	if err != nil {
		return CopyResult{}, err
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
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
