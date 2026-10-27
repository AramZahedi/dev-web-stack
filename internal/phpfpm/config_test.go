package phpfpm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var testContext = context.Background()

func validConfig() Config {
	return Config{
		SiteID:       "site",
		BuildRoot:    "/opt/php",
		PHPIni:       "/home/u/.devstack/sites/site/abc/php.ini",
		ListenHost:   "127.0.0.1",
		ListenPort:   43001,
		DocumentRoot: "/home/u/blog/public",
		ErrorLog:     "/proc/self/fd/2",
		PIDFile:      "/home/u/.devstack/sites/site/abc/php-fpm.pid",
		Env:          map[string]string{"APP_ENV": "local"},
	}
}

func TestRenderFPMConfigMatchesSpec(t *testing.T) {
	cfg := validConfig()
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"[global]", "pid = \"/home/u/.devstack/sites/site/abc/php-fpm.pid\"",
		"error_log = \"/proc/self/fd/2\"", "daemonize = no", "log_level = notice",
		"[www]", "listen = 127.0.0.1:43001",
		"pm = dynamic", "pm.max_children = 5", "pm.start_servers = 2",
		"pm.min_spare_servers = 1", "pm.max_spare_servers = 3",
		"catch_workers_output = yes", "clear_env = no",
		"chdir = \"/home/u/blog/public\"", "security.limit_extensions = .php",
		"env[APP_ENV] = \"local\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Index(text, "[global]") > strings.Index(text, "[www]") {
		t.Fatalf("[global] must come before [www]:\n%s", text)
	}
}

func TestRenderFPMConfigUsesPersistedPoolBudget(t *testing.T) {
	cfg := validConfig()
	cfg.Pool = &model.PHPFPMBudget{
		Mode:            "dynamic",
		MaxChildren:     12,
		StartServers:    3,
		MinSpareServers: 2,
		MaxSpareServers: 4,
		ListenBacklog:   511,
	}
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"pm = dynamic", "pm.max_children = 12", "pm.start_servers = 3",
		"pm.min_spare_servers = 2", "pm.max_spare_servers = 4",
		"listen.backlog = 511",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestRenderFPMConfigStaticPoolOmitsDynamicSpares(t *testing.T) {
	cfg := validConfig()
	cfg.Pool = &model.PHPFPMBudget{Mode: "static", MaxChildren: 8, ListenBacklog: 128}
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"pm = static", "pm.max_children = 8", "listen.backlog = 128",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"pm.start_servers", "pm.min_spare_servers", "pm.max_spare_servers"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("static pool contains %q:\n%s", unwanted, text)
		}
	}
}

func TestRenderFPMConfigFillsPartialPoolBudget(t *testing.T) {
	cases := []struct {
		name   string
		pool   model.PHPFPMBudget
		expect []string
	}{
		{
			name: "minimum spare only",
			pool: model.PHPFPMBudget{MaxChildren: 8, MinSpareServers: 4},
			expect: []string{
				"pm.start_servers = 4", "pm.min_spare_servers = 4", "pm.max_spare_servers = 4",
			},
		},
		{
			name: "start only",
			pool: model.PHPFPMBudget{MaxChildren: 8, StartServers: 6},
			expect: []string{
				"pm.start_servers = 6", "pm.min_spare_servers = 1", "pm.max_spare_servers = 6",
			},
		},
		{
			name: "max spare only",
			pool: model.PHPFPMBudget{MaxChildren: 8, MaxSpareServers: 2},
			expect: []string{
				"pm.start_servers = 2", "pm.min_spare_servers = 1", "pm.max_spare_servers = 2",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Pool = &tc.pool
			got, err := Render(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.expect {
				if !strings.Contains(string(got), want) {
					t.Fatalf("missing %q in:\n%s", want, got)
				}
			}
		})
	}
}

func TestRenderFPMConfigRejectsInvalidPoolBudget(t *testing.T) {
	cases := []model.PHPFPMBudget{
		{Mode: "ondemand", MaxChildren: 8},
		{Mode: "dynamic", MaxChildren: 0, StartServers: 1},
		{Mode: "dynamic", MaxChildren: 8, MinSpareServers: 6, MaxSpareServers: 4},
		{Mode: "dynamic", MaxChildren: 8, StartServers: 1, MinSpareServers: 4, MaxSpareServers: 6},
		{Mode: "dynamic", MaxChildren: 8, StartServers: 7, MinSpareServers: 1, MaxSpareServers: 6},
		{Mode: "dynamic", MaxChildren: 4, MaxSpareServers: 5},
		{Mode: "dynamic", MaxChildren: 8, ListenBacklog: 65536},
	}
	for _, budget := range cases {
		t.Run(budget.Mode, func(t *testing.T) {
			cfg := validConfig()
			cfg.Pool = &budget
			if _, err := Render(cfg); !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("Render error = %v, want ErrConfigInvalid", err)
			}
		})
	}
}

