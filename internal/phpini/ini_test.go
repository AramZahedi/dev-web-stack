package phpini

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()

// fakeRunner returns a canned result and satisfies the plan's value-style use.
type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Run(context.Context, string, []string, []string) ([]byte, error) {
	return f.out, f.err
}

type runnerCall struct {
	name string
	args []string
	env  []string
}

// recordingRunner captures the exact invocation passed to the runner.
type recordingRunner struct {
	out   []byte
	err   error
	calls []runnerCall
}

func (f *recordingRunner) Run(_ context.Context, name string, args, env []string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{
		name: name,
		args: append([]string(nil), args...),
		env:  append([]string(nil), env...),
	})
	return f.out, f.err
}

func TestComposeOrderManagedBlockAndCAAliases(t *testing.T) {
	got, err := Compose(Config{
		Base:            []byte("; build base\nmemory_limit = 128M\n"),
		VersionDefaults: map[string]string{"memory_limit": "256M", "date.timezone": "UTC"},
		Overrides:       map[string]string{"memory_limit": "512M"},
		Raw:             "; site comment\nauto_prepend_file = \"/srv/bootstrap.php\"\n",
		Extensions:      []Extension{{Name: "opcache", Type: "zend_extension", SO: "opcache.so"}},
		ExtensionDir:    "/opt/php/lib/php/extensions",
		CAFile:          "/home/u/.devstack/pki/ca-bundle.crt",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	baseAt := strings.Index(text, "; build base")
	defaultsAt := strings.Index(text, BeginDefaultsMarker)
	siteAt := strings.Index(text, BeginSiteMarker)
	extAt := strings.Index(text, BeginExtensionsMarker)
	if !(baseAt >= 0 && baseAt < defaultsAt && defaultsAt < siteAt && siteAt < extAt) {
		t.Fatalf("order wrong:\n%s", text)
	}
	for _, want := range []string{
		"memory_limit = 512M", "; site comment", "auto_prepend_file = \"/srv/bootstrap.php\"",
		"extension_dir = \"/opt/php/lib/php/extensions\"", "zend_extension=opcache.so",
		"curl.cainfo = \"/home/u/.devstack/pki/ca-bundle.crt\"", "openssl.cafile = \"/home/u/.devstack/pki/ca-bundle.crt\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, EndExtensionsMarker+"\n") {
		t.Fatalf("missing trailing extensions marker:\n%s", text)
	}
}

func TestComposeRejectsInjection(t *testing.T) {
	bad := []Config{
		{Overrides: map[string]string{"memory_limit": "1G\nextension=evil.so"}},
		{Overrides: map[string]string{"extension": "evil.so"}},
		{Overrides: map[string]string{"extension_dir": "/tmp"}},
		{Overrides: map[string]string{"ZEND_EXTENSION": "evil.so"}},
		{Overrides: map[string]string{"memory_limit": "1G\x00"}},
		{Overrides: map[string]string{"memory_limit": "1G\r"}},
		{Overrides: map[string]string{"memory_limit": "1G ; " + BeginDefaultsMarker}},
		{VersionDefaults: map[string]string{"extension": "evil.so"}},
		{VersionDefaults: map[string]string{"memory_limit": "1G\nextension=evil.so"}},
		{Raw: BeginExtensionsMarker},
		{Raw: "zend_extension=evil.so\n"},
		{Raw: "extension_dir = \"/tmp\"\n"},
		{Raw: "extension = \"evil.so\"\n"},
		{Raw: "memory_limit = 1G\x00\n"},
		{Raw: "memory_limit = 1G\r\n"},
		{Extensions: []Extension{{Name: "x", Type: "bogus", SO: "x.so"}}},
		{Extensions: []Extension{{Name: "x", Type: "extension", SO: "x.so\nzend_extension=evil.so"}}},
		{Extensions: []Extension{{Name: "x", Type: "extension", SO: ""}}},
		{ExtensionDir: "/tmp\n"},
		{CAFile: "/tmp/ca\n"},
	}
	for _, cfg := range bad {
		if _, err := Compose(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		} else if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("error does not wrap ErrInvalidConfig: %v", err)
		}
	}
}

func TestComposeSortedDefaultsAndExplicitCAWins(t *testing.T) {
	got, err := Compose(Config{
		VersionDefaults: map[string]string{
			"memory_limit":  "256M",
			"curl.cainfo":   "/explicit/ca.pem",
			"date.timezone": "UTC",
		},
		CAFile: "/devstack/ca.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Count(text, "curl.cainfo") != 1 {
		t.Fatalf("explicit curl.cainfo was duplicated:\n%s", text)
	}
	if !strings.Contains(text, "curl.cainfo = /explicit/ca.pem") {
		t.Fatalf("explicit curl.cainfo lost:\n%s", text)
	}
	if !strings.Contains(text, "openssl.cafile = \"/devstack/ca.pem\"") {
		t.Fatalf("openssl.cafile alias missing:\n%s", text)
	}
	if strings.Index(text, "date.timezone") > strings.Index(text, "memory_limit = 256M") {
		t.Fatalf("defaults not sorted:\n%s", text)
	}
}

func TestComposeNormalizesBaseAndRaw(t *testing.T) {
	got, err := Compose(Config{
		Base: []byte("base = 1"),
		Raw:  "custom.foo = bar",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.HasPrefix(text, "base = 1\n[PHP]\n"+BeginDefaultsMarker+"\n") {
		t.Fatalf("base not normalized or not reset to [PHP]:\n%s", text)
	}
	raw, err := ExtractSiteSection(got)
	if err != nil {
		t.Fatal(err)
	}
	if raw != "custom.foo = bar\n" {
		t.Fatalf("extracted raw = %q", raw)
	}
	again, err := Compose(Config{Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	rawAgain, err := ExtractSiteSection(again)
	if err != nil || rawAgain != raw {
		t.Fatalf("round trip raw = %q, err = %v", rawAgain, err)
	}
	if !bytes.Contains(got, []byte(EndSiteMarker)) {
		t.Fatalf("site marker missing:\n%s", text)
	}
}

func TestComposeIgnoresBaseAndRawForCASuppression(t *testing.T) {
	got, err := Compose(Config{
		Base:   []byte("openssl.cafile = /base/ca.pem\n"),
		Raw:    "curl.cainfo = /raw/ca.pem\n",
		CAFile: "/devstack/ca.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`curl.cainfo = "/devstack/ca.pem"`,
		`openssl.cafile = "/devstack/ca.pem"`,
		"openssl.cafile = /base/ca.pem",
		"curl.cainfo = /raw/ca.pem",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestComposeOverrideSuppressesCAAlias(t *testing.T) {
	got, err := Compose(Config{
		Overrides: map[string]string{"openssl.cafile": "/site/ca.pem"},
		CAFile:    "/devstack/ca.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Count(text, "openssl.cafile") != 1 {
		t.Fatalf("overridden openssl.cafile was duplicated:\n%s", text)
	}
	if !strings.Contains(text, "openssl.cafile = /site/ca.pem") {
		t.Fatalf("site openssl.cafile lost:\n%s", text)
	}
	if !strings.Contains(text, `curl.cainfo = "/devstack/ca.pem"`) {
		t.Fatalf("curl.cainfo alias missing:\n%s", text)
	}
}

func TestComposeResetsSectionScope(t *testing.T) {
	got, err := Compose(Config{
		Base:            []byte("[PATH=/base]\nbase.foo = bar\n"),
		VersionDefaults: map[string]string{"memory_limit": "256M"},
		Raw:             "[HOST=example.test]\ncustom.foo = bar\n",
		Extensions:      []Extension{{Name: "opcache", Type: "zend_extension", SO: "opcache.so"}},
		ExtensionDir:    "/opt/php/lib/php/extensions",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	defaultsAt := strings.Index(text, BeginDefaultsMarker)
	firstReset := strings.Index(text, "[PHP]\n")
	if firstReset < 0 || defaultsAt < 0 || firstReset > defaultsAt {
		t.Fatalf("no [PHP] reset between base and defaults:\n%s", text)
	}
	endSiteAt := strings.Index(text, EndSiteMarker)
	extensionsAt := strings.Index(text, BeginExtensionsMarker)
	if endSiteAt < 0 || extensionsAt < 0 {
		t.Fatalf("markers missing:\n%s", text)
	}
	secondReset := strings.Index(text[endSiteAt:], "[PHP]\n")
	if secondReset < 0 || endSiteAt+secondReset > extensionsAt {
		t.Fatalf("no [PHP] reset between raw and managed extensions:\n%s", text)
	}
}

func TestComposePreservesExtensionOrder(t *testing.T) {
	got, err := Compose(Config{Extensions: []Extension{
		{Name: "igbinary", Type: "extension", SO: "igbinary.so"},
		{Name: "redis", Type: "extension", SO: "redis.so"},
		{Name: "xdebug", Type: "zend_extension", SO: "xdebug.so"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	igbinary := strings.Index(text, "extension=igbinary.so")
	redis := strings.Index(text, "extension=redis.so")
	xdebug := strings.Index(text, "zend_extension=xdebug.so")
	if !(igbinary >= 0 && igbinary < redis && redis < xdebug) {
		t.Fatalf("extension dependency order lost:\n%s", text)
	}
}

func TestExtractSiteSectionRoundTrip(t *testing.T) {
	generated, err := Compose(Config{Raw: "; keep\ncustom.foo = bar\n"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ExtractSiteSection(generated)
	if err != nil || raw != "; keep\ncustom.foo = bar\n" {
		t.Fatalf("raw = %q, err = %v", raw, err)
	}
	again, err := Compose(Config{Raw: raw})
	if err != nil || !strings.Contains(string(again), "custom.foo = bar") {
		t.Fatalf("%v", err)
	}
}

func TestExtractSiteSectionRejectsMissingMarkers(t *testing.T) {
	for _, data := range []string{
		"",
		"memory_limit = 1G\n",
		BeginSiteMarker + "\ncustom.foo = bar\n",
		EndSiteMarker + "\n",
		EndSiteMarker + "\n" + BeginSiteMarker + "\n",
		BeginSiteMarker + "\n" + BeginSiteMarker + "\n" + EndSiteMarker + "\n",
	} {
		if _, err := ExtractSiteSection([]byte(data)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%q: err = %v", data, err)
		}
	}
}

func TestParseLastValueCommentsAndSections(t *testing.T) {
	got, err := Parse([]byte("; leading comment\n[PHP]\nfoo = 1\nfoo = 2 ; trailing comment\nbar=\"a;b\"\nbaz = value\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["foo"] != "2" {
		t.Fatalf("foo = %q", got["foo"])
	}
	if got["bar"] != `"a;b"` {
		t.Fatalf("bar = %q", got["bar"])
	}
	if got["baz"] != "value" {
		t.Fatalf("baz = %q", got["baz"])
	}
	if _, ok := got["PHP"]; ok {
		t.Fatal("section header parsed as a directive")
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	_, err := Parse([]byte("foo = 1\nnot a directive\n"))
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Line != 2 {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("parse error must wrap ErrInvalidConfig")
	}
}

func TestValidateOverrides(t *testing.T) {
	if err := ValidateOverrides(map[string]string{
		"memory_limit":       "512M",
		"opcache.enable":     "1",
		"date.timezone":      "UTC",
		"session.save_path":  "/tmp",
		"some-custom_direct": "on",
	}); err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	for _, values := range []map[string]string{
		{"bad key": "1"},
		{"": "1"},
		{"memory_limit": "1G\n"},
		{"memory_limit": "1G\x00"},
		{"memory_limit": "1G\r"},
		{"extension": "evil.so"},
		{"zend_extension": "evil.so"},
		{"extension_dir": "/tmp"},
		{"memory_limit": BeginSiteMarker},
	} {
		err := ValidateOverrides(values)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%v: err = %v", values, err)
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%v: err = %T, want *ValidationError", values, err)
		}
	}
}

func TestValidateRaw(t *testing.T) {
	valid := "; comment\n\n[PHP] ; trailing comment\n[PATH=/usr/local/bin]\ncustom.foo = bar\nauto_prepend_file = \"/srv/a;b.php\"\nkey =\n"
	if err := ValidateRaw(valid); err != nil {
		t.Fatalf("valid raw rejected: %v", err)
	}
	for _, tc := range []struct {
		raw  string
		line int
	}{
		{"\nmemory_limit = 1G\nnot a directive\n", 3},
		{"extension = evil.so\n", 1},
		{"\nzend_extension=evil.so\n", 2},
		{"extension_dir = \"/tmp\"\n", 1},
		{"\n\n" + BeginExtensionsMarker + "\n", 3},
		{"bad key = value\n", 1},
		{"[unterminated\n", 1},
		{"[]\n", 1},
		{"memory_limit = 1G\x00\n", 1},
		{"\nmemory_limit = 1G\r\n", 2},
	} {
		err := ValidateRaw(tc.raw)
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%q: err = %v, want *ValidationError", tc.raw, err)
		}
		if validation.Line != tc.line {
			t.Fatalf("%q: line = %d, want %d", tc.raw, validation.Line, tc.line)
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%q: error must wrap ErrInvalidConfig", tc.raw)
		}
	}
}

func TestValidateRawRejectsAllManagedMarkers(t *testing.T) {
	for _, marker := range managedMarkers {
		err := ValidateRaw("; comment\n" + marker + "\n")
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Line != 2 {
			t.Fatalf("%q: err = %v", marker, err)
		}
	}
}

func TestLintReportsLine(t *testing.T) {
	iniPath := writeTempIni(t, "memory_limit = 1G\n")
	runner := fakeRunner{out: []byte("PHP: syntax error in " + iniPath + " on line 12")}
	err := Lint(ctx, runner, "/opt/php/bin/php", iniPath)
	var lint *LintError
	if !errors.As(err, &lint) || lint.Line != 12 {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("lint error must wrap ErrInvalidConfig")
	}
}

func TestLintInvocationAndSuccess(t *testing.T) {
	iniPath := writeTempIni(t, "memory_limit = 256M\n")
	runner := &recordingRunner{}
	if err := Lint(ctx, runner, "/opt/php/bin/php", iniPath); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call.name != "/opt/php/bin/php" {
		t.Fatalf("name = %q", call.name)
	}
	wantArgs := []string{"-c", iniPath, "-r", "exit(0);"}
	if strings.Join(call.args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("args = %q", call.args)
	}
	if strings.Join(call.env, "\x00") != "PHP_INI_SCAN_DIR=" {
		t.Fatalf("env = %q", call.env)
	}
}

func TestLintTreatsWarningsAndFailuresAsErrors(t *testing.T) {
	iniPath := writeTempIni(t, "memory_limit = 256M\n")
	for _, runner := range []Runner{
		fakeRunner{out: []byte("Warning: Invalid \"memory_limit\" setting in Unknown on line 0")},
		fakeRunner{out: []byte("Failed loading /nope.so\n"), err: errors.New("exit status 1")},
		fakeRunner{err: errors.New("exit status 1")},
	} {
		err := Lint(ctx, runner, "/opt/php/bin/php", iniPath)
		var lint *LintError
		if !errors.As(err, &lint) {
			t.Fatalf("err = %v, want *LintError", err)
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v must wrap ErrInvalidConfig", err)
		}
	}
}

func TestLintRejectsEmptyInputs(t *testing.T) {
	iniPath := writeTempIni(t, "memory_limit = 256M\n")
	if err := Lint(ctx, fakeRunner{}, "", iniPath); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty php binary: %v", err)
	}
	if err := Lint(ctx, fakeRunner{}, "/opt/php/bin/php", ""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty ini path: %v", err)
	}
}

func TestLintRequiresReadableIni(t *testing.T) {
	runner := &recordingRunner{}
	missing := filepath.Join(t.TempDir(), "missing.ini")
	err := Lint(ctx, runner, "/opt/php/bin/php", missing)
	var lint *LintError
	if !errors.As(err, &lint) || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing ini err = %v", err)
	}
	if !strings.Contains(lint.Output, "missing.ini") {
		t.Fatalf("missing ini output = %q", lint.Output)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called for a missing ini (%d calls)", len(runner.calls))
	}

	directory := t.TempDir()
	err = Lint(ctx, runner, "/opt/php/bin/php", directory)
	lint = nil
	if !errors.As(err, &lint) || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("directory ini err = %v", err)
	}
	if !strings.Contains(lint.Output, "regular file") {
		t.Fatalf("directory ini output = %q", lint.Output)
	}
	if len(runner.calls) != 0 {
		t.Fatal("runner was called for a directory")
	}
}

func TestLintRealPHP(t *testing.T) {
	phpBin := findPHP(t)
	dir := t.TempDir()
	valid := filepath.Join(dir, "php.ini")
	validBody := "display_errors = On\nerror_reporting = E_ALL\nmemory_limit = 256M\n" +
		"upload_max_filesize = 64M\npost_max_size = 64M\nmax_execution_time = 120\n" +
		"date.timezone = UTC\nopcache.enable = 1\n"
	if err := os.WriteFile(valid, []byte(validBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Lint(ctx, ExecRunner{}, phpBin, valid); err != nil {
		t.Fatalf("valid ini rejected by %s: %v", phpBin, err)
	}

	// A stray scan directory would inject a warning if PHP_INI_SCAN_DIR were
	// inherited instead of cleared.
	scan := t.TempDir()
	if err := os.WriteFile(filepath.Join(scan, "bad.ini"), []byte("memory_limit = banana\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHP_INI_SCAN_DIR", scan)
	if err := Lint(ctx, ExecRunner{}, phpBin, valid); err != nil {
		t.Fatalf("scan dir was not cleared: %v", err)
	}

	bad := filepath.Join(dir, "bad.ini")
	if err := os.WriteFile(bad, []byte("= value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Lint(ctx, ExecRunner{}, phpBin, bad)
	var lint *LintError
	if !errors.As(err, &lint) {
		t.Fatalf("malformed ini err = %v, want *LintError", err)
	}
	if lint.Line != 1 {
		t.Fatalf("malformed ini line = %d, want 1 (output %q)", lint.Line, lint.Output)
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("malformed ini error must wrap ErrInvalidConfig")
	}
}

func TestLintRealPHPComposedIni(t *testing.T) {
	phpBin := findPHP(t)
	raw := "; keep this comment\ncustom.unknown = preserved\n"
	composed, err := Compose(Config{
		Base: []byte("; build base\nmemory_limit = 128M\n"),
		VersionDefaults: map[string]string{
			"display_errors":              "On",
			"error_reporting":             "E_ALL",
			"memory_limit":                "256M",
			"upload_max_filesize":         "64M",
			"post_max_size":               "64M",
			"max_execution_time":          "120",
			"date.timezone":               "UTC",
			"opcache.enable":              "1",
			"opcache.validate_timestamps": "1",
			"opcache.revalidate_freq":     "0",
		},
		Overrides: map[string]string{"memory_limit": "512M"},
		Raw:       raw,
		CAFile:    "/nonexistent/devstack-ca.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	if extracted, err := ExtractSiteSection(composed); err != nil || !strings.HasSuffix(extracted, raw) {
		t.Fatalf("extracted = %q, err = %v", extracted, err)
	}
	path := filepath.Join(t.TempDir(), "php.ini")
	if err := os.WriteFile(path, composed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Lint(ctx, ExecRunner{}, phpBin, path); err != nil {
		t.Fatalf("composed ini rejected by %s:\n%s\nerror: %v", phpBin, composed, err)
	}
}

func TestLintRealPHPMissingIni(t *testing.T) {
	phpBin := findPHP(t)
	missing := filepath.Join(t.TempDir(), "missing.ini")
	err := Lint(ctx, ExecRunner{}, phpBin, missing)
	var lint *LintError
	if !errors.As(err, &lint) || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing ini err = %v, want *LintError wrapping ErrInvalidConfig", err)
	}
	if !strings.Contains(lint.Output, "missing.ini") {
		t.Fatalf("missing ini output = %q", lint.Output)
	}
}

func TestComposeManagedExtensionsGlobalAfterPathSection(t *testing.T) {
	phpBin := findPHP(t)
	composed, err := Compose(Config{
		Base:       []byte("[PATH=/base]\n"),
		Overrides:  map[string]string{"memory_limit": "256M"},
		Raw:        "[PATH=/srv/raw]\ncustom.foo = bar\n",
		Extensions: []Extension{{Name: "opcache", Type: "zend_extension", SO: "opcache.so"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "php.ini")
	if err := os.WriteFile(path, composed, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := `echo extension_loaded("Zend OPcache") ? "loaded" : "missing";`
	out, err := (ExecRunner{}).Run(ctx, phpBin, []string{"-c", path, "-r", probe}, []string{"PHP_INI_SCAN_DIR="})
	if err != nil {
		t.Fatalf("opcache probe failed: %v (%s)", err, out)
	}
	if strings.TrimSpace(string(out)) != "loaded" {
		t.Fatalf("opcache was path-scoped, not global:\n%s\nprobe output: %q", composed, out)
	}
}

func writeTempIni(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "php.ini")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func findPHP(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/usr/local/php/bin/php"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	if path, err := exec.LookPath("php"); err == nil {
		return path
	}
	t.Skip("php not found at /usr/local/php/bin/php or on PATH; skipping real PHP lint")
	return ""
}
