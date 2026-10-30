package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/build"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

func TestPHPCliDefault(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		phpDefaultStatus: tooling.PHPDefaultStatus{Configured: "8.4", Build: &model.RuntimeBuild{ID: "php@8.4.12-r1"}, ShimInstalled: true},
	}
	out := runCLI(t, fake, "php", "default", "8.4")
	if fake.phpDefaultSet != "8.4" || fake.phpDefaultForce {
		t.Fatalf("set = %q force=%v", fake.phpDefaultSet, fake.phpDefaultForce)
	}
	if !strings.Contains(out, "8.4") {
		t.Fatalf("output = %q", out)
	}

	out = runCLI(t, fake, "php", "default", "--status")
	if !strings.Contains(out, "php@8.4.12-r1") {
		t.Fatalf("status output = %q", out)
	}

	out = runCLI(t, fake, "php", "default", "--status", "--json")
	var status tooling.PHPDefaultStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if status.Configured != "8.4" {
		t.Fatalf("status = %+v", status)
	}

	fake.phpClearStatus = tooling.PHPDefaultStatus{
		Build:         &model.RuntimeBuild{ID: "php@8.4.12-r1", Version: "8.4.12"},
		GlobalIni:     "/home/config/cli/php.ini",
		ShimInstalled: true,
	}
	out = runCLI(t, fake, "php", "default", "--clear")
	if fake.phpDefaultClear != 1 {
		t.Fatalf("clear calls = %d", fake.phpDefaultClear)
	}
	if !strings.Contains(out, "php@8.4.12-r1") || !strings.Contains(out, "true") {
		t.Fatalf("clear output does not show the fallback build:\n%s", out)
	}
	fake.phpClearStatus = tooling.PHPDefaultStatus{}
	out = runCLI(t, fake, "php", "default", "--clear")
	if !strings.Contains(out, "No PHP build is installed") {
		t.Fatalf("clear output does not report the missing build:\n%s", out)
	}

	code, _, _ := runCLIExit(t, fake, "php", "default")
	if code == 0 {
		t.Fatal("empty default accepted")
	}
}

func TestToolCLI(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{tools: []helperproto.ToolStatus{
		{ID: helperproto.ToolComposer, Installed: true, Managed: true, Version: "2.10.3", WrapperPath: "/usr/local/bin/composer"},
	}}
	out := runCLI(t, fake, "tool", "list")
	for _, want := range []string{"composer", "2.10.3", "/usr/local/bin/composer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}

	out = runCLI(t, fake, "tool", "list", "--json")
	var tools []helperproto.ToolStatus
	if err := json.Unmarshal([]byte(out), &tools); err != nil || len(tools) != 1 {
		t.Fatalf("json err=%v output=%q", err, out)
	}

	runCLI(t, fake, "tool", "install", "composer", "--force")
	if fake.toolInstallID != "composer" || !fake.toolInstallForce {
		t.Fatalf("install = %q force=%v", fake.toolInstallID, fake.toolInstallForce)
	}
	runCLI(t, fake, "tool", "remove", "composer")
	if fake.toolRemoveID != "composer" {
		t.Fatalf("remove = %q", fake.toolRemoveID)
	}
	code, _, _ := runCLIExit(t, fake, "tool", "install", "nginx")
	if code == 0 {
		t.Fatal("unknown tool accepted")
	}
}

func TestSiteBuildCLI(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		buildResult: build.Result{Site: "app", Success: true, DurationMS: 120, LogPath: "/tmp/log"},
	}
	out := runCLI(t, fake, "site", "build", "app")
	if fake.buildRef != "app" {
		t.Fatalf("build ref = %q", fake.buildRef)
	}
	if !strings.Contains(out, "app") || !strings.Contains(out, "120") {
		t.Fatalf("output = %q", out)
	}

	out = runCLI(t, fake, "site", "build", "app", "--json")
	var result build.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if !result.Success || result.DurationMS != 120 {
		t.Fatalf("result = %+v", result)
	}

	fake.buildResult = build.Result{Site: "app", Success: false, Error: "node failed"}
	code, _, stderr := runCLIExit(t, fake, "site", "build", "app")
	if code == 0 || !strings.Contains(stderr, "node failed") {
		t.Fatalf("failure code=%d stderr=%q", code, stderr)
	}
}