func TestRenderFPMConfigQuotesEscapesAndSortsEnv(t *testing.T) {
	cfg := validConfig()
	cfg.Env = map[string]string{
		"ZED":     "last",
		"ALPHA":   `a "quoted" \ path`,
		"EMPTY":   "",
		"WITH_EQ": "a=b",
	}
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`env[ALPHA] = "a \"quoted\" \\ path"`,
		`env[EMPTY] = ""`,
		`env[WITH_EQ] = "a=b"`,
		`env[ZED] = "last"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if !(strings.Index(text, "env[ALPHA]") < strings.Index(text, "env[EMPTY]") &&
		strings.Index(text, "env[EMPTY]") < strings.Index(text, "env[WITH_EQ]") &&
		strings.Index(text, "env[WITH_EQ]") < strings.Index(text, "env[ZED]")) {
		t.Fatalf("env lines are not sorted:\n%s", text)
	}
}

func TestRenderFPMConfigEscapesDollarExpansion(t *testing.T) {
	cfg := validConfig()
	cfg.DocumentRoot = "/home/u/site/${HOME}"
	cfg.Env = map[string]string{
		"LITERAL":   "literal ${HOME} value",
		"DOLLAR":    "price $5",
		"BACKSLASH": `a \ path`,
		"BOTH":      `a \$ b`,
	}
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`chdir = "/home/u/site/\${HOME}"`,
		`env[LITERAL] = "literal \${HOME} value"`,
		`env[DOLLAR] = "price \$5"`,
		`env[BACKSLASH] = "a \\ path"`,
		`env[BOTH] = "a \\\$ b"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestRenderFPMConfigOptionalUserGroup(t *testing.T) {
	cfg := validConfig()
	cfg.User = "www-data"
	cfg.Group = "www-data"
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "user = www-data") || !strings.Contains(text, "group = www-data") {
		t.Fatalf("missing user/group in:\n%s", text)
	}

	without, err := Render(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "user =") || strings.Contains(string(without), "group =") {
		t.Fatalf("user/group emitted when unset:\n%s", without)
	}
}

func TestRenderFPMConfigIPv6Listen(t *testing.T) {
	cfg := validConfig()
	cfg.ListenHost = "::1"
	cfg.ListenPort = 43999
	got, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "listen = [::1]:43999") {
		t.Fatalf("missing IPv6 listen in:\n%s", got)
	}
}

func TestRenderFPMConfigPortBoundaries(t *testing.T) {
	for _, port := range []int{portMin, portMax} {
		cfg := validConfig()
		cfg.ListenPort = port
		if _, err := Render(cfg); err != nil {
			t.Fatalf("port %d rejected: %v", port, err)
		}
	}
	for _, port := range []int{-1, 0, 42999, 44000, 65536} {
		cfg := validConfig()
		cfg.ListenPort = port
		if _, err := Render(cfg); !errors.Is(err, ErrConfigInvalid) {
			t.Fatalf("port %d error = %v, want ErrConfigInvalid", port, err)
		}
	}
}

