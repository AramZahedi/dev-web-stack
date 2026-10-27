package tooling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
)

func TestComposerWrapperUsesSitePHPAndPreservesArgv(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "args")
	siteBin := SiteShimDir(paths, "app")
	fakePHP := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(out) + "\n"
	writeTestScript(t, filepath.Join(siteBin, "php"), fakePHP, 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "arg one", "--flag", "x y")
	command.Env = []string{
		"DEVSTACK_SITE=app",
		"DEVSTACK_HOME=" + home,
		"PATH=" + siteBin,
	}
	if err := command.Run(); err != nil {
		script, _ := os.ReadFile(filepath.Join(ComposerBinDir(paths), "composer"))
		t.Fatalf("run composer wrapper: %v\nscript:\n%s", err, script)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{ComposerPHARPath(paths), "arg one", "--flag", "x y"}
	if strings.Join(lines, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, want %q", lines, want)
	}
}

func TestComposerWrapperUsesGlobalShimOutsideSite(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "args")
	fakePHP := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(out) + "\n"
	writeTestScript(t, filepath.Join(ShimDir(paths), "php"), fakePHP, 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_HOME=" + home,
		"PATH=/usr/bin:/bin",
	}
	if err := command.Run(); err != nil {
		t.Fatalf("run composer wrapper: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{ComposerPHARPath(paths), "install"}
	if strings.Join(lines, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, want %q", lines, want)
	}
}

func TestComposerWrapperRefusesNonSitePHPWhenSiteSet(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	otherBin := filepath.Join(t.TempDir(), "bin")
	out := filepath.Join(t.TempDir(), "args")
	writeTestScript(t, filepath.Join(otherBin, "php"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n", 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_SITE=site-1",
		"DEVSTACK_HOME=" + home,
		"PATH=" + otherBin,
	}
	err := command.Run()
	if err == nil {
		t.Fatal("composer wrapper accepted a non-site PHP")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("non-site PHP was executed: %v", statErr)
	}
}

func TestComposerWrapperRefusesGlobalPHPForSiteShell(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "args")
	writeTestScript(t, filepath.Join(ShimDir(paths), "php"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n", 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_SITE=site-1",
		"DEVSTACK_HOME=" + home,
		"PATH=" + ShimDir(paths),
	}
	err := command.Run()
	if err == nil {
		t.Fatal("composer wrapper fell back to the global devstack PHP inside a site")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("global PHP was executed for a site shell: %v", statErr)
	}
}

func TestComposerWrapperRefusesWhenNodeSiteHasNoSitePHP(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	nodeBin := filepath.Join(home, "tools", "sites", "node-1", "bin")
	writeTestScript(t, filepath.Join(nodeBin, "node"), "#!/bin/sh\nexit 0\n", 0o755)
	out := filepath.Join(t.TempDir(), "args")
	writeTestScript(t, filepath.Join(ShimDir(paths), "php"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n", 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_SITE=node-1",
		"DEVSTACK_HOME=" + home,
		"PATH=" + nodeBin + ":" + ShimDir(paths),
	}
	err := command.Run()
	if err == nil {
		t.Fatal("composer wrapper used global PHP for a Node site shell")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("global PHP was executed for a Node site shell: %v", statErr)
	}
}

func TestComposerWrapperRefusesAnotherSitesShim(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	otherSite := filepath.Join(home, "tools", "sites", "other", "bin")
	out := filepath.Join(t.TempDir(), "args")
	writeTestScript(t, filepath.Join(otherSite, "php"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n", 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_SITE=site-1",
		"DEVSTACK_HOME=" + home,
		"PATH=" + otherSite,
	}
	if err := command.Run(); err == nil {
		t.Fatal("composer wrapper accepted another site's PHP shim")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("another site's PHP was executed: %v", statErr)
	}
}

func TestComposerWrapperRejectsInvalidSiteName(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "args")
	writeTestScript(t, filepath.Join(home, "tools", "sites", "evil", "bin", "php"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n", 0o755)

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_SITE=../evil",
		"DEVSTACK_HOME=" + home,
		"PATH=" + filepath.Join(home, "tools", "sites", "evil", "bin"),
	}
	if err := command.Run(); err == nil {
		t.Fatal("composer wrapper accepted an invalid site name")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("PHP was executed for an invalid site name: %v", statErr)
	}
}

func TestComposerWrapperRejectsSymlinkedSitePHPOutsideSite(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	payload := []byte("phar")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := installComposer(context.Background(), srv.Client(), paths, composerSource{
		URL: srv.URL, Version: "test", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "args")
	outside := filepath.Join(t.TempDir(), "outside-php")
	writeTestScript(t, outside, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n", 0o755)
	siteBin := SiteShimDir(paths, "app")
	if err := os.MkdirAll(siteBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(siteBin, "php")); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("executing shell script wrappers is not supported on Windows")
	}

	command := exec.Command(filepath.Join(ComposerBinDir(paths), "composer"), "install")
	command.Env = []string{
		"DEVSTACK_SITE=app",
		"DEVSTACK_HOME=" + home,
		"PATH=" + siteBin,
	}
	if err := command.Run(); err == nil {
		t.Fatal("composer wrapper accepted a symlink escaping the site shim")
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("escaping PHP was executed: %v", statErr)
	}
}
