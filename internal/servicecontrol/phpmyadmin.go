package servicecontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// PhpMyAdminPreparer seeds the phpMyAdmin signon config, the signon script,
// the active root secret, and the token-signing key. The catalog mounts these
// read-only into the container. The root password is never written into the PHP
// config; signon.php reads it from /run/secrets/root_password.
type PhpMyAdminPreparer struct {
	Root  string
	Store Store
}

// NewPhpMyAdminPreparer returns a preparer rooted at the devstack home. The
// optional store resolves the active MySQL-family engine for auto-login.
func NewPhpMyAdminPreparer(root string, stores ...Store) *PhpMyAdminPreparer {
	clean := strings.TrimSpace(root)
	if clean != "" {
		clean = filepath.Clean(clean)
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
	}
	prep := &PhpMyAdminPreparer{Root: clean}
	if len(stores) > 0 {
		prep.Store = stores[0]
	}
	return prep
}

// NewPhpMyAdminPreparers returns the preparer registry for phpMyAdmin.
func NewPhpMyAdminPreparers(root string, stores ...Store) map[services.ID]Preparer {
	return map[services.ID]Preparer{
		services.IDPHPMyAdmin: NewPhpMyAdminPreparer(root, stores...),
	}
}

// PrepareInstall seeds the signon config and refreshes the root/key secrets.
func (p *PhpMyAdminPreparer) PrepareInstall(ctx context.Context, svc services.Service) error {
	return p.prepare(ctx, svc)
}

// PrepareActivate repeats the idempotent seed before a start.
func (p *PhpMyAdminPreparer) PrepareActivate(ctx context.Context, svc services.Service) error {
	return p.prepare(ctx, svc)
}

// CommitActivate is a no-op.
func (p *PhpMyAdminPreparer) CommitActivate(context.Context, services.Service) error {
	return nil
}

// CleanupActivate is a no-op.
func (p *PhpMyAdminPreparer) CleanupActivate(context.Context, services.Service, error) error {
	return nil
}

// Credentials is unsupported: phpMyAdmin uses the active root secret directly.
func (p *PhpMyAdminPreparer) Credentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: phpMyAdmin uses signon authentication", ErrCredentialsUnsupported)
}

// RotateCredentials is unsupported for phpMyAdmin itself.
func (p *PhpMyAdminPreparer) RotateCredentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: phpMyAdmin uses signon authentication", ErrCredentialsUnsupported)
}

func (p *PhpMyAdminPreparer) prepare(ctx context.Context, svc services.Service) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if svc.ID != services.IDPHPMyAdmin {
		return fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, svc.ID)
	}
	if p == nil || strings.TrimSpace(p.Root) == "" {
		return fmt.Errorf("%w: phpMyAdmin preparer root is not configured", ErrCredentialsUnsupported)
	}
	serviceDir := filepath.Join(p.Root, "services", string(svc.ID))
	configDir := filepath.Join(serviceDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create phpMyAdmin config directory: %w", err)
	}
	if err := p.seedUserConfig(filepath.Join(configDir, "config.user.inc.php")); err != nil {
		return err
	}
	if err := writePhpMyAdminFile(filepath.Join(configDir, "signon.php"), services.PhpMyAdminSignonScript); err != nil {
		return err
	}
	if err := p.syncSignonKey(); err != nil {
		return err
	}
	return p.syncRootPassword(ctx)
}

// seedUserConfig writes the generated config when missing and migrates only the
// exact legacy generated file. Operator-edited files are never overwritten.
func (p *PhpMyAdminPreparer) seedUserConfig(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return writeFileIfAbsent(path, services.PhpMyAdminUserConfig, 0o644)
	}
	if err != nil {
		return fmt.Errorf("read phpMyAdmin user config: %w", err)
	}
	current := strings.TrimSpace(string(content))
	if current != strings.TrimSpace(services.PhpMyAdminLegacyUserConfig) &&
		current != strings.TrimSpace(services.PhpMyAdminConfigAuthUserConfig) {
		return nil
	}
	if err := writePhpMyAdminFile(path, services.PhpMyAdminUserConfig); err != nil {
		return fmt.Errorf("migrate phpMyAdmin user config: %w", err)
	}
	return nil
}

