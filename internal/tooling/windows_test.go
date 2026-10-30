package tooling

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestWindowsSiteEnvironmentIncludesRuntimeBins(t *testing.T) {
	site := model.Site{
		Name:        "app",
		BackendPath: "/mnt/c/projects/app",
		PHPBuild:    "php@8.4.27-r1",
		NodeVersion: "24.22.1",
	}
	env := WindowsSiteEnvironment(site)
	path := env["PATH"]
	for _, want := range []string{
		"/data/devstack/tools/sites/app/bin",
		"/data/devstack/runtimes/php@8.4.27-r1/bin",
		"/data/devstack/runtimes/node@24.22.1/bin",
		"/data/devstack/bin",
	} {
		if !strings.Contains(path, want) {
			t.Fatalf("PATH = %q, missing %q", path, want)
		}
	}

	static := model.Site{
		Name:        "build-site",
		BackendPath: "/mnt/c/projects/build-site",
		Build:       &model.BuildProfile{NodeVersion: "22"},
	}
	if got := WindowsSiteEnvironment(static)["PATH"]; !strings.Contains(got, "/data/devstack/runtimes/node@22/bin") {
		t.Fatalf("build PATH = %q, missing the build Node runtime", got)
	}
}

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

func TestEnsureGlobalWindowsToolShimsUsesExactPHPBuild(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tools", "php-default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tools", "php-default", "VERSION"), []byte("php@8.4.27-r1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGlobalWindowsToolShims(home, "devstack"); err != nil {
		t.Fatal(err)
	}
	phpShim, err := os.ReadFile(filepath.Join(home, "bin", "php.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(phpShim), "/data/devstack/runtimes/php@8.4.27-r1/bin/php") {
		t.Fatalf("php.cmd did not pin the exact build: %s", phpShim)
	}
	composerShim, err := os.ReadFile(filepath.Join(home, "bin", "composer.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(composerShim), "/data/devstack/runtimes/php@8.4.27-r1/bin/php") ||
		!strings.Contains(string(composerShim), "/data/devstack/tools/composer/composer.phar") {
		t.Fatalf("composer.cmd did not pin the exact PHP build: %s", composerShim)
	}
}

func TestEnsureGlobalWindowsToolShimsForBuildPinsExactPath(t *testing.T) {
	home := t.TempDir()
	buildPath := "/data/devstack/runtimes/php@8.4.27-r1"
	if err := EnsureGlobalWindowsToolShimsForBuild(home, "devstack", buildPath); err != nil {
		t.Fatal(err)
	}
	phpShim, err := os.ReadFile(filepath.Join(home, "bin", "php.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(phpShim), buildPath+"/bin/php") {
		t.Fatalf("php.cmd did not use the supplied build path: %s", phpShim)
	}
	composerShim, err := os.ReadFile(filepath.Join(home, "bin", "composer.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(composerShim), buildPath+"/bin/php") ||
		!strings.Contains(string(composerShim), "/data/devstack/tools/composer/composer.phar") {
		t.Fatalf("composer.cmd did not use the supplied build: %s", composerShim)
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

func TestEnsureWindowsSiteShimsRejectsUnsafeEnvironmentNames(t *testing.T) {
	bin := t.TempDir()
	site := model.Site{
		ID: "site1", Name: "app", BackendPath: "/data/devstack/projects/app",
		Env: map[string]string{"EVIL; rm -rf /": "x"},
	}
	if err := EnsureWindowsSiteShims(bin, "devstack", site); err == nil {
		t.Fatal("EnsureWindowsSiteShims accepted a shell-unsafe environment name")
	}
	if _, err := os.Stat(filepath.Join(bin, "php.cmd")); !os.IsNotExist(err) {
		t.Fatalf("shims were written despite the invalid environment: %v", err)
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
	// Prefer a native POSIX bash when this test runs under WSL/Linux. Git
	// Bash is only the right interpreter for a native Windows test process.
	if runtime.GOOS == "windows" {
		if gitBash := filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"); fileExists(gitBash) {
			bashPath = gitBash
		}
	}
	if bashPath == "" {
		if p, err := exec.LookPath("bash"); err == nil && !strings.Contains(strings.ToLower(p), "windowsapps") {
			bashPath = p
		}
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
	// Execute the generated script from a file. Passing it through `bash -c`
	// from a Go test lets Git Bash's Windows command-line parser interpret the
	// backslashes before the script reaches bash, which does not match the
	// wsl.exe invocation used by the real .cmd shim.
	scriptFile := filepath.Join(t.TempDir(), "translate.sh")
	if err := os.WriteFile(scriptFile, []byte(testScript), 0o600); err != nil {
		t.Fatal(err)
	}

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
		cmd := exec.Command(bashPath, scriptFile, tc.arg)
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

func TestWindowsNodeShimUsesResolvedExactBuild(t *testing.T) {
	bin := t.TempDir()
	build := model.RuntimeBuild{
		ID:      "node@22.11.0",
		Kind:    "node",
		Version: "22.11.0",
		Backend: model.BackendWSL,
		Path:    "/data/devstack/runtimes/node@22.11.0",
	}
	if err := EnsureWindowsNodeShims(bin, "devstack", build); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		content, err := os.ReadFile(filepath.Join(bin, name+".cmd"))
		if err != nil {
			t.Fatalf("read %s.cmd: %v", name, err)
		}
		text := string(content)
		if !strings.Contains(text, `wsl.exe -d devstack --exec`) {
			t.Fatalf("%s.cmd does not call the managed distro:\n%s", name, text)
		}
		if want := "/data/devstack/runtimes/node@22.11.0/bin/" + name; !strings.Contains(text, want) {
			t.Fatalf("%s.cmd does not use the resolved exact build %s:\n%s", name, want, text)
		}
		if strings.Contains(text, "/runtimes/node@22/bin/") {
			t.Fatalf("%s.cmd used the version line instead of the exact build:\n%s", name, text)
		}
		if !strings.Contains(text, `\"${args[@]}\"`) || !strings.Contains(text, "%*") {
			t.Fatalf("%s.cmd does not forward argv:\n%s", name, text)
		}
		if !strings.Contains(text, "//wsl") {
			t.Fatalf("%s.cmd missing UNC path translation:\n%s", name, text)
		}
		if strings.Contains(text, "exit /b %ERRORLEVEL%") {
			t.Fatalf("%s.cmd should not trap Ctrl+C:\n%s", name, text)
		}
	}
}

func TestEnsureWindowsNodeShimsRejectsUnsafeInputs(t *testing.T) {
	build := model.RuntimeBuild{Kind: "node", Version: "22.11.0", Path: "/data/devstack/runtimes/node@22.11.0"}
	if err := EnsureWindowsNodeShims(t.TempDir(), `devstack & whoami`, build); err == nil {
		t.Fatal("unsafe distro name was accepted")
	}
	if err := EnsureWindowsNodeShims(t.TempDir(), "devstack", model.RuntimeBuild{Kind: "node", Version: "22.11.0"}); err == nil {
		t.Fatal("empty build path was accepted")
	}
	if err := EnsureWindowsNodeShims(t.TempDir(), "devstack", model.RuntimeBuild{Kind: "php", Version: "8.4.12", Path: "/data/php"}); err == nil {
		t.Fatal("PHP build was accepted as a Node build")
	}
}

func TestWindowsNodeShimsPinNpmAndNpxToExactNode(t *testing.T) {
	bash := testBashPath(t)
	root := t.TempDir()
	buildBin := filepath.Join(root, "bin")
	writeTestScript(t, filepath.Join(buildBin, "node"), "#!/bin/sh\nprintf '%s\\n' exact\n", 0o755)
	for _, name := range []string{"npm", "npx"} {
		writeTestScript(t, filepath.Join(buildBin, name), "#!/usr/bin/env node\n", 0o755)
	}
	decoy := t.TempDir()
	writeTestScript(t, filepath.Join(decoy, "node"), "#!/bin/sh\nprintf '%s\\n' decoy\n", 0o755)

	build := model.RuntimeBuild{
		ID:      "node@22.11.0",
		Kind:    "node",
		Version: "22.11.0",
		Backend: model.BackendWSL,
		Path:    bashTestPath(root),
	}
	bin := t.TempDir()
	if err := EnsureWindowsNodeShims(bin, "devstack", build); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"npm", "npx"} {
		content, err := os.ReadFile(filepath.Join(bin, name+".cmd"))
		if err != nil {
			t.Fatalf("read %s.cmd: %v", name, err)
		}
		text := string(content)
		idx := strings.Index(text, `bash -c "`)
		end := strings.LastIndex(text, `" _ %*`)
		if idx < 0 || end <= idx {
			t.Fatalf("%s.cmd has no inspectable bash script:\n%s", name, text)
		}
		script := strings.ReplaceAll(text[idx+len(`bash -c "`):end], `\"`, `"`)
		scriptFile := filepath.Join(t.TempDir(), name+".sh")
		if err := os.WriteFile(scriptFile, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(bash, scriptFile)
		// The decoy node is first in the ambient PATH. The Windows bash script
		// must prepend the exact build before npm/npx look up their interpreter.
		command.Env = append(os.Environ(), "PATH="+bashTestPath(decoy)+":"+bashTestPath(buildBin))
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("run %s.cmd script: %v\n%s", name, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "exact" {
			t.Fatalf("%s.cmd resolved node to %q, want the exact build node\nscript:\n%s", name, got, script)
		}
	}
}

func TestWSLComposerWrapperScriptContainsRequiredExtensions(t *testing.T) {
	script := WSLComposerWrapperScript()
	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Fatalf("wrapper script missing shebang: %s", script)
	}
	required := []string{
		"extension=phar.so",
		"extension=mbstring.so",
		"extension=openssl.so",
		"extension=curl.so",
		"/data/devstack/tools/composer/composer.phar",
		"readlink -f",
	}
	for _, req := range required {
		if !strings.Contains(script, req) {
			t.Errorf("wrapper script missing %q:\n%s", req, script)
		}
	}
}

func TestProvisionWSLComposerFilesCreatesExpectedLayout(t *testing.T) {
	wslRoot := t.TempDir()
	pharData := []byte("mock-composer-phar-content")
	version := "2.10.3"

	if err := ProvisionWSLComposerFiles(wslRoot, pharData, version); err != nil {
		t.Fatalf("ProvisionWSLComposerFiles failed: %v", err)
	}

	pharPath := filepath.Join(wslRoot, "tools", "composer", "composer.phar")
	gotPhar, err := os.ReadFile(pharPath)
	if err != nil {
		t.Fatalf("read composer.phar: %v", err)
	}
	if string(gotPhar) != string(pharData) {
		t.Errorf("composer.phar = %q, want %q", gotPhar, pharData)
	}

	versionPath := filepath.Join(wslRoot, "tools", "composer", "VERSION")
	gotVersion, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if strings.TrimSpace(string(gotVersion)) != version {
		t.Errorf("VERSION = %q, want %q", gotVersion, version)
	}

	for _, rel := range []string{
		filepath.Join("tools", "composer", "bin", "composer"),
		filepath.Join("bin", "composer"),
	} {
		wrapperPath := filepath.Join(wslRoot, rel)
		gotWrapper, err := os.ReadFile(wrapperPath)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(gotWrapper), "composer.phar") {
			t.Errorf("%s missing composer.phar reference:\n%s", rel, gotWrapper)
		}
	}
}
