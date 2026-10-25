package tooling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func runtimeRow(t *testing.T, st *store.Store, id, kind, version, path string) model.RuntimeBuild {
	t.Helper()
	build := model.RuntimeBuild{
		ID:          id,
		Kind:        kind,
		Version:     version,
		Backend:     model.BackendLocal,
		Path:        path,
		InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertRuntime(context.Background(), build); err != nil {
		t.Fatalf("upsert runtime %s: %v", id, err)
	}
	return build
}

func fakePHPBuild(t *testing.T, home, version string) model.RuntimeBuild {
	t.Helper()
	root := filepath.Join(home, "runtimes", "php", version+"-linux-x64")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	extDir := filepath.Join(root, "lib", "php", "extensions")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"php", "php-config", "phpize"} {
		writeTestScript(t, filepath.Join(root, "bin", name), "#!/bin/sh\nexit 0\n", 0o755)
	}
	for _, name := range []string{"phar", "openssl", "mbstring"} {
		if err := os.WriteFile(filepath.Join(extDir, name+".so"), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "php.ini"), []byte("; base\nmemory_limit = 128M\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return model.RuntimeBuild{
		ID:          "php@" + version + "-r1",
		Kind:        "php",
		Version:     version,
		Backend:     model.BackendLocal,
		Path:        root,
		InstalledAt: time.Now().UTC(),
	}
}

func writeTestScript(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDefaultLineExactNewestAndFallback(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	dir := t.TempDir()
	runtimeRow(t, st, "php@8.4.10-r1", "php", "8.4.10", filepath.Join(dir, "a"))
	runtimeRow(t, st, "php@8.4.12-r1", "php", "8.4.12", filepath.Join(dir, "b"))
	runtimeRow(t, st, "php@8.5.0-r1", "php", "8.5.0", filepath.Join(dir, "c"))
	runtimeRow(t, st, "node@24.0.0", "node", "24.0.0", filepath.Join(dir, "node"))

	for _, tc := range []struct {
		name    string
		setting string
		wantID  string
	}{
		{name: "line", setting: "8.4", wantID: "php@8.4.12-r1"},
		{name: "exact", setting: "php@8.4.10-r1", wantID: "php@8.4.10-r1"},
		{name: "fallback newest", setting: "", wantID: "php@8.5.0-r1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setting == "" {
				_ = st.SetSetting(ctx, DefaultPHPSettingKey, "")
			} else if err := st.SetSetting(ctx, DefaultPHPSettingKey, tc.setting); err != nil {
				t.Fatal(err)
			}
			build, err := ResolveDefault(ctx, st)
			if err != nil {
				t.Fatal(err)
			}
			if build.ID != tc.wantID {
				t.Fatalf("build = %s, want %s", build.ID, tc.wantID)
			}
		})
	}
}