// writePhpMyAdminFile atomically replaces a generated file with mode 0644.
func writePhpMyAdminFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// syncRootPassword copies the active MySQL-family root secret into the stable
// file mounted into phpMyAdmin. The directory is private; the file itself is
// 0644 so the unprivileged phpMyAdmin process can read the bind mount.
func (p *PhpMyAdminPreparer) syncRootPassword(ctx context.Context) error {
	if p.Store == nil {
		return nil
	}
	records, err := p.Store.ListServiceStates(ctx)
	if err != nil {
		return fmt.Errorf("list services for phpMyAdmin auto-login: %w", err)
	}
	var engine services.ID
	for _, rec := range records {
		if rec.ID != services.IDMySQL && rec.ID != services.IDMariaDB {
			continue
		}
		if rec.Active || rec.State == services.StateActive {
			engine = rec.ID
			break
		}
	}
	if engine == "" {
		return nil
	}
	secret, err := (&credentials.Store{Root: p.Root}).Read(string(engine), "root_password")
	if err != nil {
		// Windows/WSL agent installs store service secrets beside the service,
		// while the Linux helper uses <root>/secrets/<engine>.
		fallback := filepath.Join(p.Root, "services", string(engine), "secrets", "root_password")
		data, readErr := os.ReadFile(fallback)
		if readErr != nil {
			return fmt.Errorf("read %s root password for phpMyAdmin: %w", engine, err)
		}
		secret = string(data)
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("read %s root password for phpMyAdmin: empty secret", engine)
	}
	for _, dir := range []string{
		filepath.Join(p.Root, "secrets", "phpmyadmin"),
		filepath.Join(p.Root, "services", "phpmyadmin", "secrets"),
	} {
		if err := writePhpMyAdminSecret(dir, "root_password", secret); err != nil {
			return err
		}
	}
	return nil
}

// syncSignonKey ensures the 32-byte HMAC key exists in both service layouts.
func (p *PhpMyAdminPreparer) syncSignonKey() error {
	key, err := readPhpMyAdminSecret(p.Root, "signon_key")
	if errors.Is(err, os.ErrNotExist) || key == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generate phpMyAdmin signon key: %w", err)
		}
		key = hex.EncodeToString(raw)
	} else if err != nil {
		return fmt.Errorf("read phpMyAdmin signon key: %w", err)
	}
	for _, dir := range []string{
		filepath.Join(p.Root, "secrets", "phpmyadmin"),
		filepath.Join(p.Root, "services", "phpmyadmin", "secrets"),
	} {
		if err := writePhpMyAdminSecret(dir, "signon_key", key); err != nil {
			return err
		}
	}
	return nil
}

// readPhpMyAdminSecret reads one stable phpMyAdmin secret, checking the Linux
// helper layout and the Windows/WSL agent layout.
func readPhpMyAdminSecret(root, name string) (string, error) {
	paths := []string{
		filepath.Join(root, "secrets", "phpmyadmin", name),
		filepath.Join(root, "services", "phpmyadmin", "secrets", name),
	}
	var lastErr error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
		lastErr = err
	}
	return "", lastErr
}

// writePhpMyAdminSecret writes one stable secret into a service layout.
// The directory is private; the file itself is 0644 so the unprivileged
// phpMyAdmin process can read the bind mount.
func writePhpMyAdminSecret(dir, name, secret string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create phpMyAdmin secret directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure phpMyAdmin secret directory: %w", err)
	}
	target := filepath.Join(dir, name)
	if current, readErr := os.ReadFile(target); readErr == nil && strings.TrimSpace(string(current)) == secret {
		if chmodErr := os.Chmod(target, 0o644); chmodErr != nil {
			return fmt.Errorf("secure phpMyAdmin %s: %w", name, chmodErr)
		}
		return nil
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(secret+"\n"), 0o644); err != nil {
		return fmt.Errorf("write phpMyAdmin %s: %w", name, err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure phpMyAdmin %s: %w", name, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace phpMyAdmin %s: %w", name, err)
	}
	return nil
}
