package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadCreatesDefaults(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPPort != 8080 || cfg.HTTPSPort != 8443 || cfg.UIPort != 7070 {
		t.Fatalf("ports = %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsDuplicatePorts(t *testing.T) {
	cfg := Default()
	cfg.HTTPSPort = cfg.HTTPPort
	if err := cfg.Validate(); err == nil {
		t.Fatal("want error")
	}
}

func TestDefaultValues(t *testing.T) {
	cfg := Default()
	if cfg.HTTPPort != 8080 || cfg.HTTPSPort != 8443 || cfg.UIPort != 7070 {
		t.Fatalf("ports = %+v", cfg)
	}
	if cfg.ServingMode != ServingModeBuiltin {
		t.Fatalf("ServingMode = %q", cfg.ServingMode)
	}
	if cfg.ServingProvider != ServingProviderNative {
		t.Fatalf("ServingProvider = %q", cfg.ServingProvider)
	}
	if cfg.WSLDistro != "devstack" {
		t.Fatalf("WSLDistro = %q", cfg.WSLDistro)
	}
	if !cfg.AutoStartSites {
		t.Fatal("AutoStartSites = false")
	}
	if cfg.GracefulStopSeconds != 10 || cfg.MaxRequestBodyMB != 1024 {
		t.Fatalf("timeouts/body = %+v", cfg)
	}
	if cfg.LogMaxMB != 10 || cfg.LogKeep != 5 {
		t.Fatalf("log settings = %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestLoadReadsFileAndKeepsDefaults(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "http_port: 9090\nauto_start_sites: false\n")

	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Home != home {
		t.Fatalf("Home = %q, want %q", cfg.Home, home)
	}
	if cfg.HTTPPort != 9090 {
		t.Fatalf("HTTPPort = %d", cfg.HTTPPort)
	}
	if cfg.AutoStartSites {
		t.Fatal("AutoStartSites = true")
	}
	if cfg.HTTPSPort != 8443 || cfg.UIPort != 7070 || cfg.WSLDistro != "devstack" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.ServingMode != ServingModeBuiltin {
		t.Fatalf("ServingMode default = %q", cfg.ServingMode)
	}
}

func TestLoadLegacyConfigDefaultsServingProvider(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "serving_mode: nginx\nui_port: 7070\n")

	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServingProvider != ServingProviderNative {
		t.Fatalf("ServingProvider = %q, want %q", cfg.ServingProvider, ServingProviderNative)
	}
}

func TestLoadReadsServingProvider(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "serving_mode: nginx\nui_port: 7070\nserving_provider: docker\n")

	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServingProvider != ServingProviderDocker {
		t.Fatalf("ServingProvider = %q, want %q", cfg.ServingProvider, ServingProviderDocker)
	}
}

func TestSaveWritesServingProvider(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServingProvider = ServingProviderDocker
	cfg.ServingMode = ServingModeNginx
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "serving_provider: docker") {
		t.Fatalf("config.yaml missing serving_provider: %s", data)
	}
}

func TestLoadUsesDevstackHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Home != home {
		t.Fatalf("Home = %q, want %q", cfg.Home, home)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadExplicitHomeWinsOverEnv(t *testing.T) {
	explicit, fromEnv := t.TempDir(), t.TempDir()
	t.Setenv("DEVSTACK_HOME", fromEnv)

	cfg, err := Load(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Home != explicit {
		t.Fatalf("Home = %q, want %q", cfg.Home, explicit)
	}
	if _, err := os.Stat(filepath.Join(fromEnv, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config created in DEVSTACK_HOME: %v", err)
	}
}

func TestLoadEnsuresDirectories(t *testing.T) {
	home := t.TempDir()
	if _, err := Load(home); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"pki", "runtimes", "sites", "logs", "run", "cache"} {
		info, err := os.Stat(filepath.Join(home, dir))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "http_port: 0\n")
	if _, err := Load(home); err == nil {
		t.Fatal("want error for invalid port")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "http_port: [\n")
	if _, err := Load(home); err == nil {
		t.Fatal("want error for malformed yaml")
	}
}

func TestValidatePorts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		wantOK bool
	}{
		{"defaults", func(*Config) {}, true},
		{"lowest", func(c *Config) { c.HTTPPort = 1 }, true},
		{"highest", func(c *Config) { c.HTTPPort = 65535 }, true},
		{"below backend range", func(c *Config) { c.HTTPPort = 40999 }, true},
		{"above backend range", func(c *Config) { c.HTTPPort = 44000 }, true},
		{"zero http", func(c *Config) { c.HTTPPort = 0 }, false},
		{"negative https", func(c *Config) { c.HTTPSPort = -1 }, false},
		{"too high ui", func(c *Config) { c.UIPort = 65536 }, false},
		{"backend range start", func(c *Config) { c.UIPort = 41000 }, false},
		{"backend range end", func(c *Config) { c.UIPort = 43999 }, false},
		{"backend range middle", func(c *Config) { c.HTTPPort = 43001 }, false},
		{"duplicate ui and http", func(c *Config) { c.UIPort = c.HTTPPort }, false},
		{"zero graceful stop", func(c *Config) { c.GracefulStopSeconds = 0 }, false},
		{"negative graceful stop", func(c *Config) { c.GracefulStopSeconds = -1 }, false},
		{"serving builtin", func(c *Config) { c.ServingMode = ServingModeBuiltin }, true},
		{"serving nginx", func(c *Config) { c.ServingMode = ServingModeNginx }, true},
		{"serving apache", func(c *Config) { c.ServingMode = ServingModeApache }, true},
		{"serving empty", func(c *Config) { c.ServingMode = "" }, false},
		{"serving unknown", func(c *Config) { c.ServingMode = "caddy" }, false},
		{"serving builtin custom ui", func(c *Config) { c.ServingMode = ServingModeBuiltin; c.UIPort = 7071 }, true},
		{"serving nginx custom ui", func(c *Config) { c.ServingMode = ServingModeNginx; c.UIPort = 7071 }, true},
		{"serving apache custom ui", func(c *Config) { c.ServingMode = ServingModeApache; c.UIPort = 7071 }, true},
		{"provider native", func(c *Config) { c.ServingProvider = ServingProviderNative }, true},
		{"provider docker native mode", func(c *Config) { c.ServingProvider = ServingProviderDocker }, false},
		{"provider docker nginx", func(c *Config) {
			c.ServingProvider = ServingProviderDocker
			c.ServingMode = ServingModeNginx
		}, true},
		{"provider docker apache", func(c *Config) {
			c.ServingProvider = ServingProviderDocker
			c.ServingMode = ServingModeApache
		}, true},
		{"provider empty", func(c *Config) { c.ServingProvider = "" }, false},
		{"provider unknown", func(c *Config) { c.ServingProvider = "podman" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantOK && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
		})
	}
}

func TestValidateErrorNamesTheField(t *testing.T) {
	cfg := Default()
	cfg.UIPort = 42000
	err := cfg.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "ui_port") {
		t.Fatalf("error %q does not name ui_port", err)
	}
}