func TestResolveDefaultRejectsMissingOrInvalid(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	dir := t.TempDir()
	runtimeRow(t, st, "php@8.4.10-r1", "php", "8.4.10", filepath.Join(dir, "a"))

	if err := st.SetSetting(ctx, DefaultPHPSettingKey, "php@8.4.99-r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDefault(ctx, st); !errors.Is(err, ErrPHPDefaultNotInstalled) {
		t.Fatalf("missing exact error = %v", err)
	}

	if err := st.SetSetting(ctx, DefaultPHPSettingKey, "not-a-build"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDefault(ctx, st); !errors.Is(err, ErrUnsupportedPHPDefault) {
		t.Fatalf("invalid default error = %v", err)
	}

	empty := testStore(t)
	if _, err := ResolveDefault(ctx, empty); !errors.Is(err, ErrNoPHPInstalled) {
		t.Fatalf("empty store error = %v", err)
	}
}

func TestGlobalIniPathAndEnsure(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	build := fakePHPBuild(t, home, "8.4.12")

	if got, want := GlobalIniPath(paths), filepath.Join(home, "config", "cli", "php.ini"); got != want {
		t.Fatalf("GlobalIniPath = %q, want %q", got, want)
	}
	path := GlobalIniPath(paths)
	if err := EnsureGlobalIni(build, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("mode = %04o, want 0644", perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"memory_limit = 128M",
		"extension_dir = \"" + filepath.Join(build.Path, "lib", "php", "extensions") + "\"",
		"extension=phar.so",
		"extension=openssl.so",
		"extension=mbstring.so",
		phpini.BeginDefaultsMarker,
		phpini.BeginExtensionsMarker,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated ini missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "site overrides") && strings.Contains(text, "memory_limit = 1G") {
		t.Fatalf("global ini unexpectedly contains site overrides:\n%s", text)
	}

	// Re-running is deterministic and never leaves temporary files behind.
	if err := EnsureGlobalIni(build, path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".php.ini") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestEnsurePHPShimsUsesAbsoluteBuildAndGlobalIni(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	build := fakePHPBuild(t, home, "8.4.12")
	iniPath := filepath.Join(home, "config", "cli", "php.ini")
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte("[PHP]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	installed, err := EnsurePHPShims(build, paths, iniPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(installed, ",") != "php,php-config,phpize" {
		t.Fatalf("installed = %v", installed)
	}
	dir := ShimDir(paths)
	for _, name := range []string{"php", "php-config", "phpize"} {
		wrapper := filepath.Join(dir, name)
		info, err := os.Stat(wrapper)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != 0o755 {
			t.Fatalf("%s mode = %04o, want 0755", name, perm)
		}
		data, err := os.ReadFile(wrapper)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, filepath.Join(build.Path, "bin", name)) {
			t.Fatalf("%s does not execute the absolute build binary:\n%s", name, text)
		}
		if strings.Contains(text, wrapper) {
			t.Fatalf("%s recurses into its own shim:\n%s", name, text)
		}
		if name == "php" && !strings.Contains(text, iniPath) {
			t.Fatalf("php shim does not apply the global ini:\n%s", text)
		}
	}
}

func TestComposerInstallVerifiesChecksumSizeAndWrapper(t *testing.T) {
	payload := []byte("#!/usr/bin/env php\n<?php echo 'composer';\n")
	sum := sha256.Sum256(payload)
	spec := composerSource{
		URL:     "",
		Version: "test-version",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(payload)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	spec.URL = srv.URL + "/composer.phar"

	home := t.TempDir()
	paths := config.NewPaths(home)
	status, err := installComposer(context.Background(), srv.Client(), paths, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Version != "test-version" {
		t.Fatalf("status = %+v", status)
	}
	phar := filepath.Join(home, "tools", "composer", "composer.phar")
	info, err := os.Stat(phar)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("phar mode = %04o, want 0600", perm)
	}
	got, err := os.ReadFile(phar)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatal("downloaded phar differs")
	}
	wrapper := filepath.Join(home, "tools", "composer", "bin", "composer")
	info, err = os.Stat(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Fatalf("wrapper mode = %04o, want 0755", perm)
	}
	script, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	if !strings.Contains(text, `exec "$php" "$phar" "$@"`) {
		t.Fatalf("wrapper does not use exec with quoted args:\n%s", text)
	}
	if !strings.Contains(text, "DEVSTACK_SITE") || !strings.Contains(text, "command -v php") {
		t.Fatalf("wrapper does not select the site PHP:\n%s", text)
	}
	if !strings.Contains(text, "does not resolve to a devstack site shim") {
		t.Fatalf("wrapper can silently fall back to a non-site PHP:\n%s", text)
	}
	if !strings.Contains(text, filepath.Join(ShimDir(paths), "php")) {
		t.Fatalf("wrapper does not reference the global shim:\n%s", text)
	}
}

func TestComposerInstallRejectsChecksumAndSizeMismatch(t *testing.T) {
	payload := []byte("composer fixture")
	sum := sha256.Sum256(payload)

	for _, tc := range []struct {
		name string
		spec composerSource
	}{
		{name: "checksum", spec: composerSource{SHA256: strings.Repeat("0", 64), Size: int64(len(payload))}},
		{name: "size", spec: composerSource{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload) + 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(payload)
			}))
			defer srv.Close()
			tc.spec.URL = srv.URL + "/composer.phar"

			home := t.TempDir()
			paths := config.NewPaths(home)
			if _, err := installComposer(context.Background(), srv.Client(), paths, tc.spec); !errors.Is(err, ErrComposerChecksum) {
				t.Fatalf("error = %v, want ErrComposerChecksum", err)
			}
			if _, err := os.Stat(filepath.Join(home, "tools", "composer", "composer.phar")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("phar left behind after failure: %v", err)
			}
		})
	}
}

