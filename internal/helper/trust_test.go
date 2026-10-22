package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type runnerCall struct {
	name string
	args []string
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []runnerCall
	fn    func(name string, args []string) ([]byte, error)
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	fn := r.fn
	r.mu.Unlock()
	if fn != nil {
		return fn(name, args)
	}
	return nil, nil
}

func (r *fakeRunner) called(name string) bool {
	return r.count(name) > 0
}

func (r *fakeRunner) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int
	for _, c := range r.calls {
		if c.name == name {
			n++
		}
	}
	return n
}

func (r *fakeRunner) invocations(name string) []runnerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runnerCall
	for _, c := range r.calls {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

func anchorFilePath(anchorDir string) string {
	return filepath.Join(anchorDir, "devstack-root.crt")
}

func findTarget(targets []Target, name string) (Target, bool) {
	for _, target := range targets {
		if target.Name == name {
			return target, true
		}
	}
	return Target{}, false
}

func findTargetPrefix(targets []Target, prefix string) (Target, bool) {
	for _, target := range targets {
		if strings.HasPrefix(target.Name, prefix) {
			return target, true
		}
	}
	return Target{}, false
}

func installFakeCertutil(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "certutil")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

func TestTrustInstallWritesAnchorAndCallsUpdate(t *testing.T) {
	// Force a deterministic "no certutil" result so this test exercises only
	// the OS anchor store.
	t.Setenv("PATH", t.TempDir())
	runner := &fakeRunner{}
	authority := newTestAuthority(t)
	home := t.TempDir()
	anchorDir := t.TempDir()
	tm := NewTrustManager(authority.RootPEM(), home, runner, anchorDir)

	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("Install returned no targets")
	}

	path := anchorFilePath(anchorDir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	if !bytes.Equal(data, authority.RootPEM()) {
		t.Fatalf("anchor content = %q, want root PEM", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("anchor mode = %v, want 0644", info.Mode().Perm())
	}
	if !runner.called("update-ca-certificates") {
		t.Fatalf("update command not called; calls = %v", runner.calls)
	}
	if runner.called("update-ca-trust") {
		t.Fatal("Fedora update command called for an injected Debian-style anchor dir")
	}

	system, ok := findTarget(targets, "OS")
	if !ok {
		t.Fatalf("System target missing from %+v", targets)
	}
	if !system.OK {
		t.Fatalf("System target = %+v", system)
	}
	chrome, ok := findTarget(targets, "Chrome")
	if !ok {
		t.Fatalf("Chrome target missing from %+v", targets)
	}
	if !chrome.OK || chrome.Detail != "not installed" {
		t.Fatalf("Chrome target = %+v, want OK not installed", chrome)
	}
	firefox, ok := findTargetPrefix(targets, "Firefox")
	if !ok {
		t.Fatalf("Firefox target missing from %+v", targets)
	}
	if !firefox.OK || firefox.Detail != "not installed" {
		t.Fatalf("Firefox target = %+v, want OK not installed", firefox)
	}
}

func TestTrustInstallReportsUpdateFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runner := &fakeRunner{fn: func(name string, _ []string) ([]byte, error) {
		if name == "update-ca-certificates" {
			return nil, errors.New("boom")
		}
		return nil, nil
	}}
	authority := newTestAuthority(t)
	tm := NewTrustManager(authority.RootPEM(), t.TempDir(), runner, t.TempDir())

	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	system, ok := findTarget(targets, "OS")
	if !ok || system.OK {
		t.Fatalf("System target = %+v (found %v), want failure", system, ok)
	}
	if !strings.Contains(system.Detail, "boom") {
		t.Fatalf("System detail = %q, want update error", system.Detail)
	}
}

func TestTrustInstallIsIdempotent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runner := &fakeRunner{}
	authority := newTestAuthority(t)
	anchorDir := t.TempDir()
	tm := NewTrustManager(authority.RootPEM(), t.TempDir(), runner, anchorDir)

	for i := 0; i < 2; i++ {
		if _, err := tm.Install(context.Background()); err != nil {
			t.Fatalf("Install %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(anchorFilePath(anchorDir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, authority.RootPEM()) {
		t.Fatal("anchor content changed")
	}
	if got := runner.count("update-ca-certificates"); got != 2 {
		t.Fatalf("update command count = %d, want 2", got)
	}
}

func TestTrustRemoveOnlyDeletesMatchingAnchor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runner := &fakeRunner{}
	authority := newTestAuthority(t)
	anchorDir := t.TempDir()
	tm := NewTrustManager(authority.RootPEM(), t.TempDir(), runner, anchorDir)

	if _, err := tm.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Remove(context.Background()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(anchorFilePath(anchorDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchor still present after Remove: %v", err)
	}
	if got := runner.count("update-ca-certificates"); got != 2 {
		t.Fatalf("update command count = %d, want 2", got)
	}

	// A certificate with a different fingerprint must be left untouched.
	other := newTestAuthority(t)
	path := anchorFilePath(anchorDir)
	if err := os.WriteFile(path, other.RootPEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	targets, err := tm.Remove(context.Background())
	if err != nil {
		t.Fatalf("Remove foreign anchor: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign anchor was removed: %v", err)
	}
	system, ok := findTarget(targets, "OS")
	if !ok || system.OK {
		t.Fatalf("System target = %+v (found %v), want failure for a foreign anchor", system, ok)
	}
	if got := runner.count("update-ca-certificates"); got != 2 {
		t.Fatalf("update command ran for a foreign anchor; count = %d", got)
	}

	// Invalid content is equally protected.
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Remove(context.Background()); err != nil {
		t.Fatalf("Remove invalid anchor: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("invalid anchor was removed: %v", err)
	}
}

func TestTrustStatusAfterInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runner := &fakeRunner{}
	authority := newTestAuthority(t)
	anchorDir := t.TempDir()
	tm := NewTrustManager(authority.RootPEM(), t.TempDir(), runner, anchorDir)

	targets, err := tm.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	system, ok := findTarget(targets, "OS")
	if !ok || system.OK {
		t.Fatalf("System status before install = %+v (found %v)", system, ok)
	}

	if _, err := tm.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	targets, err = tm.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	system, ok = findTarget(targets, "OS")
	if !ok || !system.OK {
		t.Fatalf("System status after install = %+v (found %v)", system, ok)
	}

	if _, err := tm.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	targets, err = tm.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	system, ok = findTarget(targets, "OS")
	if !ok || system.OK {
		t.Fatalf("System status after remove = %+v (found %v)", system, ok)
	}
}

func TestTrustStatusDetectsForeignAnchor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	authority := newTestAuthority(t)
	other := newTestAuthority(t)
	anchorDir := t.TempDir()
	path := anchorFilePath(anchorDir)
	if err := os.WriteFile(path, other.RootPEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	tm := NewTrustManager(authority.RootPEM(), t.TempDir(), &fakeRunner{}, anchorDir)
	targets, err := tm.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	system, ok := findTarget(targets, "OS")
	if !ok || system.OK {
		t.Fatalf("System status with foreign anchor = %+v (found %v)", system, ok)
	}
}

func TestTrustRejectsInvalidRoot(t *testing.T) {
	tm := NewTrustManager([]byte("not a certificate"), t.TempDir(), &fakeRunner{}, t.TempDir())
	if _, err := tm.Install(context.Background()); err == nil {
		t.Fatal("Install accepted an invalid root certificate")
	}
}

func TestTrustAnchorCommandsAndDetection(t *testing.T) {
	name, args := anchorUpdateCommand(true)
	if name != "update-ca-trust" || len(args) != 1 || args[0] != "extract" {
		t.Fatalf("Fedora update command = %s %v", name, args)
	}
	name, args = anchorUpdateCommand(false)
	if name != "update-ca-certificates" || len(args) != 0 {
		t.Fatalf("Debian update command = %s %v", name, args)
	}

	dir, fedora, ok := detectAnchorDir(fedoraAnchorsDir)
	if !ok || !fedora || dir != fedoraAnchorsDir {
		t.Fatalf("detectAnchorDir(fedora) = %q, %v, %v", dir, fedora, ok)
	}
	temp := t.TempDir()
	dir, fedora, ok = detectAnchorDir(temp)
	if !ok || fedora || dir != temp {
		t.Fatalf("detectAnchorDir(temp) = %q, %v, %v", dir, fedora, ok)
	}
}

type fakeNSSRunner struct {
	mu    sync.Mutex
	dbs   map[string]map[string]bool
	calls []runnerCall
}

func newFakeNSSRunner() *fakeNSSRunner {
	return &fakeNSSRunner{dbs: make(map[string]map[string]bool)}
}

func (r *fakeNSSRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	if name != "certutil" {
		return nil, fmt.Errorf("unexpected command %q", name)
	}

	var db, nick, op string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-d":
			if i+1 < len(args) {
				db = strings.TrimPrefix(args[i+1], "sql:")
				i++
			}
		case "-n":
			if i+1 < len(args) {
				nick = args[i+1]
				i++
			}
		case "-N":
			op = "N"
		case "-A":
			op = "A"
		case "-D":
			op = "D"
		case "-L":
			op = "L"
		}
	}
	if db == "" {
		return nil, fmt.Errorf("certutil invocation without -d: %v", args)
	}
	switch op {
	case "N":
		if _, ok := r.dbs[db]; !ok {
			r.dbs[db] = make(map[string]bool)
		}
		return nil, nil
	case "A":
		if nick == "" {
			return nil, errors.New("certutil -A without -n")
		}
		if _, ok := r.dbs[db]; !ok {
			r.dbs[db] = make(map[string]bool)
		}
		r.dbs[db][nick] = true
		return nil, nil
	case "D":
		if !r.dbs[db][nick] {
			return nil, errors.New("nickname not found")
		}
		delete(r.dbs[db], nick)
		return nil, nil
	case "L":
		if !r.dbs[db][nick] {
			return nil, errors.New("nickname not found")
		}
		return []byte(nick), nil
	default:
		return nil, fmt.Errorf("unsupported certutil invocation: %v", args)
	}
}

func (r *fakeNSSRunner) hasNick(db, nick string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dbs[db][nick]
}

func (r *fakeNSSRunner) invocations(name string) []runnerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runnerCall
	for _, call := range r.calls {
		if call.name == name {
			out = append(out, call)
		}
	}
	return out
}

func TestTrustNSSChromeAndFirefoxProfiles(t *testing.T) {
	installFakeCertutil(t)
	home := t.TempDir()
	base := filepath.Join(home, ".mozilla", "firefox")
	relativeProfile := filepath.Join(base, "rel.default")
	absoluteProfile := filepath.Join(home, "absolute.profile")
	for _, dir := range []string{relativeProfile, absoluteProfile} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ini := strings.Join([]string{
		"[General]",
		"StartWithLastProfile=1",
		"",
		"[Profile0]",
		"Name=relative",
		"IsRelative=1",
		"Path=rel.default",
		"Default=1",
		"",
		"[Profile1]",
		"Name=absolute",
		"IsRelative=0",
		"Path=" + absoluteProfile,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(base, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := newFakeNSSRunner()
	authority := newTestAuthority(t)
	tm := NewTrustManager(authority.RootPEM(), home, runner, t.TempDir())

	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	chrome, ok := findTarget(targets, "Chrome")
	if !ok || !chrome.OK {
		t.Fatalf("Chrome target = %+v (found %v)", chrome, ok)
	}
	for _, name := range []string{"relative", "absolute"} {
		target, ok := findTarget(targets, "Firefox ("+name+")")
		if !ok || !target.OK {
			t.Fatalf("Firefox (%s) target = %+v (found %v)", name, target, ok)
		}
	}

	chromeDB := filepath.Join(home, ".pki", "nssdb")
	for _, db := range []string{chromeDB, relativeProfile, absoluteProfile} {
		if !runner.hasNick(db, "devstack local CA") {
			t.Fatalf("NSS nickname missing from %s; dbs = %v", db, runner.dbs)
		}
	}
	if got := len(runner.invocations("certutil")); got == 0 {
		t.Fatal("certutil was not invoked")
	}

	targets, err = tm.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if chrome, ok := findTarget(targets, "Chrome"); !ok || !chrome.OK {
		t.Fatalf("Chrome status = %+v (found %v)", chrome, ok)
	}
	if firefox, ok := findTargetPrefix(targets, "Firefox"); !ok || !firefox.OK {
		t.Fatalf("Firefox status = %+v (found %v)", firefox, ok)
	}

	targets, err = tm.Remove(context.Background())
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if chrome, ok := findTarget(targets, "Chrome"); !ok || !chrome.OK {
		t.Fatalf("Chrome remove = %+v (found %v)", chrome, ok)
	}
	for _, db := range []string{chromeDB, relativeProfile, absoluteProfile} {
		if runner.hasNick(db, "devstack local CA") {
			t.Fatalf("NSS nickname still present in %s", db)
		}
	}

	targets, err = tm.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after remove: %v", err)
	}
	if chrome, ok := findTarget(targets, "Chrome"); !ok || chrome.OK {
		t.Fatalf("Chrome status after remove = %+v (found %v), want failure", chrome, ok)
	}
}

func TestTrustFirefoxNotInstalled(t *testing.T) {
	installFakeCertutil(t)
	tm := NewTrustManager(newTestAuthority(t).RootPEM(), t.TempDir(), newFakeNSSRunner(), t.TempDir())
	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firefox, ok := findTargetPrefix(targets, "Firefox")
	if !ok || !firefox.OK || firefox.Detail != "not installed" {
		t.Fatalf("Firefox target = %+v (found %v)", firefox, ok)
	}
}

func TestTrustFirefoxRejectsProfilesOutsideHome(t *testing.T) {
	installFakeCertutil(t)
	home := t.TempDir()
	outside := t.TempDir()
	base := filepath.Join(home, ".mozilla", "firefox")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	ini := "[Profile0]\nName=outside\nIsRelative=0\nPath=" + outside + "\n"
	if err := os.WriteFile(filepath.Join(base, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := newFakeNSSRunner()
	tm := NewTrustManager(newTestAuthority(t).RootPEM(), home, runner, t.TempDir())
	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target, ok := findTarget(targets, "Firefox (outside)")
	if !ok || target.OK {
		t.Fatalf("Firefox target = %+v (found %v), want failure", target, ok)
	}
	if runner.hasNick(outside, "devstack local CA") {
		t.Fatal("certutil was pointed at a profile outside the user's home")
	}
}

func TestTrustFirefoxSkipsMissingProfile(t *testing.T) {
	installFakeCertutil(t)
	home := t.TempDir()
	base := filepath.Join(home, ".mozilla", "firefox")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	ini := "[Profile0]\nName=missing\nIsRelative=1\nPath=missing.default\n"
	if err := os.WriteFile(filepath.Join(base, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := newFakeNSSRunner()
	tm := NewTrustManager(newTestAuthority(t).RootPEM(), home, runner, t.TempDir())
	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target, ok := findTarget(targets, "Firefox (missing)")
	if !ok || !target.OK || target.Detail != "not installed" {
		t.Fatalf("Firefox target = %+v (found %v)", target, ok)
	}
	if _, err := os.Stat(filepath.Join(base, "missing.default")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing profile directory was created: %v", err)
	}
}

func TestTrustChromeRejectsNSSDirOutsideHome(t *testing.T) {
	installFakeCertutil(t)
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".pki"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".pki", "nssdb")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	runner := newFakeNSSRunner()
	tm := NewTrustManager(newTestAuthority(t).RootPEM(), home, runner, t.TempDir())
	targets, err := tm.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	chrome, ok := findTarget(targets, "Chrome")
	if !ok || chrome.OK {
		t.Fatalf("Chrome target = %+v (found %v), want failure", chrome, ok)
	}
	if runner.hasNick(outside, "devstack local CA") {
		t.Fatal("certutil was pointed at an NSS database outside the user's home")
	}
}

func TestTrustRemoveMissingAnchor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runner := &fakeRunner{}
	tm := NewTrustManager(newTestAuthority(t).RootPEM(), t.TempDir(), runner, t.TempDir())
	targets, err := tm.Remove(context.Background())
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	system, ok := findTarget(targets, "OS")
	if !ok || !system.OK || system.Detail != "not installed" {
		t.Fatalf("System target = %+v (found %v)", system, ok)
	}
	if runner.called("update-ca-certificates") {
		t.Fatal("update command ran when no anchor was present")
	}
}

func TestAuthorizeHomeResolvesAllowUID(t *testing.T) {
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil || account.HomeDir == "" {
		t.Skipf("current user has no resolvable home: user=%+v err=%v", account, err)
	}
	s, err := NewServer(Config{
		SocketPath: filepath.Join(t.TempDir(), "helper.sock"),
		AllowUID:   os.Getuid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.authorizeHome(account.HomeDir)
	if err != nil {
		t.Fatalf("authorizeHome(%q): %v", account.HomeDir, err)
	}
	if got != filepath.Clean(account.HomeDir) {
		t.Fatalf("authorizeHome = %q, want %q", got, filepath.Clean(account.HomeDir))
	}
	if _, err := s.authorizeHome(filepath.Join(account.HomeDir, "other")); !errors.Is(err, helperproto.ErrUnauthorized) {
		t.Fatalf("foreign home error = %v, want ErrUnauthorized", err)
	}
}
