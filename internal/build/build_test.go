package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func newBuildFixture(t *testing.T) (*Service, *store.Store, string, model.RuntimeBuild) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("executing /bin/sh fixtures is not supported on native Windows; builds run in WSL")
	}
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	nodeRoot := filepath.Join(home, "runtimes", "node", "22.11.0-linux-x64")
	if err := os.MkdirAll(filepath.Join(nodeRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo node-stdout\n" +
		"echo node-env-$NODE_ENV\n" +
		"echo node-path-$PATH\n" +
		"echo node-stderr >&2\n" +
		"mkdir -p dist\n" +
		"printf 'built' > dist/index.html\n"
	if err := os.WriteFile(filepath.Join(nodeRoot, "bin", "node"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(nodeRoot, "bin", "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := model.RuntimeBuild{
		ID:          "node@22.11.0",
		Kind:        "node",
		Version:     "22.11.0",
		Backend:     model.BackendLocal,
		Path:        nodeRoot,
		InstalledAt: time.Now().UTC(),
	}
	if err := st.UpsertRuntime(context.Background(), build); err != nil {
		t.Fatal(err)
	}

	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	svc := NewService(Deps{
		Store: st,
		Paths: paths,
		Now:   func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) },
		EUID:  func() int { return 1000 },
	})
	project := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return svc, st, project, build
}

func buildSite(project string, output string) model.Site {
	return model.Site{
		ID:       "site-1",
		Name:     "app",
		Kind:     model.SiteStatic,
		HostPath: project,
		Build: &model.BuildProfile{
			NodeVersion: "22",
			Command:     "node build.js",
			Output:      output,
			Env:         map[string]string{"NODE_ENV": "production"},
		},
	}
}

func TestCappedLogWriterBoundsTheBuildLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := newCappedLogWriter(file, 16)
	for _, chunk := range []string{"0123456789", "abcdefghij", "more"} {
		if n, err := writer.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if !writer.truncated {
		t.Fatal("writer did not record truncation")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 16+len("\n[devstack: build output truncated]\n") {
		t.Fatalf("log grew beyond the cap: %d bytes", len(data))
	}
	if !strings.Contains(string(data), "truncated") {
		t.Fatalf("truncation marker missing: %q", data)
	}
}

func TestBuildSuccessStreamsLogAndValidatesOutput(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	script := "#!/bin/sh\n" +
		"echo building\n" +
		"echo warning >&2\n" +
		"mkdir -p dist\n" +
		"printf 'built' > dist/index.html\n"
	if err := os.WriteFile(filepath.Join(project, "build.js"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	// The runner invokes `node build.js`; the fake node prints its own output.
	site := buildSite(project, "dist")
	site.Build.Command = "node build.js"

	result, err := svc.Build(context.Background(), site)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.Success || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	wantOutput := filepath.Join(project, "dist")
	if result.OutputPath != wantOutput {
		t.Fatalf("output path = %q, want %q", result.OutputPath, wantOutput)
	}
	if result.DurationMS < 0 {
		t.Fatalf("duration = %d", result.DurationMS)
	}
	if !strings.HasPrefix(result.LogPath, filepath.Join(svc.logsRoot(), "app")) {
		t.Fatalf("log path = %q", result.LogPath)
	}
	logData, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"node-stdout", "node-stderr", "node-env-production"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("log missing %q:\n%s", want, logData)
		}
	}
	logText := string(logData)
	if !strings.Contains(logText, "node-path-"+filepath.Join(svc.deps.Paths.Runtimes, "node", "22.11.0-linux-x64", "bin")) {
		t.Fatalf("build PATH does not start with the selected Node bin:\n%s", logText)
	}
	if _, err := os.Stat(filepath.Join(project, "dist", "index.html")); err != nil {
		t.Fatal(err)
	}

	logs, err := svc.Logs(context.Background(), site, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.Content, "node-stderr") || strings.Contains(logs.Content, "node-stdout") {
		t.Fatalf("tail = %q", logs.Content)
	}
}

func TestBuildRejectsMissingBuildProfileAndKind(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	if _, err := svc.Build(context.Background(), model.Site{ID: "s", Name: "app", Kind: model.SiteStatic, HostPath: project}); !errors.Is(err, ErrInvalidSetup) {
		t.Fatalf("site without build profile error = %v", err)
	}
	site := buildSite(project, "dist")
	site.Kind = model.SitePHP
	if _, err := svc.Build(context.Background(), site); !errors.Is(err, ErrInvalidSetup) {
		t.Fatalf("PHP site error = %v", err)
	}
}

func TestBuildRequiresInstalledNode(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	site := buildSite(project, "dist")
	site.Build.NodeVersion = "24"
	if _, err := svc.Build(context.Background(), site); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildRejectsOutputOutsideProject(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	for _, output := range []string{"../outside", "/tmp/outside", ""} {
		t.Run(output, func(t *testing.T) {
			if _, err := svc.Build(context.Background(), buildSite(project, output)); !errors.Is(err, ErrInvalidSetup) {
				t.Fatalf("output %q error = %v", output, err)
			}
		})
	}
}

func TestBuildFailureRestoresPreviousOutput(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	dist := filepath.Join(project, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Replace the fake node with one that writes partial output and fails.
	buildPath := filepath.Join(svc.deps.Paths.Runtimes, "node", "22.11.0-linux-x64", "bin", "node")
	script := "#!/bin/sh\n" +
		"rm -rf dist\n" +
		"mkdir -p dist\n" +
		"printf 'partial' > dist/new.txt\n" +
		"echo boom >&2\n" +
		"exit 7\n"
	if err := os.WriteFile(buildPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	site := buildSite(project, "dist")
	result, err := svc.Build(context.Background(), site)
	if err != nil {
		t.Fatalf("Build returned setup error: %v", err)
	}
	if result.Success {
		t.Fatalf("result = %+v", result)
	}
	if !result.Restored {
		t.Fatalf("previous output was not restored: %+v", result)
	}
	if result.Error == "" {
		t.Fatal("failure result has no error")
	}
	if _, err := os.Stat(filepath.Join(dist, "old.txt")); err != nil {
		t.Fatalf("old output missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dist, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial output remains: %v", err)
	}
}

func TestBuildMissingOutputRestoresAndFails(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	dist := filepath.Join(project, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildPath := filepath.Join(svc.deps.Paths.Runtimes, "node", "22.11.0-linux-x64", "bin", "node")
	if err := os.WriteFile(buildPath, []byte("#!/bin/sh\nrm -rf dist\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Build(context.Background(), buildSite(project, "dist"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !result.Restored {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Error, "output") {
		t.Fatalf("error = %q", result.Error)
	}
	if _, err := os.Stat(filepath.Join(dist, "old.txt")); err != nil {
		t.Fatalf("old output missing: %v", err)
	}
}

func TestBuildSnapshotSizeCapReportsNoRestore(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	dist := filepath.Join(project, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	sparse := filepath.Join(dist, "huge.bin")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(SnapshotSizeCap + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	buildPath := filepath.Join(svc.deps.Paths.Runtimes, "node", "22.11.0-linux-x64", "bin", "node")
	if err := os.WriteFile(buildPath, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Build(context.Background(), buildSite(project, "dist"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Restored {
		t.Fatalf("result = %+v", result)
	}
	if !result.SnapshotSkipped {
		t.Fatalf("SnapshotSkipped = false: %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Error), "restore") {
		t.Fatalf("error does not report restore limitation: %q", result.Error)
	}
}

func TestOutputExistsAndPathHelpers(t *testing.T) {
	project := t.TempDir()
	site := buildSite(project, "dist")
	if _, err := OutputPath(site); err != nil {
		t.Fatal(err)
	}
	exists, err := OutputExists(site)
	if err != nil || exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	if err := os.MkdirAll(filepath.Join(project, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	exists, err = OutputExists(site)
	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}

	if _, err := OutputPath(buildSite(project, "a/../../outside")); err == nil {
		t.Fatal("traversing output accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := OutputExists(buildSite(project, "link")); err == nil {
		t.Fatal("symlink output accepted")
	}
}

func TestBuildRefusesRoot(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	if err := os.WriteFile(filepath.Join(project, "build.js"), []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.deps.EUID = func() int { return 0 }
	if _, err := svc.Build(context.Background(), buildSite(project, "dist")); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root error = %v", err)
	}
}

func TestBuildSkippedSnapshotMarksSiteDegraded(t *testing.T) {
	svc, st, project, _ := newBuildFixture(t)
	dist := filepath.Join(project, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	sparse := filepath.Join(dist, "huge.bin")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(SnapshotSizeCap + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	buildPath := filepath.Join(svc.deps.Paths.Runtimes, "node", "22.11.0-linux-x64", "bin", "node")
	if err := os.WriteFile(buildPath, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	site := buildSite(project, "dist")
	site.Status = model.StatusRunning
	site.DesiredState = model.DesiredRunning
	if err := st.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Build(context.Background(), site)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !result.SnapshotSkipped {
		t.Fatalf("result = %+v", result)
	}
	stored, err := st.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.StatusDegraded {
		t.Fatalf("persisted status = %q, want degraded", stored.Status)
	}
}

func TestBuildEnvCannotReplaceNodePath(t *testing.T) {
	svc, _, project, _ := newBuildFixture(t)
	site := buildSite(project, "dist")
	site.Build.Env["PATH"] = "/untrusted/bin:/usr/bin:/bin"

	result, err := svc.Build(context.Background(), site)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	nodeBin := filepath.Join(svc.deps.Paths.Runtimes, "node", "22.11.0-linux-x64", "bin")
	line := ""
	for _, candidate := range strings.Split(logText, "\n") {
		if strings.HasPrefix(candidate, "node-path-") {
			line = strings.TrimPrefix(candidate, "node-path-")
		}
	}
	if !strings.HasPrefix(line, nodeBin) {
		t.Fatalf("PATH = %q, want selected Node bin first", line)
	}
	if !strings.Contains(line, "/untrusted/bin") {
		t.Fatalf("PATH = %q, build env PATH was dropped", line)
	}
}
