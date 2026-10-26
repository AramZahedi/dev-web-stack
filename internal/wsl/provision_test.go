package wsl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type provisionRunner struct {
	distros  []string
	version  string
	marker   string
	initProc string
	probe    string
	err      error
	calls    [][]string
}

func (f *provisionRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.err != nil {
		return nil, f.err
	}
	switch {
	case strings.Join(args, " ") == "--version":
		return []byte(f.version), nil
	case strings.Join(args, " ") == "--list --quiet":
		return []byte(strings.Join(f.distros, "\n")), nil
	case len(args) >= 5 && args[0] == "-d" && args[2] == "--" && args[3] == "cat":
		switch args[4] {
		case "/data/devstack/managed.json":
			return []byte(f.marker), nil
		case "/proc/1/comm":
			return []byte(f.initProc), nil
		}
	case strings.Contains(strings.Join(args, " "), initializeManagedDistroScript):
		f.marker = `{"managed_by":"devstack","install_id":"` + args[len(args)-1] + `"}`
		return []byte("ok\n"), nil
	case len(args) >= 7 && args[0] == "-d" && args[2] == "--" && args[3] == "bash":
		return []byte("ok\n"), nil
	case len(args) >= 5 && args[0] == "-d" && args[2] == "--" && args[3] == "bash":
		return []byte(f.probe), nil
	}
	return []byte("ok\n"), nil
}

func testProvisioner(t *testing.T, runner Runner) *Provisioner {
	t.Helper()
	rootfs := filepath.Join(t.TempDir(), "debian-rootfs.tar.gz")
	data := []byte("pinned Debian rootfs fixture")
	if err := os.WriteFile(rootfs, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return &Provisioner{
		Runner: runner,
		Config: ProvisionConfig{
			DistroName:       "devstack",
			InstallDir:       filepath.Join(t.TempDir(), "wsl", "devstack"),
			RootFSPath:       rootfs,
			RootFSSHA256:     hex.EncodeToString(sum[:]),
			MinimumDiskBytes: 1,
		},
		FreeDiskBytes: func(string) (uint64, error) { return 1 << 30, nil },
	}
}

func TestProvisionRequiresWSL2SystemdAndLocalhostForwarding(t *testing.T) {
	runner := &provisionRunner{version: "WSL version: 2.0.0.0\n", probe: "ok\n"}
	p := testProvisioner(t, runner)
	if err := p.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	runner.version = "WSL version: 1.2.5\n"
	if err := p.Preflight(context.Background()); err == nil {
		t.Fatal("Preflight accepted WSL older than 2.0.0")
	}
	runner.version = "WSL version: 2.0.0.0\n"
	if err := p.CheckLocalhostForwarding(context.Background(), 8080); err != nil {
		t.Fatalf("CheckLocalhostForwarding: %v", err)
	}
	runner.err = errors.New("connection refused")
	if err := p.CheckLocalhostForwarding(context.Background(), 8080); !errors.Is(err, ErrLocalhostForwarding) {
		t.Fatalf("CheckLocalhostForwarding error = %v, want ErrLocalhostForwarding", err)
	}
}

func TestProvisionImportsStartsAndReusesOnlyMarkedDistro(t *testing.T) {
	runner := &provisionRunner{version: "WSL version: 2.1.5\n", initProc: "systemd\n"}
	p := testProvisioner(t, runner)
	p.Config.InstallID = "01234567-89ab-cdef-0123-456789abcdef"
	managed, err := p.Provision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if managed.Name != "devstack" || managed.InstallID == "" || !managed.Imported {
		t.Fatalf("managed = %+v", managed)
	}
	if !hasCall(runner.calls, "--import devstack") || !hasCall(runner.calls, "-d devstack --exec /bin/true") {
		t.Fatalf("calls missing import/start: %#v", runner.calls)
	}

	runner.distros = []string{"devstack"}
	runner.marker = `{"managed_by":"devstack","install_id":"11111111-2222-3333-4444-555555555555"}`
	runner.initProc = "systemd\n"
	runner.probe = "ok\n"
	runner.calls = nil
	managed, err = p.Provision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if managed.InstallID != "11111111-2222-3333-4444-555555555555" || managed.Imported {
		t.Fatalf("reused distro = %+v", managed)
	}
	if hasCall(runner.calls, "--import devstack") {
		t.Fatalf("reused managed distro was imported again: %#v", runner.calls)
	}
}

func TestProvisionRefusesUnmarkedDistro(t *testing.T) {
	runner := &provisionRunner{version: "WSL version: 2.0.0\n", distros: []string{"devstack"}}
	p := testProvisioner(t, runner)
	if _, err := p.Provision(context.Background()); !errors.Is(err, ErrUnmanagedDistro) {
		t.Fatalf("Provision error = %v, want ErrUnmanagedDistro", err)
	}
	if hasCall(runner.calls, "--import devstack") || hasCall(runner.calls, "--unregister devstack") {
		t.Fatalf("unmarked distro was mutated: %#v", runner.calls)
	}
}

func TestProvisionRequiresConfirmationBeforeUnregister(t *testing.T) {
	runner := &provisionRunner{version: "WSL version: 2.0.0\n", distros: []string{"devstack"}, marker: `{"managed_by":"devstack","install_id":"11111111-2222-3333-4444-555555555555"}`, initProc: "systemd\n", probe: "ok\n"}
	p := testProvisioner(t, runner)
	if err := p.Unregister(context.Background(), false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("Unregister(false) = %v, want confirmation error", err)
	}
	if hasCall(runner.calls, "--unregister devstack") {
		t.Fatal("unconfirmed unregister reached wsl.exe")
	}
	if err := p.Unregister(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !hasCall(runner.calls, "--unregister devstack") {
		t.Fatalf("confirmed unregister missing: %#v", runner.calls)
	}
}

func TestProvisionRejectsRootfsChecksumOrDiskSpace(t *testing.T) {
	runner := &provisionRunner{version: "WSL version: 2.0.0\n"}
	p := testProvisioner(t, runner)
	p.Config.RootFSSHA256 = strings.Repeat("0", 64)
	if _, err := p.Provision(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("checksum error = %v", err)
	}
	p = testProvisioner(t, runner)
	p.FreeDiskBytes = func(string) (uint64, error) { return 0, nil }
	if _, err := p.Provision(context.Background()); err == nil || !strings.Contains(err.Error(), "disk space") {
		t.Fatalf("disk space error = %v", err)
	}
}

func hasCall(calls [][]string, fragment string) bool {
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), fragment) {
			return true
		}
	}
	return false
}