func shellFixture(t *testing.T) (*fakeClient, string, model.Site) {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	phpRoot := filepath.Join(home, "runtimes", "php", "8.4.12-linux-x64")
	if err := os.MkdirAll(filepath.Join(phpRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php", "php-config", "phpize"} {
		path := filepath.Join(phpRoot, "bin", name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	nodeRoot := filepath.Join(home, "runtimes", "node", "22.11.0-linux-x64")
	if err := os.MkdirAll(filepath.Join(nodeRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		path := filepath.Join(nodeRoot, "bin", name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	site := model.Site{
		ID:            "site-1",
		Name:          "app",
		HostPath:      project,
		Kind:          model.SitePHP,
		PHPVersion:    "8.4",
		PHPBuild:      "php@8.4.12-r1",
		PHPConfigHash: strings.Repeat("d", 64),
		Env:           map[string]string{"APP_ENV": "local"},
		NodeToolchain: &model.NodeToolchain{
			Version: "22.11.0", BuildID: "node@22.11.0",
			Source: model.NodeSourceGlobal, Installed: true,
		},
	}
	generation := filepath.Join(home, "sites", site.ID, site.PHPConfigHash)
	if err := os.MkdirAll(generation, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(generation, "php.ini")
	if err := os.WriteFile(ini, []byte("[PHP]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	builds := []model.RuntimeBuild{
		{ID: "php@8.4.12-r1", Kind: "php", Version: "8.4.12", Backend: model.BackendLocal, Path: phpRoot},
		{ID: "node@22.11.0", Kind: "node", Version: "22.11.0", Backend: model.BackendLocal, Path: nodeRoot},
	}
	fake := &fakeClient{
		sites:    []model.Site{site},
		runtimes: builds,
		phpInfo:  sites.PHPInfo{Site: site, Build: builds[0], ConfigHash: site.PHPConfigHash, GeneratedIni: "[PHP]\n"},
	}
	return fake, home, site
}

func TestShellInitAndExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-init export tests are Linux-only; Windows uses WSL exec")
	}
	useTestSeams(t)
	fake, home, site := shellFixture(t)

	out := runCLI(t, fake, "--home", home, "shell-init", "--site", "app")
	for _, want := range []string{
		"export DEVSTACK_SITE='app'",
		"export DEVSTACK_HOME='" + home + "'",
		"export DEVSTACK_PROJECT='" + site.HostPath + "'",
		"export PHPRC='" + filepath.Join(home, "sites", site.ID, site.PHPConfigHash, "php.ini") + "'",
		"export PATH='",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("shell-init missing %q:\n%s", want, out)
		}
	}

	if runtime.GOOS == "windows" {
		t.Skip("executing shell scripts without Windows shim is not supported on Windows")
	}

	script := filepath.Join(t.TempDir(), "print-env")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$PHPRC\"\nprintf '%s\\n' \"$(pwd)\"\nprintf '%s\\n' \"$DEVSTACK_SITE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out = runCLI(t, fake, "--home", home, "exec", "--site", "app", "--", script)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("exec output = %q", out)
	}
	if lines[0] != filepath.Join(home, "sites", site.ID, site.PHPConfigHash, "php.ini") {
		t.Fatalf("PHPRC = %q", lines[0])
	}
	if lines[1] != site.HostPath {
		t.Fatalf("cwd = %q", lines[1])
	}
	if lines[2] != "app" {
		t.Fatalf("DEVSTACK_SITE = %q", lines[2])
	}

	// A relative executable resolves against the site project, not the CLI cwd.
	local := filepath.Join(site.HostPath, "print-env-local")
	if err := os.WriteFile(local, []byte("#!/bin/sh\nprintf '%s\\n' \"$DEVSTACK_SITE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out = runCLI(t, fake, "--home", home, "exec", "--site", "app", "--", "./print-env-local")
	if strings.TrimSpace(out) != "app" {
		t.Fatalf("relative exec output = %q", out)
	}
}

func TestShellAndExecResolveByCWD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-init CWD export resolution is Linux-only; Windows uses WSL exec")
	}
	useTestSeams(t)
	fake, home, site := shellFixture(t)
	sub := filepath.Join(site.HostPath, "src", "pages")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	out := runCLI(t, fake, "--home", home, "shell-init")
	if !strings.Contains(out, "export DEVSTACK_SITE='app'") {
		t.Fatalf("cwd resolution failed:\n%s", out)
	}
}

func TestShellRefusesRoot(t *testing.T) {
	useTestSeams(t)
	fake, home, _ := shellFixture(t)
	old := currentEUID
	currentEUID = func() int { return 0 }
	t.Cleanup(func() { currentEUID = old })
	code, _, stderr := runCLIExit(t, fake, "--home", home, "shell", "--site", "app")
	if code == 0 || !strings.Contains(stderr, "root") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
