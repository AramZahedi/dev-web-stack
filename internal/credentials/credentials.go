// Package credentials generates and stores service credential secrets under
// <root>/secrets/<engine>. Secrets are random 32-byte values encoded as hex and
// are created with O_EXCL so an existing credential is never silently replaced.
package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SecretDirMode is the permission mode for the secrets root and engine directories.
const SecretDirMode os.FileMode = 0o700

// SecretFileMode is the permission mode for every generated secret file.
const SecretFileMode os.FileMode = 0o600

var (
	// ErrInvalidName reports an unsafe engine or secret name.
	ErrInvalidName = errors.New("invalid credential name")
	// ErrSecretExists reports an attempt to create a secret that already exists.
	ErrSecretExists = errors.New("credential secret already exists")
	// ErrSecretMissing reports a missing credential file.
	ErrSecretMissing = errors.New("credential secret is missing")
	// ErrEmptySecret reports an empty or whitespace-only credential file.
	ErrEmptySecret = errors.New("credential secret is empty")
	// ErrInvalidSecret reports a secret path that is not a regular file.
	ErrInvalidSecret = errors.New("credential secret is not a regular file")
	// ErrInvalidDataDir reports a data path that is not a directory.
	ErrInvalidDataDir = errors.New("service data path is not a directory")
	// ErrDataWithoutSecret reports an initialized data directory whose secret is missing.
	ErrDataWithoutSecret = errors.New("service data exists but its credential secret is missing")
	// ErrSecretWithoutData reports a credential whose data directory is missing.
	ErrSecretWithoutData = errors.New("service credential exists but its data directory is missing")
	// ErrInvalidStore reports a Store without a usable root.
	ErrInvalidStore = errors.New("credential store root is not configured")
)

var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Store manages the secrets tree below Root. Now is an optional clock seam for
// callers that need deterministic audit timestamps; secret generation always
// uses crypto/rand.
type Store struct {
	Root string
	Now  func() time.Time
}

// Dir returns the engine secret directory under the store root.
func (s *Store) Dir(engine string) string {
	return filepath.Join(s.Root, "secrets", engine)
}

// Path returns the on-disk path for one engine secret.
func (s *Store) Path(engine, name string) string {
	return filepath.Join(s.Dir(engine), name)
}

// Ensure returns the requested secrets for engine, creating any missing files
// when the data directory is new. It never overwrites an existing secret.
//
// A missing data directory with existing secrets, or a non-empty data directory
// with a missing secret, is reported as an explicit inconsistency instead of
// silently generating a new password. An empty data directory is treated as an
// installed-but-not-yet-initialized service, which is the normal state between
// Install and the first Activate.
func (s *Store) Ensure(engine, dataDir string, names ...string) (map[string]string, error) {
	if err := s.validateStore(); err != nil {
		return nil, err
	}
	if err := validateName(engine); err != nil {
		return nil, fmt.Errorf("%w: engine %q", ErrInvalidName, engine)
	}

	unique := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if err := validateName(name); err != nil {
			return nil, fmt.Errorf("%w: secret %q", ErrInvalidName, name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		unique = append(unique, name)
	}

	if len(unique) == 0 {
		if err := ensureDir(s.Dir(engine)); err != nil {
			return nil, err
		}
		return map[string]string{}, nil
	}

	dataExists, dataInitialized, err := inspectDataDir(dataDir)
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(unique))
	missing := make([]string, 0, len(unique))
	for _, name := range unique {
		value, exists, err := s.readIfExists(engine, name)
		if err != nil {
			return nil, err
		}
		if exists {
			values[name] = value
			continue
		}
		missing = append(missing, name)
	}

	if len(values) > 0 && !dataExists {
		return nil, fmt.Errorf("%w: engine %q", ErrSecretWithoutData, engine)
	}
	if dataInitialized && len(missing) > 0 {
		return nil, fmt.Errorf("%w: engine %q secret %q", ErrDataWithoutSecret, engine, missing[0])
	}

	if err := ensureDir(s.Dir(engine)); err != nil {
		return nil, err
	}

	created := make([]string, 0, len(missing))
	cleanup := func() {
		for _, name := range created {
			_ = os.Remove(s.Path(engine, name))
		}
	}
	for _, name := range missing {
		value, err := s.writeNew(engine, name)
		if errors.Is(err, ErrSecretExists) {
			// A concurrent caller won the O_EXCL race. Reuse its value
			// instead of replacing it.
			existing, exists, readErr := s.readIfExists(engine, name)
			if readErr != nil {
				cleanup()
				return nil, readErr
			}
			if !exists {
				cleanup()
				return nil, err
			}
			values[name] = existing
			continue
		}
		if err != nil {
			cleanup()
			return nil, err
		}
		created = append(created, name)
		values[name] = value
	}
	return values, nil
}