func TestValidateExternalModeAllowsCustomDashboardPort(t *testing.T) {
	for _, mode := range []string{ServingModeNginx, ServingModeApache} {
		cfg := Default()
		cfg.ServingMode = mode
		cfg.UIPort = 8081
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%s with custom ui_port: %v", mode, err)
		}
	}
}

func TestSaveWritesPrivateFile(t *testing.T) {
	home := t.TempDir()
	cfg := Default()
	cfg.Home = home
	cfg.HTTPSPort = 9443
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, "config.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Fatalf("leftover temp files: %v", entries)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	cfg := Default()
	cfg.Home = home
	cfg.HTTPPort = 9090
	cfg.WSLDistro = "other"
	cfg.LogKeep = 9
	cfg.ServingMode = ServingModeNginx
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.HTTPPort != 9090 || got.WSLDistro != "other" || got.LogKeep != 9 {
		t.Fatalf("round trip = %+v", got)
	}
	if got.ServingMode != ServingModeNginx {
		t.Fatalf("serving mode round trip = %q", got.ServingMode)
	}
}

func TestSaveRequiresHome(t *testing.T) {
	cfg := Default()
	if err := cfg.Save(); err == nil {
		t.Fatal("want error without home")
	}
}

func TestSaveRejectsInvalidConfig(t *testing.T) {
	cfg := Default()
	cfg.Home = t.TempDir()
	cfg.HTTPPort = cfg.HTTPSPort
	if err := cfg.Save(); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("invalid config was written: %v", err)
	}
}

func TestNewPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "devstack")
	p := NewPaths(home)

	want := map[string]string{
		"Home":     home,
		"Config":   filepath.Join(home, "config.yaml"),
		"DB":       filepath.Join(home, "state.db"),
		"PKI":      filepath.Join(home, "pki"),
		"Runtimes": filepath.Join(home, "runtimes"),
		"Sites":    filepath.Join(home, "sites"),
		"Logs":     filepath.Join(home, "logs"),
		"Run":      filepath.Join(home, "run"),
		"Cache":    filepath.Join(home, "cache"),
	}
	got := map[string]string{
		"Home":     p.Home,
		"Config":   p.Config,
		"DB":       p.DB,
		"PKI":      p.PKI,
		"Runtimes": p.Runtimes,
		"Sites":    p.Sites,
		"Logs":     p.Logs,
		"Run":      p.Run,
		"Cache":    p.Cache,
	}
	for name, wantPath := range want {
		if got[name] != wantPath {
			t.Errorf("%s = %q, want %q", name, got[name], wantPath)
		}
	}
}

func TestPathsEnsure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "devstack")
	p := NewPaths(home)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.Home, p.PKI, p.Runtimes, p.Sites, p.Logs, p.Run, p.Cache} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}
	if err := p.Ensure(); err != nil {
		t.Fatalf("second Ensure() = %v", err)
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