func TestRenderFPMConfigRejectsUnsafeValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty listen host", func(c *Config) { c.ListenHost = "" }},
		{"hostname listen host", func(c *Config) { c.ListenHost = "localhost" }},
		{"listen host with slash", func(c *Config) { c.ListenHost = "127.0.0.1/evil" }},
		{"listen host newline", func(c *Config) { c.ListenHost = "127.0.0.1\nlisten = 0.0.0.0:1" }},
		{"listen host NUL", func(c *Config) { c.ListenHost = "127.0.0.1\x00" }},
		{"docroot newline", func(c *Config) { c.DocumentRoot = "/home/u/blog\nevil = yes" }},
		{"docroot carriage return", func(c *Config) { c.DocumentRoot = "/home/u/blog\r" }},
		{"docroot NUL", func(c *Config) { c.DocumentRoot = "/home/u/blog\x00" }},
		{"docroot relative", func(c *Config) { c.DocumentRoot = "public" }},
		{"docroot dot relative", func(c *Config) { c.DocumentRoot = "./public" }},
		{"docroot unclean", func(c *Config) { c.DocumentRoot = "/home/u/blog/../secret" }},
		{"php ini empty", func(c *Config) { c.PHPIni = "" }},
		{"php ini relative", func(c *Config) { c.PHPIni = "php.ini" }},
		{"pid file empty", func(c *Config) { c.PIDFile = "" }},
		{"pid file relative", func(c *Config) { c.PIDFile = "php-fpm.pid" }},
		{"error log relative", func(c *Config) { c.ErrorLog = "error.log" }},
		{"build root relative", func(c *Config) { c.BuildRoot = "php" }},
		{"env value newline", func(c *Config) { c.Env = map[string]string{"APP_ENV": "local\nclear_env = yes"} }},
		{"env value carriage return", func(c *Config) { c.Env = map[string]string{"APP_ENV": "local\r"} }},
		{"env value NUL", func(c *Config) { c.Env = map[string]string{"APP_ENV": "local\x00"} }},
		{"env key space", func(c *Config) { c.Env = map[string]string{"BAD KEY": "x"} }},
		{"env key bracket", func(c *Config) { c.Env = map[string]string{"BAD]": "x"} }},
		{"env key starts with digit", func(c *Config) { c.Env = map[string]string{"1BAD": "x"} }},
		{"env key empty", func(c *Config) { c.Env = map[string]string{"": "x"} }},
		{"site id newline", func(c *Config) { c.SiteID = "site\n[www]" }},
		{"user newline", func(c *Config) { c.User = "user\nclear_env = yes" }},
		{"group quote", func(c *Config) { c.Group = `group"` }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			if _, err := Render(cfg); !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("Render error = %v, want ErrConfigInvalid", err)
			}
		})
	}
}

func TestRenderFPMConfigRejectsControlCharacters(t *testing.T) {
	cfg := validConfig()
	cfg.DocumentRoot = "/home/u/blog/\x01public"
	if _, err := Render(cfg); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("control character error = %v, want ErrConfigInvalid", err)
	}
	cfg = validConfig()
	cfg.Env = map[string]string{"APP_ENV": "local\x1f"}
	if _, err := Render(cfg); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("env control character error = %v, want ErrConfigInvalid", err)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first, err := Render(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("Render produced different output for identical config")
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Fatal("Render output must end with a newline")
	}
}

