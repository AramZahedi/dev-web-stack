package tooling

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
)

func writeFakeExtension(t *testing.T, build model.RuntimeBuild, name string) {
	t.Helper()
	dir := filepath.Join(build.Path, "lib", "php", "extensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".so"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readGlobalIni(t *testing.T, paths string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths, "config", "cli", "php.ini"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGlobalIniUsesVersionDefaultsAndComposerExtensions(t *testing.T) {
	svc, st, paths, build, _ := newToolService(t)
	ctx := context.Background()
	writeFakeExtension(t, build, "redis")
	if err := st.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini: map[string]string{
			"memory_limit":   "1G",
			"display_errors": "Off",
		},
		Extensions: map[string]bool{
			"redis":    true,
			"xdebug":   false,
			"phar":     false,
			"openssl":  false,
			"mbstring": false,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SetPHPDefault(ctx, "8.4", false); err != nil {
		t.Fatal(err)
	}
	text := readGlobalIni(t, paths.Home)
	for _, want := range []string{
		"memory_limit = 1G",
		"display_errors = Off",
		"extension=redis.so",
		// Composer must work even when a version profile disables its
		// required extensions.
		"extension=phar.so",
		"extension=openssl.so",
		"extension=mbstring.so",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("global ini missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "xdebug.so") {
		t.Fatalf("global ini enabled a disabled extension:\n%s", text)
	}
}

func TestGlobalIniRendersCLIRawOverridesAfterVersionDefaults(t *testing.T) {
	svc, st, paths, _, _ := newToolService(t)
	ctx := context.Background()
	if err := st.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini:     map[string]string{"memory_limit": "1G"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPHPDefault(ctx, "8.4", false); err != nil {
		t.Fatal(err)
	}

	raw := "memory_limit = 2G\ncli.only = on\n"
	if _, err := svc.SaveGlobalIni(ctx, raw); err != nil {
		t.Fatal(err)
	}
	text := readGlobalIni(t, paths.Home)
	defaultIndex := strings.Index(text, "memory_limit = 1G")
	rawIndex := strings.Index(text, "memory_limit = 2G")
	if defaultIndex < 0 || rawIndex < 0 {
		t.Fatalf("global ini missing default or raw override:\n%s", text)
	}
	if rawIndex < defaultIndex {
		t.Fatalf("raw override rendered before the version default:\n%s", text)
	}
	if !strings.Contains(text, "cli.only = on") {
		t.Fatalf("global ini missing CLI-only directive:\n%s", text)
	}
}

func TestGlobalIniMigratesExistingRawSectionIntoTheGeneratedFile(t *testing.T) {
	svc, st, paths, _, _ := newToolService(t)
	ctx := context.Background()
	if err := st.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini:     map[string]string{"memory_limit": "1G"},
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := phpini.Compose(phpini.Config{Raw: "cli.legacy = on\n"})
	if err != nil {
		t.Fatal(err)
	}
	iniPath := GlobalIniPath(paths)
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, legacy, 0o644); err != nil {
		t.Fatal(err)
	}

	ini, err := svc.GlobalIni(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"memory_limit = 1G", "cli.legacy = on"} {
		if !strings.Contains(ini.Content, want) {
			t.Fatalf("migrated ini missing %q:\n%s", want, ini.Content)
		}
	}
	if ini.Raw != "cli.legacy = on\n" {
		t.Fatalf("raw = %q, want the migrated CLI section", ini.Raw)
	}
}

func TestGlobalIniTracksTheSelectedPHPLineDefaults(t *testing.T) {
	svc, st, paths, _, _ := newToolService(t)
	ctx := context.Background()
	build83 := fakePHPBuild(t, paths.Home, "8.3.14")
	if err := st.UpsertRuntime(ctx, build83); err != nil {
		t.Fatal(err)
	}
	for version, memory := range map[string]string{"8.3": "300M", "8.4": "400M"} {
		if err := st.SavePHPDefaults(ctx, model.PHPDefaults{
			Version: version,
			Ini:     map[string]string{"memory_limit": memory},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.SetPHPDefault(ctx, "8.4", false); err != nil {
		t.Fatal(err)
	}
	if text := readGlobalIni(t, paths.Home); !strings.Contains(text, "memory_limit = 400M") {
		t.Fatalf("PHP 8.4 global ini missing its default:\n%s", text)
	}
	if _, err := svc.SetPHPDefault(ctx, "8.3", false); err != nil {
		t.Fatal(err)
	}
	if text := readGlobalIni(t, paths.Home); !strings.Contains(text, "memory_limit = 300M") {
		t.Fatalf("PHP 8.3 global ini missing its default:\n%s", text)
	}
}

func TestGlobalIniMigrationDropsManagedDirectivesFromAManualFile(t *testing.T) {
	svc, _, paths, _, _ := newToolService(t)
	ctx := context.Background()
	legacy := "[PHP]\nmemory_limit = 1G\ncli.only = on\nextension = xdebug.so\nextension_dir = \"/tmp/php-ext\"\n"
	iniPath := GlobalIniPath(paths)
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	ini, err := svc.GlobalIni(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"memory_limit = 1G", "cli.only = on"} {
		if !strings.Contains(ini.Content, want) {
			t.Fatalf("migrated ini missing %q:\n%s", want, ini.Content)
		}
	}
	for _, unwanted := range []string{"extension=xdebug.so", "/tmp/php-ext"} {
		if strings.Contains(ini.Content, unwanted) {
			t.Fatalf("migrated ini kept managed directive %q:\n%s", unwanted, ini.Content)
		}
	}
	if !strings.Contains(ini.Raw, "cli.only = on") {
		t.Fatalf("raw = %q, want the preserved manual directives", ini.Raw)
	}
}

func TestRefreshPHPDefaultsRegeneratesOnlyTheMatchingCLILine(t *testing.T) {
	svc, st, paths, _, _ := newToolService(t)
	ctx := context.Background()
	if _, err := svc.SetPHPDefault(ctx, "8.4", false); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini:     map[string]string{"memory_limit": "2G"},
	}); err != nil {
		t.Fatal(err)
	}
	if text := readGlobalIni(t, paths.Home); strings.Contains(text, "memory_limit = 2G") {
		t.Fatalf("test setup unexpectedly regenerated the CLI ini:\n%s", text)
	}

	if err := svc.RefreshPHPDefaults(ctx, "8.3"); err != nil {
		t.Fatal(err)
	}
	if text := readGlobalIni(t, paths.Home); strings.Contains(text, "memory_limit = 2G") {
		t.Fatalf("non-matching line refreshed the CLI ini:\n%s", text)
	}
	if err := svc.RefreshPHPDefaults(ctx, "8.4"); err != nil {
		t.Fatal(err)
	}
	if text := readGlobalIni(t, paths.Home); !strings.Contains(text, "memory_limit = 2G") {
		t.Fatalf("matching line did not refresh the CLI ini:\n%s", text)
	}
}
