package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCapabilitiesFor(t *testing.T) {
	linux := CapabilitiesFor("linux", CapabilityProbe{Systemd: true, DockerSocket: true})
	if !linux.HasSystemd || !linux.HasDockerSocket || linux.HasNamedPipe || linux.RequiresWSLAgent {
		t.Fatalf("linux = %+v", linux)
	}
	windows := CapabilitiesFor("windows", CapabilityProbe{WSL: true})
	if windows.HasSystemd || !windows.HasNamedPipe || !windows.HasWSL || !windows.RequiresWSLAgent || windows.SupportsHostTuning {
		t.Fatalf("windows = %+v", windows)
	}
	if windows.SupportsConPTY == false && runtime.GOOS == "windows" {
		t.Fatalf("windows ConPTY = %+v", windows)
	}
}

func TestConfigureProcess(t *testing.T) {
	cmd := NewCommand(context.Background(), "echo", "ok")
	if cmd.Path == "" || len(cmd.Args) != 2 {
		t.Fatalf("command = %+v", cmd)
	}
	ConfigureProcess(cmd)
}

func TestNewTerminal(t *testing.T) {
	if _, err := NewTerminal("windows"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("windows terminal = %v, want ErrUnsupported", err)
	}
	if _, err := NewTerminal("linux"); err != nil {
		t.Fatalf("linux terminal = %v", err)
	}
}

func TestNewResourceCollector(t *testing.T) {
	if _, err := NewResourceCollector("windows"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("windows collector = %v", err)
	}
	if _, err := NewResourceCollector("linux"); err != nil {
		t.Fatalf("linux collector = %v", err)
	}
}

func TestNewDoctor(t *testing.T) {
	if _, err := NewDoctor("linux"); err != nil {
		t.Fatalf("linux doctor = %v", err)
	}
	if _, err := NewDoctor("windows"); err != nil {
		t.Fatalf("windows doctor = %v", err)
	}
}

func TestNewBuilder(t *testing.T) {
	if _, err := NewBuilder("windows", nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("windows builder = %v, want ErrUnsupported", err)
	}
	if _, err := NewBuilder("linux", nil); err != nil {
		t.Fatalf("linux builder = %v", err)
	}
}

func TestWriteShims(t *testing.T) {
	dir := t.TempDir()
	shims := []Shim{{Name: "php", Command: "/data/devstack/runtimes/php/bin/php"}, {Name: "composer", Command: "composer"}}
	if err := WriteShims("windows", dir, shims); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "php.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "wsl.exe") || !strings.Contains(string(data), "/data/devstack/runtimes/php/bin/php") {
		t.Fatalf("cmd shim = %q", data)
	}
	linuxDir := t.TempDir()
	if err := WriteShims("linux", linuxDir, shims); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(linuxDir, "php"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("linux shim is not executable: %o", info.Mode().Perm())
	}
}
