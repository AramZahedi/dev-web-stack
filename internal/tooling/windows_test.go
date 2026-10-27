package tooling

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestEnsureWindowsToolShimsWritesNonDotCmdFiles(t *testing.T) {
	bin := t.TempDir()
	if err := EnsureWindowsToolShims(bin, "devstack"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php.cmd", "composer.cmd"} {
		content, err := os.ReadFile(filepath.Join(bin, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.HasPrefix(name, ".") || !strings.Contains(string(content), `wsl.exe -d devstack --exec`) {
			t.Fatalf("unexpected %s shim: %q", name, content)
		}
		if !strings.Contains(string(content), "//wsl") {
			t.Fatalf("%s missing UNC path translation:\n%s", name, content)
		}
		if strings.Contains(string(content), "exit /b %ERRORLEVEL%") {
			t.Fatalf("%s should not contain trailing exit /b to avoid trapping Ctrl+C:\n%s", name, content)
		}
	}
}

func TestEnsureWindowsToolShimsPathWithSpaces(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "devstack tools bin")
	if err := EnsureWindowsToolShims(bin, "devstack"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php.cmd", "composer.cmd"} {
		content, err := os.ReadFile(filepath.Join(bin, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(content), `wsl.exe -d devstack --exec`) {
			t.Fatalf("unexpected %s content: %s", name, content)
		}
	}
}

func TestEnsureGlobalWindowsToolShimsUsesConfiguredDistro(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("wsl_distro: Ubuntu-Dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGlobalWindowsToolShims(home, ""); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, "bin", "php.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `wsl.exe -d Ubuntu-Dev`) {
		t.Fatalf("shim ignored configured distro: %s", content)
	}
}

func TestEnsureWindowsToolShimsRejectsUnsafeDistroName(t *testing.T) {
	if err := EnsureWindowsToolShims(t.TempDir(), `devstack & whoami`); err == nil {
		t.Fatal("unsafe distro name was accepted")
	}
}

func TestReadWSLDistroUsesConfiguredNameWithoutWritingConfig(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(configPath, []byte("wsl_distro: Ubuntu-Dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWSLDistro(home)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Ubuntu-Dev" {
		t.Fatalf("distro = %q, want configured name", got)
	}
	content, err := os.ReadFile(configPath)
	if err != nil || string(content) != "wsl_distro: Ubuntu-Dev\n" {
		t.Fatalf("config was modified: %q, %v", content, err)
	}
}

func TestReadWSLDistroRejectsEmptyHome(t *testing.T) {
	if _, err := ReadWSLDistro(""); err == nil {
		t.Fatal("empty home was accepted")
	}
}

func TestBuildWSLExecArgsQuotesWorkingDirectoryEnvironmentAndArguments(t *testing.T) {
	args, err := BuildWSLExecArgs("devstack", "/mnt/c/Projects/My App", map[string]string{"APP_MODE": "it's safe"}, []string{"php", "-r", "echo $x"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 6 || args[0] != "-d" || args[1] != "devstack" || args[2] != "--" || args[3] != "bash" || args[4] != "-lc" {
		t.Fatalf("wsl arguments = %#v", args)
	}
	script := args[5]
	for _, want := range []string{"cd -- '/mnt/c/Projects/My App'", "export APP_MODE='it'\\''s safe'", "exec 'php' '-r' 'echo $x'"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q: %s", want, script)
		}
	}
}

func TestBuildWSLExecArgsRejectsUnsafeInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		env  map[string]string
		argv []string
	}{
		{name: "relative path", path: "project", argv: []string{"pwd"}},
		{name: "parent path", path: "/home/dev/../other", argv: []string{"pwd"}},
		{name: "invalid environment key", path: "/home/dev/project", env: map[string]string{"BAD-KEY": "x"}, argv: []string{"pwd"}},
		{name: "empty command", path: "/home/dev/project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildWSLExecArgs("devstack", tc.path, tc.env, tc.argv, false); err == nil {
				t.Fatal("BuildWSLExecArgs accepted invalid input")
			}
		})
	}
}

func TestEnsureWindowsSiteShimsWritesExpectedCmdFiles(t *testing.T) {
	bin := t.TempDir()
	site := model.Site{
		Name:        "my-site",
		BackendPath: "/mnt/c/Users/Aram/Projects/my site",
		Env:         map[string]string{"APP_ENV": "local", "DB_NAME": "my app db"},
		PHPBuild:    "php@8.4.27-r1",
	}
	if err := EnsureWindowsSiteShims(bin, "devstack", site); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php.cmd", "composer.cmd", "shell.cmd", "exec.cmd"} {
		content, err := os.ReadFile(filepath.Join(bin, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(content)
		if strings.HasPrefix(name, ".") || !strings.Contains(text, `wsl.exe -d devstack`) {
			t.Fatalf("unexpected %s content: %s", name, text)
		}
		if !strings.Contains(text, "cd -- '/mnt/c/Users/Aram/Projects/my site'") {
			t.Fatalf("%s does not quote backend path with spaces:\n%s", name, text)
		}
		if name != "shell.cmd" && !strings.Contains(text, "//wsl") {
			t.Fatalf("%s missing UNC path translation:\n%s", name, text)
		}
		if strings.Contains(text, "exit /b %ERRORLEVEL%") {
			t.Fatalf("%s should not contain trailing exit /b to avoid trapping Ctrl+C:\n%s", name, text)
		}
	}
}

func TestEnsureWindowsSiteShimsPathWithSpaces(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "devstack tools sites my-site bin")
	site := model.Site{
		Name:        "my-site",
		BackendPath: "/mnt/c/Users/Aram/Projects/my site",
	}
	if err := EnsureWindowsSiteShims(bin, "devstack", site); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php.cmd", "composer.cmd", "shell.cmd", "exec.cmd"} {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Fatalf("expected %s in directory with spaces: %v", name, err)
		}
	}
}

