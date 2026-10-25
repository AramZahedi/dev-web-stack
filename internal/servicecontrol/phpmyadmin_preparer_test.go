package servicecontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestPhpMyAdminPreparerSeedsUserConfig(t *testing.T) {
	root := t.TempDir()
	prep := NewPhpMyAdminPreparer(root)
	svc, ok := services.DefaultCatalog().Get(services.IDPHPMyAdmin)
	if !ok {
		t.Fatal("catalog missing phpmyadmin")
	}

	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatalf("PrepareInstall() = %v", err)
	}
	path := filepath.Join(root, "services", "phpmyadmin", "config", "config.user.inc.php")
	content := readTestFile(t, path)
	for _, want := range []string{
		"$cfg['Servers'][1]['host'] = 'db';",
		"$cfg['Servers'][1]['port'] = '3306';",
		"$cfg['Servers'][1]['auth_type'] = 'cookie';",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config.user.inc.php missing %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{"password", "root_password", "devstack_password"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("config.user.inc.php contains credential material %q:\n%s", forbidden, content)
		}
	}
	requireTestMode(t, path, 0o644)
	requireTestMode(t, filepath.Dir(path), 0o755)
}

func TestPhpMyAdminPreparerDoesNotOverwriteEditedConfig(t *testing.T) {
	root := t.TempDir()
	prep := NewPhpMyAdminPreparer(root)
	svc, _ := services.DefaultCatalog().Get(services.IDPHPMyAdmin)
	if err := prep.PrepareInstall(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "services", "phpmyadmin", "config", "config.user.inc.php")
	edited := readTestFile(t, path) + "\n// operator edit\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prep.PrepareActivate(context.Background(), svc); err != nil {
		t.Fatalf("PrepareActivate() = %v", err)
	}
	if got := readTestFile(t, path); got != edited {
		t.Fatal("PrepareActivate() overwrote an edited config")
	}
}

func TestPhpMyAdminPreparerRejectsWrongService(t *testing.T) {
	prep := NewPhpMyAdminPreparer(t.TempDir())
	svc, _ := services.DefaultCatalog().Get(services.IDMySQL)
	if err := prep.PrepareInstall(context.Background(), svc); err == nil {
		t.Fatal("PrepareInstall(mysql) = nil, want an error")
	}
}

func TestPhpMyAdminPreparerHasNoCredentials(t *testing.T) {
	prep := NewPhpMyAdminPreparer(t.TempDir())
	if _, err := prep.Credentials(context.Background(), services.IDPHPMyAdmin); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("Credentials() error = %v, want ErrCredentialsUnsupported", err)
	}
	if _, err := prep.RotateCredentials(context.Background(), services.IDPHPMyAdmin); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("RotateCredentials() error = %v, want ErrCredentialsUnsupported", err)
	}
}

func TestNewPhpMyAdminPreparers(t *testing.T) {
	preparers := NewPhpMyAdminPreparers(t.TempDir())
	if prep, ok := preparers[services.IDPHPMyAdmin]; !ok || prep == nil {
		t.Fatalf("phpmyadmin preparer = %v, present = %t", prep, ok)
	}
	if len(preparers) != 1 {
		t.Fatalf("preparers = %d, want 1", len(preparers))
	}
}