func TestArgvExact(t *testing.T) {
	got := Argv("/opt/php/sbin/php-fpm", "/home/u/.devstack/sites/site/abc/php-fpm.conf", "/home/u/.devstack/sites/site/abc/php.ini")
	want := []string{
		"/opt/php/sbin/php-fpm",
		"--nodaemonize",
		"--fpm-config", "/home/u/.devstack/sites/site/abc/php-fpm.conf",
		"-c", "/home/u/.devstack/sites/site/abc/php.ini",
	}
	if len(got) != len(want) {
		t.Fatalf("Argv = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Argv[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestValidateMissingFilesWrapsErrConfigInvalid(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "php-fpm.conf")
	iniPath := filepath.Join(dir, "php.ini")
	err := Validate(testContext, "/bin/true", configPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate error = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "php-fpm.conf") {
		t.Fatalf("error %q does not name the missing config", err)
	}

	if err := os.WriteFile(configPath, []byte("[global]\ndaemonize = no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Validate(testContext, "/bin/true", configPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) || !strings.Contains(err.Error(), "php.ini") {
		t.Fatalf("missing ini error = %v, want ErrConfigInvalid naming php.ini", err)
	}
}

func TestValidateInvokesFPMAndClearsScanDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executing shell script without .cmd/.bat is not supported on Windows")
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "php-fpm.conf")
	iniPath := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(configPath, []byte("[global]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte("memory_limit = 256M\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "php-fpm")
	body := "#!/bin/sh\n" +
		"printf 'args=%s\\n' \"$*\" >&2\n" +
		"printf 'scan=%s\\n' \"$PHP_INI_SCAN_DIR\" >&2\n" +
		"printf 'boom: invalid pool value\\n' >&2\n" +
		"exit 78\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHP_INI_SCAN_DIR", "/must/not/leak")

	err := Validate(testContext, script, configPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate error = %v, want ErrConfigInvalid", err)
	}
	text := err.Error()
	for _, want := range []string{
		"args=-t -y " + configPath + " -c " + iniPath,
		"scan=",
		"boom: invalid pool value",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q does not contain %q", text, want)
		}
	}
	if strings.Contains(text, "/must/not/leak") {
		t.Fatalf("PHP_INI_SCAN_DIR leaked into the child environment: %q", text)
	}
}

func TestValidateRejectsPHPErrorWithZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executing shell script without .cmd/.bat is not supported on Windows")
	}
	dir := t.TempDir()
	configPath, iniPath := writeValidateInputs(t, dir)
	script := filepath.Join(dir, "php-fpm")
	body := "#!/bin/sh\n" +
		"printf \"PHP:  syntax error, unexpected '=' in /tmp/php.ini on line 1\\n\" >&2\n" +
		"printf '[ts] NOTICE: configuration file test is successful\\n' >&2\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Validate(testContext, script, configPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate error = %v, want ErrConfigInvalid", err)
	}
	for _, want := range []string{"PHP:", "syntax error", "test is successful"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not include %q", err, want)
		}
	}
}

func TestValidateAcceptsNoticeOnlyZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executing shell script without .cmd/.bat is not supported on Windows")
	}
	dir := t.TempDir()
	configPath, iniPath := writeValidateInputs(t, dir)
	script := filepath.Join(dir, "php-fpm")
	body := "#!/bin/sh\n" +
		"printf '[23-Oct-2026 00:00:00] NOTICE: configuration file test is successful\\n' >&2\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Validate(testContext, script, configPath, iniPath); err != nil {
		t.Fatalf("notice-only output was rejected: %v", err)
	}
}

func TestValidateTimesOut(t *testing.T) {
	old := validateTimeout
	validateTimeout = 150 * time.Millisecond
	defer func() { validateTimeout = old }()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "php-fpm.conf")
	iniPath := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(configPath, []byte("[global]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte("memory_limit = 256M\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "php-fpm")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := Validate(testContext, script, configPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate error = %v, want ErrConfigInvalid", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Validate took %v, want the timeout enforced", elapsed)
	}
}

