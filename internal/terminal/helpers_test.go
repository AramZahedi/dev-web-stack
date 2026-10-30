package terminal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newBufferLogger(buffer *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(&bufferWriter{buffer: buffer}, nil))
}

type bufferWriter struct {
	buffer *strings.Builder
}

func (w *bufferWriter) Write(p []byte) (int, error) {
	return w.buffer.Write(p)
}

var _ io.Writer = (*bufferWriter)(nil)

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// testPHPBuild creates a fake native PHP build whose php binary prints a
// marker. The site shim must win over the global PHP shim.
func testPHPBuild(t *testing.T, home string) model.RuntimeBuild {
	t.Helper()
	root := filepath.Join(home, "runtimes", "php", "8.4.0")
	writeExecutable(t, filepath.Join(root, "bin", "php"), "#!/bin/sh\nprintf 'SITE_PHP\\n'\n")
	writeExecutable(t, filepath.Join(root, "bin", "php-config"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(root, "bin", "phpize"), "#!/bin/sh\nexit 0\n")
	return model.RuntimeBuild{
		ID:      "php@8.4.0",
		Kind:    "php",
		Version: "8.4.0",
		Backend: model.BackendLocal,
		Path:    root,
	}
}

func testNodeBuild(t *testing.T, home string) model.RuntimeBuild {
	t.Helper()
	root := filepath.Join(home, "runtimes", "node", "22.1.0")
	writeExecutable(t, filepath.Join(root, "bin", "node"), "#!/bin/sh\nprintf 'SITE_NODE\\n'\n")
	writeExecutable(t, filepath.Join(root, "bin", "npm"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(root, "bin", "npx"), "#!/bin/sh\nexit 0\n")
	return model.RuntimeBuild{
		ID:      "node@22.1.0",
		Kind:    "node",
		Version: "22.1.0",
		Backend: model.BackendLocal,
		Path:    root,
	}
}

// testGlobalPHPScript installs the machine-wide PHP shim. A site shell must
// resolve its own shim first.
func testGlobalPHPScript(t *testing.T, paths config.Paths) {
	t.Helper()
	writeExecutable(t, filepath.Join(paths.Tools, "php-default", "bin", "php"), "#!/bin/sh\nprintf 'GLOBAL_PHP\\n'\n")
}

func testPHPFixture(t *testing.T, home string) (config.Paths, model.Site, model.RuntimeBuild, sites.PHPInfo) {
	t.Helper()
	paths := config.NewPaths(home)
	project := filepath.Join(paths.Projects, "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	hash := strings.Repeat("a", 64)
	ini := filepath.Join(paths.Sites, "site-1", hash, "php.ini")
	writeFile(t, ini, "[PHP]\nmemory_limit = 512M\n")
	build := testPHPBuild(t, home)
	nodeBuild := testNodeBuild(t, home)
	site := model.Site{
		ID:            "site-1",
		Name:          "app",
		Kind:          model.SitePHP,
		HostPath:      project,
		BackendPath:   project,
		PHPVersion:    "8.4",
		PHPBuild:      build.ID,
		PHPConfigHash: hash,
		DocumentRoot:  "",
		Env:           map[string]string{"APP_ENV": "local"},
		NodeToolchain: &model.NodeToolchain{
			Version: nodeBuild.Version, BuildID: nodeBuild.ID,
			Source: model.NodeSourceGlobal, Installed: true,
		},
	}
	info := sites.PHPInfo{
		Site:         site,
		Build:        build,
		GeneratedIni: "[PHP]\nmemory_limit = 512M\n",
		ConfigHash:   hash,
	}
	return paths, site, build, info
}

func testNodeFixture(t *testing.T, home string) (config.Paths, model.Site, model.RuntimeBuild) {
	t.Helper()
	paths := config.NewPaths(home)
	project := filepath.Join(paths.Projects, "node-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	build := testNodeBuild(t, home)
	site := model.Site{
		ID:          "site-node",
		Name:        "node-app",
		Kind:        model.SiteNode,
		HostPath:    project,
		BackendPath: project,
		NodeVersion: "22",
	}
	return paths, site, build
}

type staticRuntimes []model.RuntimeBuild

func (s staticRuntimes) List(context.Context) ([]model.RuntimeBuild, error) {
	return []model.RuntimeBuild(s), nil
}

type staticPHP struct {
	info sites.PHPInfo
	err  error
}

func (p staticPHP) PHPInfo(context.Context, string) (sites.PHPInfo, error) {
	return p.info, p.err
}

func testDeps(paths config.Paths) Deps {
	nodeDefault := model.RuntimeBuild{
		ID: "node@22.1.0", Kind: "node", Version: "22.1.0", Backend: model.BackendLocal,
		Path: filepath.Join(paths.Home, "runtimes", "node", "22.1.0"),
	}
	return Deps{
		Paths:       paths,
		NodeDefault: &nodeDefault,
		UID:         os.Getuid(),
		Shell:       "/bin/sh",
		BasePATH:    os.Getenv("PATH"),
		Now:         time.Now,
		Logger:      testLogger(),
		IdleTimeout: time.Minute,
		MaxDuration: time.Minute,
	}
}

func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	return values
}