func TestBuildWSLExecArgsQuotesSpacesAndQuotesInArguments(t *testing.T) {
	args, err := BuildWSLExecArgs("devstack", "/mnt/c/Path With Spaces", map[string]string{"GREETING": "hello world"}, []string{"php", "artisan", "user:create", "--name=O'Connor & Smith", "with spaces"}, false)
	if err != nil {
		t.Fatal(err)
	}
	script := args[5]
	for _, want := range []string{
		"cd -- '/mnt/c/Path With Spaces'",
		"export GREETING='hello world'",
		"exec 'php' 'artisan' 'user:create' '--name=O'\\''Connor & Smith' 'with spaces'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestWindowsShimArgumentTranslationInBash(t *testing.T) {
	var bashPath string
	if gitBash := filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"); fileExists(gitBash) {
		bashPath = gitBash
	} else if p, err := exec.LookPath("bash"); err == nil && !strings.Contains(strings.ToLower(p), "windowsapps") {
		bashPath = p
	}
	if bashPath == "" {
		t.Skip("bash is not available")
	}
	bin := t.TempDir()
	if err := EnsureWindowsToolShims(bin, "devstack"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(bin, "php.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	idx := strings.Index(text, `bash -c "`)
	if idx < 0 {
		t.Fatalf("bash -c not found in php.cmd: %s", text)
	}
	scriptPart := text[idx+len(`bash -c "`):]
	endIdx := strings.LastIndex(scriptPart, `" _ %*`)
	if endIdx < 0 {
		t.Fatalf("closing delimiter not found in php.cmd: %s", scriptPart)
	}
	unescapedScript := strings.ReplaceAll(scriptPart[:endIdx], `\"`, `"`)
	// Replace "exec php" with "printf '%s\n'" to inspect the translated arguments
	testScript := strings.Replace(unescapedScript, `exec php "${args[@]}"`, `printf '%s\n' "${args[@]}"`, 1)

	tests := []struct {
		arg  string
		want string
	}{
		{`C:\Projects\app`, `/mnt/c/Projects/app`},
		{`c:/Projects/app`, `/mnt/c/Projects/app`},
		{`--opt=C:\Users\Aram`, `--opt=/mnt/c/Users/Aram`},
		{`\\wsl.localhost\devstack\data\devstack\projects\app\artisan`, `/data/devstack/projects/app/artisan`},
		{`\\wsl$\devstack\home\user\site`, `/home/user/site`},
		{`//wsl.localhost/devstack/var/www`, `/var/www`},
		{`--working-dir=\\wsl.localhost\devstack\data`, `--working-dir=/data`},
		{`\Users\Aram\test`, `/mnt/c/Users/Aram/test`},
		{`/Users/Aram/test`, `/mnt/c/Users/Aram/test`},
		{`--name=Smith`, `--name=Smith`},
		{`-r`, `-r`},
		{`echo "hello";`, `echo "hello";`},
	}

	for _, tc := range tests {
		cmd := exec.Command(bashPath, "-c", testScript, "_", tc.arg)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash failed on %q: %v, output: %s", tc.arg, err, string(out))
		}
		got := strings.TrimSpace(string(out))
		if got != tc.want {
			t.Errorf("translate(%q) = %q, want %q", tc.arg, got, tc.want)
		}
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