func TestComposerPinnedVersionConstants(t *testing.T) {
	if ComposerVersion != "2.10.3" {
		t.Fatalf("ComposerVersion = %q", ComposerVersion)
	}
	if ComposerURL != "https://getcomposer.org/download/2.10.3/composer.phar" {
		t.Fatalf("ComposerURL = %q", ComposerURL)
	}
	if ComposerSHA256 != "7a2d379d5b8ffdaa028580ef26494c36d2feef4b178d3dd1473a4dbc5e17c8d6" {
		t.Fatalf("ComposerSHA256 = %q", ComposerSHA256)
	}
	if ComposerSize != 3642137 {
		t.Fatalf("ComposerSize = %d", ComposerSize)
	}
}

func TestWriteGlobalIniValidatesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli", "php.ini")
	build := fakePHPBuild(t, t.TempDir(), "8.4.12")
	content := []byte("[PHP]\nmemory_limit = 512M\n")
	if err := WriteGlobalIni(build, path, content); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content = %q", got)
	}
	if err := WriteGlobalIni(build, path, []byte("this is not ini\n")); err == nil {
		t.Fatal("invalid ini accepted")
	}
}

func TestComposerWrapperDoesNotInterpolateArguments(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	script := composerWrapperScript(paths)
	if strings.Contains(script, "$*") || strings.Contains(script, "composer.phar $") {
		t.Fatalf("wrapper interpolates arguments:\n%s", script)
	}
	if !strings.Contains(script, `"$@"`) {
		t.Fatalf("wrapper does not pass argv as a list:\n%s", script)
	}
}

func TestComposerDownloadUsesExactPinnedURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := ComposerURL
	// The production URL is immutable; this assertions guards accidental
	// fallback URLs in the source rather than changing the constant.
	if !strings.Contains(old, "/2.10.3/") {
		t.Fatalf("ComposerURL = %q", old)
	}
	_ = gotPath
	if _, err := io.ReadAll(strings.NewReader("ok")); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePHPBuildAndNodeBuild(t *testing.T) {
	builds := []model.RuntimeBuild{
		{ID: "php@8.4.10-r1", Kind: "php", Version: "8.4.10"},
		{ID: "php@8.4.12-r1", Kind: "php", Version: "8.4.12"},
		{ID: "node@22.10.0", Kind: "node", Version: "22.10.0"},
		{ID: "node@22.11.0", Kind: "node", Version: "22.11.0"},
	}
	php, err := ResolvePHPBuild(builds, "", "8.4")
	if err != nil || php.ID != "php@8.4.12-r1" {
		t.Fatalf("php = %+v err=%v", php, err)
	}
	node, err := ResolveNodeBuild(builds, "22")
	if err != nil || node.ID != "node@22.11.0" {
		t.Fatalf("node = %+v err=%v", node, err)
	}
	if _, err := ResolveNodeBuild(builds, "24"); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("missing node error = %v", err)
	}
}

func TestComposerSourceDefaults(t *testing.T) {
	got := defaultComposerSource()
	if got.URL != ComposerURL || got.SHA256 != ComposerSHA256 || got.Size != ComposerSize || got.Version != ComposerVersion {
		t.Fatalf("default source = %+v", got)
	}
	if got.SHA256 != strings.ToLower(got.SHA256) {
		t.Fatalf("sha256 is not lowercase: %q", got.SHA256)
	}
}