func TestValidateRealFPM(t *testing.T) {
	fpmBin := findFPM(t)
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "php.ini")
	iniBody := "display_errors = On\nerror_reporting = E_ALL\nmemory_limit = 256M\n" +
		"upload_max_filesize = 64M\npost_max_size = 64M\nmax_execution_time = 120\n" +
		"date.timezone = UTC\n"
	if err := os.WriteFile(iniPath, []byte(iniBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		SiteID:       "site",
		BuildRoot:    filepath.Dir(filepath.Dir(fpmBin)),
		PHPIni:       iniPath,
		ListenHost:   "127.0.0.1",
		ListenPort:   43999,
		DocumentRoot: dir,
		ErrorLog:     filepath.Join(dir, "error.log"),
		PIDFile:      filepath.Join(dir, "php-fpm.pid"),
		Env:          map[string]string{"APP_ENV": "test"},
	}
	rendered, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "php-fpm.conf")
	if err := os.WriteFile(configPath, rendered, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Validate(testContext, fpmBin, configPath, iniPath); err != nil {
		t.Fatalf("real php-fpm rejected rendered config: %v\n%s", err, rendered)
	}

	badPath := filepath.Join(dir, "bad-php-fpm.conf")
	if err := os.WriteFile(badPath, []byte("[global]\ndaemonize = maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Validate(testContext, fpmBin, badPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("invalid config error = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "daemonize") {
		t.Fatalf("invalid config error = %v, want php-fpm output", err)
	}
}

func TestValidateRealFPMMalformedIni(t *testing.T) {
	fpmBin := findFPM(t)
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "malformed.ini")
	if err := os.WriteFile(iniPath, []byte("= value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		SiteID:       "site",
		BuildRoot:    filepath.Dir(filepath.Dir(fpmBin)),
		PHPIni:       iniPath,
		ListenHost:   "127.0.0.1",
		ListenPort:   43996,
		DocumentRoot: dir,
		ErrorLog:     filepath.Join(dir, "error.log"),
		PIDFile:      filepath.Join(dir, "php-fpm.pid"),
	}
	rendered, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "php-fpm.conf")
	if err := os.WriteFile(configPath, rendered, 0o600); err != nil {
		t.Fatal(err)
	}

	err = Validate(testContext, fpmBin, configPath, iniPath)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("malformed ini error = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("malformed ini error = %v, want PHP syntax error output", err)
	}
}

// TestRenderFPMConfigDollarValuesSurviveRealFPM uses php-fpm's -tt config dump
// to prove that escaped dollars are parsed literally: a site env value such as
// ${HOME} or a document root containing ${HOME} must not be expanded.
func TestRenderFPMConfigDollarValuesSurviveRealFPM(t *testing.T) {
	fpmBin := findFPM(t)
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(iniPath, []byte("memory_limit = 256M\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	documentRoot := filepath.Join(dir, "${HOME}")
	if err := os.Mkdir(documentRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		SiteID:       "site",
		BuildRoot:    filepath.Dir(filepath.Dir(fpmBin)),
		PHPIni:       iniPath,
		ListenHost:   "127.0.0.1",
		ListenPort:   43997,
		DocumentRoot: documentRoot,
		ErrorLog:     filepath.Join(dir, "error.log"),
		PIDFile:      filepath.Join(dir, "php-fpm.pid"),
		Env: map[string]string{
			"LITERAL":   "literal ${HOME} value",
			"DOLLAR":    "price $5",
			"BACKSLASH": `a \ path`,
			"BOTH":      `a \$ b`,
		},
	}
	rendered, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "php-fpm.conf")
	if err := os.WriteFile(configPath, rendered, 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(fpmBin, "-tt", "-y", configPath, "-c", iniPath)
	command.Env = withEnv(os.Environ(), "PHP_INI_SCAN_DIR", "")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("php-fpm -tt failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"chdir = " + documentRoot,
		"env[LITERAL] = literal ${HOME} value",
		"env[DOLLAR] = price $5",
		`env[BACKSLASH] = a \ path`,
		`env[BOTH] = a \$ b`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("php-fpm -tt output missing %q in:\n%s", want, text)
		}
	}
}

func writeValidateInputs(t *testing.T, dir string) (configPath, iniPath string) {
	t.Helper()
	configPath = filepath.Join(dir, "php-fpm.conf")
	iniPath = filepath.Join(dir, "php.ini")
	if err := os.WriteFile(configPath, []byte("[global]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte("memory_limit = 256M\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, iniPath
}

func findFPM(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/usr/local/php/sbin/php-fpm"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	t.Skip("php-fpm not found at /usr/local/php/sbin/php-fpm; skipping real FPM validation")
	return ""
}
