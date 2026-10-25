package tooling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/config"
)

// composerSource describes one pinned Composer download. Production callers
// use defaultComposerSource; tests can supply a local fixture.
type composerSource struct {
	URL     string
	Version string
	SHA256  string
	Size    int64
}

func defaultComposerSource() composerSource {
	return composerSource{
		URL:     ComposerURL,
		Version: ComposerVersion,
		SHA256:  ComposerSHA256,
		Size:    ComposerSize,
	}
}

// ComposerStatus reports the managed PHAR and wrapper state.
type ComposerStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	PHARPath  string `json:"phar_path"`
	Wrapper   string `json:"wrapper_path"`
}

// installComposer downloads, verifies, and installs the pinned PHAR and its
// wrapper. Existing valid artifacts are reused without a network request.
func installComposer(ctx context.Context, client *http.Client, paths config.Paths, source composerSource) (ComposerStatus, error) {
	if source.URL == "" || source.SHA256 == "" || source.Size <= 0 {
		return ComposerStatus{}, errors.New("tooling: incomplete composer source")
	}
	if client == nil {
		client = http.DefaultClient
	}
	phar := ComposerPHARPath(paths)
	valid, err := verifyComposerFile(phar, source)
	if err != nil {
		return ComposerStatus{}, err
	}
	if !valid {
		if err := downloadComposer(ctx, client, phar, source); err != nil {
			return ComposerStatus{}, err
		}
	} else if err := os.Chmod(phar, 0o600); err != nil {
		return ComposerStatus{}, fmt.Errorf("tooling: set composer phar mode: %w", err)
	}
	if err := writeAtomicFile(filepath.Join(ComposerDir(paths), "VERSION"), []byte(source.Version+"\n"), 0o644); err != nil {
		return ComposerStatus{}, err
	}
	if err := writeScriptAtomic(ComposerWrapperPath(paths), composerWrapperScript(paths)); err != nil {
		return ComposerStatus{}, err
	}
	return ComposerStatus{Installed: true, Version: source.Version, PHARPath: phar, Wrapper: ComposerWrapperPath(paths)}, nil
}

// verifyComposerFile reports whether path is the exact pinned PHAR.
func verifyComposerFile(path string, source composerSource) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("tooling: inspect composer phar: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != source.Size {
		return false, nil
	}
	sum, err := hashFile(path)
	if err != nil {
		return false, fmt.Errorf("tooling: hash composer phar: %w", err)
	}
	return sum == strings.ToLower(source.SHA256), nil
}

// downloadComposer streams one pinned response into a temporary file, verifies
// size and SHA-256, and atomically publishes it with mode 0600.
func downloadComposer(ctx context.Context, client *http.Client, target string, source composerSource) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return fmt.Errorf("tooling: build composer request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("tooling: download composer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tooling: download composer: %s returned HTTP %d", source.URL, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("tooling: create composer directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".composer.phar-*")
	if err != nil {
		return fmt.Errorf("tooling: create temporary composer phar: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: set composer phar mode: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, source.Size+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return fmt.Errorf("tooling: download composer: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("tooling: close composer phar: %w", closeErr)
	}
	if written != source.Size {
		return fmt.Errorf("%w: downloaded %d bytes, want %d", ErrComposerChecksum, written, source.Size)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != strings.ToLower(source.SHA256) {
		return fmt.Errorf("%w: sha256 %s, want %s", ErrComposerChecksum, got, source.SHA256)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("tooling: replace composer phar: %w", err)
	}
	return nil
}

// composerWrapperScript renders the managed Composer wrapper. It never builds
// a command string: arguments are passed as a list with "$@".
func composerWrapperScript(paths config.Paths) string {
	globalPHP := filepath.Join(ShimDir(paths), "php")
	phar := ComposerPHARPath(paths)
	return "#!/bin/sh\n" +
		"set -eu\n" +
		"phar=" + shellQuote(phar) + "\n" +
		"if [ -n \"${DEVSTACK_SITE:-}\" ]; then\n" +
		"    php=\"$(command -v php)\" || { echo 'devstack: site shell is missing its php shim' >&2; exit 127; }\n" +
		"    site_dir=\"${DEVSTACK_HOME:-/nonexistent}/tools/sites/${DEVSTACK_SITE}/\"\n" +
		"    case \"$php\" in\n" +
		"        \"$site_dir\"*) ;;\n" +
		"        *) echo 'devstack: Composer is unavailable in this site shell: no site PHP runtime is on PATH (Composer requires a PHP site)' >&2; exit 127 ;;\n" +
		"    esac\n" +
		"else\n" +
		"    php=" + shellQuote(globalPHP) + "\n" +
		"    if [ ! -x \"$php\" ]; then\n" +
		"        php=\"$(command -v php)\" || { echo 'devstack: no PHP CLI default is installed' >&2; exit 127; }\n" +
		"    fi\n" +
		"fi\n" +
		"exec \"$php\" \"$phar\" \"$@\"\n"
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
