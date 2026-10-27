//go:build windows

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeServiceRunner struct {
	commands []recordedCommand
	state    int
	queryErr error
}

func (f *fakeServiceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.commands = append(f.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(args) >= 2 && args[0] == "query" {
		if f.queryErr != nil {
			return nil, f.queryErr
		}
		return []byte("SERVICE_NAME: DevstackHelper\r\n        STATE              : " + string(rune('0'+f.state)) + "  (state)\r\n"), nil
	}
	return nil, nil
}

func TestBuildWindowsServiceInstallCommandUsesLocalSystem(t *testing.T) {
	args, err := windowsServiceInstallArgs(`C:\Program Files\Devstack\devstack-helper.exe`, allowedHelperSID)
	if err != nil {
		t.Fatalf("install args: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"create", "binPath=", "start=", "auto", "obj=", "LocalSystem", "--allow-sid", allowedHelperSID} {
		if !strings.Contains(joined, want) {
			t.Errorf("install args %q do not contain %q", joined, want)
		}
	}
}

func TestWindowsServiceCommandDispatch(t *testing.T) {
	if !isWindowsServiceCommand([]string{"service", "install"}) {
		t.Fatal("service subcommand was not recognized")
	}
	if isWindowsServiceCommand([]string{"--allow-sid", allowedHelperSID}) {
		t.Fatal("normal helper flags were mistaken for a service subcommand")
	}
}

func TestInstallWindowsServiceDeniesNonAdmin(t *testing.T) {
	runner := &fakeServiceRunner{}
	if err := installWindowsService(context.Background(), runner, false, `C:\devstack\devstack-helper.exe`, allowedHelperSID); err == nil {
		t.Fatal("non-administrator service install succeeded")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("non-admin install ran commands: %+v", runner.commands)
	}
}

func TestUninstallWindowsServiceStopsThenDeletes(t *testing.T) {
	runner := &fakeServiceRunner{state: 4}
	if err := uninstallWindowsService(context.Background(), runner, true, windowsServiceName); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(runner.commands) != 3 || runner.commands[1].args[0] != "stop" || runner.commands[2].args[0] != "delete" {
		t.Fatalf("commands = %+v; want query, stop, delete", runner.commands)
	}
}

func TestUninstallWindowsServiceDoesNotRestopPendingService(t *testing.T) {
	runner := &fakeServiceRunner{state: 3}
	if err := uninstallWindowsService(context.Background(), runner, true, windowsServiceName); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(runner.commands) != 2 || runner.commands[1].args[0] != "delete" {
		t.Fatalf("commands = %+v; want query then delete", runner.commands)
	}
}

func TestWindowsServiceStartAndStopAreIdempotent(t *testing.T) {
	t.Run("start already running", func(t *testing.T) {
		runner := &fakeServiceRunner{state: 4}
		if err := startWindowsService(context.Background(), runner, windowsServiceName); err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 1 {
			t.Fatalf("commands = %+v; want query only", runner.commands)
		}
	})
	t.Run("start already pending", func(t *testing.T) {
		runner := &fakeServiceRunner{state: 2}
		if err := startWindowsService(context.Background(), runner, windowsServiceName); err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 1 {
			t.Fatalf("commands = %+v; want query only", runner.commands)
		}
	})
	t.Run("stop already stopped", func(t *testing.T) {
		runner := &fakeServiceRunner{state: 1}
		if err := stopWindowsService(context.Background(), runner, windowsServiceName); err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 1 {
			t.Fatalf("commands = %+v; want query only", runner.commands)
		}
	})
	t.Run("stop already pending", func(t *testing.T) {
		runner := &fakeServiceRunner{state: 3}
		if err := stopWindowsService(context.Background(), runner, windowsServiceName); err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 1 {
			t.Fatalf("commands = %+v; want query only", runner.commands)
		}
	})
	t.Run("start stopped service", func(t *testing.T) {
		runner := &fakeServiceRunner{state: 1}
		if err := startWindowsService(context.Background(), runner, windowsServiceName); err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 2 || runner.commands[1].args[0] != "start" {
			t.Fatalf("commands = %+v; want query then start", runner.commands)
		}
	})
	t.Run("query failure is not treated as stopped", func(t *testing.T) {
		runner := &fakeServiceRunner{queryErr: errors.New("access denied")}
		if err := stopWindowsService(context.Background(), runner, windowsServiceName); err == nil {
			t.Fatal("stop ignored query failure")
		}
		if len(runner.commands) != 1 {
			t.Fatalf("commands = %+v", runner.commands)
		}
	})
}
