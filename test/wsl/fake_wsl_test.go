package wsl

import (
	"context"
	"strings"
	"testing"
)

// fakeWSLRunner models only the fixed wsl.exe commands used by provisioning;
// it intentionally does not invoke a host WSL installation.
type fakeWSLRunner struct {
	distros []string
	calls   [][]string
}

func (f *fakeWSLRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	switch strings.Join(args, " ") {
	case "--version":
		return []byte("WSL version: 2.0.0.0\n"), nil
	case "--list --quiet":
		return []byte(strings.Join(f.distros, "\n")), nil
	}
	if len(args) > 0 && args[0] == "--import" {
		f.distros = append(f.distros, args[1])
	}
	if len(args) > 0 && args[0] == "--unregister" {
		name := args[1]
		for i, distro := range f.distros {
			if distro == name {
				f.distros = append(f.distros[:i], f.distros[i+1:]...)
				break
			}
		}
	}
	return []byte("ok\n"), nil
}

func TestFakeWSLRunnerKeepsProvisioningTestsOffHostWSL(t *testing.T) {
	runner := &fakeWSLRunner{}
	if _, err := runner.Run(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), "--import", "devstack", `C:\devstack\wsl\devstack`, "rootfs.tar.gz", "--version", "2"); err != nil {
		t.Fatal(err)
	}
	if len(runner.distros) != 1 || runner.distros[0] != "devstack" {
		t.Fatalf("fake distros = %#v", runner.distros)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("fake WSL calls = %#v", runner.calls)
	}
}