// Read returns one existing secret. It never generates or repairs a secret.
func (s *Store) Read(engine, name string) (string, error) {
	value, exists, err := s.readIfExists(engine, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: engine %q secret %q", ErrSecretMissing, engine, name)
	}
	return value, nil
}

// Write generates a new secret and creates it with O_EXCL. An existing value is
// never replaced; use Rotate for an explicit replacement.
func (s *Store) Write(engine, name string) (string, error) {
	if err := s.validateStore(); err != nil {
		return "", err
	}
	if err := validateName(engine); err != nil {
		return "", fmt.Errorf("%w: engine %q", ErrInvalidName, engine)
	}
	if err := validateName(name); err != nil {
		return "", fmt.Errorf("%w: secret %q", ErrInvalidName, name)
	}
	if err := ensureDir(s.Dir(engine)); err != nil {
		return "", err
	}
	return s.writeNew(engine, name)
}

// Rotate generates a new secret and atomically replaces the existing file. The
// previous value is not retained.
func (s *Store) Rotate(engine, name string) (string, error) {
	if err := s.validateStore(); err != nil {
		return "", err
	}
	if err := validateName(engine); err != nil {
		return "", fmt.Errorf("%w: engine %q", ErrInvalidName, engine)
	}
	if err := validateName(name); err != nil {
		return "", fmt.Errorf("%w: secret %q", ErrInvalidName, name)
	}
	if err := ensureDir(s.Dir(engine)); err != nil {
		return "", err
	}

	value, err := newSecret()
	if err != nil {
		return "", err
	}

	dir := s.Dir(engine)
	tmp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return "", fmt.Errorf("create temporary secret for %q: %w", engine, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(SecretFileMode); err != nil {
		cleanup()
		return "", fmt.Errorf("set temporary secret mode: %w", err)
	}
	if _, err := tmp.WriteString(value); err != nil {
		cleanup()
		return "", fmt.Errorf("write temporary secret: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("sync temporary secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close temporary secret: %w", err)
	}
	if err := os.Rename(tmpName, s.Path(engine, name)); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("replace credential secret: %w", err)
	}
	return value, nil
}

func (s *Store) validateStore() error {
	if s == nil || strings.TrimSpace(s.Root) == "" {
		return ErrInvalidStore
	}
	return nil
}

func (s *Store) readIfExists(engine, name string) (string, bool, error) {
	path := s.Path(engine, name)
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("inspect credential secret for %q: %w", engine, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%w: engine %q secret %q", ErrInvalidSecret, engine, name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read credential secret for %q: %w", engine, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", false, fmt.Errorf("%w: engine %q secret %q", ErrEmptySecret, engine, name)
	}
	return value, true, nil
}

func (s *Store) writeNew(engine, name string) (string, error) {
	value, err := newSecret()
	if err != nil {
		return "", err
	}
	path := s.Path(engine, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, SecretFileMode)
	if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%w: engine %q secret %q", ErrSecretExists, engine, name)
	}
	if err != nil {
		return "", fmt.Errorf("create credential secret for %q: %w", engine, err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(SecretFileMode); err != nil {
		cleanup()
		return "", fmt.Errorf("set credential secret mode: %w", err)
	}
	if _, err := file.WriteString(value); err != nil {
		cleanup()
		return "", fmt.Errorf("write credential secret for %q: %w", engine, err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("sync credential secret for %q: %w", engine, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close credential secret for %q: %w", engine, err)
	}
	return value, nil
}

func inspectDataDir(dataDir string) (exists bool, initialized bool, err error) {
	if strings.TrimSpace(dataDir) == "" {
		return false, false, nil
	}
	info, err := os.Lstat(dataDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("inspect service data directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, false, fmt.Errorf("%w: %s", ErrInvalidDataDir, dataDir)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		// A data directory owned by the container image's service UID may be
		// unreadable to the installing user. Treat an unreadable existing
		// directory as initialized rather than as an empty install.
		if errors.Is(err, fs.ErrPermission) {
			return true, true, nil
		}
		return false, false, fmt.Errorf("inspect service data directory: %w", err)
	}
	return true, len(entries) > 0, nil
}

func validateName(name string) error {
	if !nameRegex.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, SecretDirMode); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(dir, SecretDirMode); err != nil {
		return fmt.Errorf("set credential directory mode: %w", err)
	}
	return nil
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("generate credential secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
