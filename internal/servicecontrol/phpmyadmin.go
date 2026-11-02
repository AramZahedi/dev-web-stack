package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// PhpMyAdminPreparer seeds the small phpMyAdmin user config that points the
// container at the fixed "db" Compose network alias. The catalog environment
// still carries PMA_HOST=host.docker.internal for host-gateway compatibility,
// but the alias is what survives MySQL/MariaDB switches on native Linux Docker.
//
// The same preparer keeps a stable copy of the active MySQL-family root
// password at <root>/secrets/phpmyadmin/root_password. The catalog mounts that
// file read-only at /run/secrets/root_password, and config.user.inc.php uses
// config authentication as root, so the Open phpMyAdmin button logs in without
// a password prompt. The password never appears in the PHP config file.
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

// PrepareInstall seeds config.user.inc.php and refreshes the auto-login secret.
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
	return Credentials{}, fmt.Errorf("%w: phpMyAdmin uses config authentication", ErrCredentialsUnsupported)
}

// RotateCredentials is unsupported for phpMyAdmin itself.
func (p *PhpMyAdminPreparer) RotateCredentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: phpMyAdmin uses config authentication", ErrCredentialsUnsupported)
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
	if strings.TrimSpace(string(content)) != strings.TrimSpace(services.PhpMyAdminLegacyUserConfig) {
		return nil
	}
	if err := os.WriteFile(path, []byte(services.PhpMyAdminUserConfig), 0o644); err != nil {
		return fmt.Errorf("migrate phpMyAdmin user config: %w", err)
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
		if err := writePhpMyAdminSecret(dir, secret); err != nil {
			return err
		}
	}
	return nil
}

// writePhpMyAdminSecret writes the stable root secret into one service layout.
// The directory is private; the file itself is 0644 so the unprivileged
// phpMyAdmin process can read the bind mount.
func writePhpMyAdminSecret(dir, secret string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create phpMyAdmin secret directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure phpMyAdmin secret directory: %w", err)
	}
	target := filepath.Join(dir, "root_password")
	if current, readErr := os.ReadFile(target); readErr == nil && strings.TrimSpace(string(current)) == secret {
		if chmodErr := os.Chmod(target, 0o644); chmodErr != nil {
			return fmt.Errorf("secure phpMyAdmin root secret: %w", chmodErr)
		}
		return nil
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(secret+"\n"), 0o644); err != nil {
		return fmt.Errorf("write phpMyAdmin root secret: %w", err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure phpMyAdmin root secret: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace phpMyAdmin root secret: %w", err)
	}
	return nil
}
