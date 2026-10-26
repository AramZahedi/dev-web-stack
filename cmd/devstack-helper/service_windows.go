//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"golang.org/x/sys/windows"
)

const windowsServiceName = "DevstackHelper"

type windowsCommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execWindowsCommandRunner struct{}

func (execWindowsCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func isWindowsServiceCommand(args []string) bool {
	return len(args) > 0 && args[0] == "service"
}

func windowsServiceInstallArgs(executable, allowSID string) ([]string, error) {
	if executable == "" {
		return nil, errors.New("service executable path is empty")
	}
	if _, err := helper.NewServer(helper.Config{AllowSID: allowSID}); err != nil {
		return nil, err
	}
	command := windows.EscapeArg(executable) + " service run --allow-sid " + windows.EscapeArg(allowSID)
	return []string{"create", windowsServiceName, "binPath=", command, "start=", "auto", "obj=", "LocalSystem", "DisplayName=", "Devstack Helper"}, nil
}

func installWindowsService(ctx context.Context, runner windowsCommandRunner, isAdmin bool, executable, allowSID string) error {
	if !isAdmin {
		return errors.New("installing the Windows helper service requires an elevated administrator")
	}
	args, err := windowsServiceInstallArgs(executable, allowSID)
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "sc.exe", args...); err != nil {
		return fmt.Errorf("create Windows helper service: %w", err)
	}
	return nil
}

func uninstallWindowsService(ctx context.Context, runner windowsCommandRunner, isAdmin bool, name string) error {
	if !isAdmin {
		return errors.New("uninstalling the Windows helper service requires an elevated administrator")
	}
	state, output, err := queryWindowsService(ctx, runner, name)
	if err != nil && !windowsServiceMissing(output) {
		return err
	}
	if err == nil && state != 1 {
		if _, err := runner.Run(ctx, "sc.exe", "stop", name); err != nil {
			return fmt.Errorf("stop Windows helper service: %w", err)
		}
	}
	if err != nil {
		return nil
	}
	if _, err := runner.Run(ctx, "sc.exe", "delete", name); err != nil {
		return fmt.Errorf("delete Windows helper service: %w", err)
	}
	return nil
}

func startWindowsService(ctx context.Context, runner windowsCommandRunner, name string) error {
	state, _, err := queryWindowsService(ctx, runner, name)
	if err != nil {
		return err
	}
	if state == 4 {
		return nil
	}
	if _, err := runner.Run(ctx, "sc.exe", "start", name); err != nil {
		return fmt.Errorf("start Windows helper service: %w", err)
	}
	return nil
}

func stopWindowsService(ctx context.Context, runner windowsCommandRunner, name string) error {
	state, _, err := queryWindowsService(ctx, runner, name)
	if err != nil {
		return err
	}
	if state == 1 {
		return nil
	}
	if _, err := runner.Run(ctx, "sc.exe", "stop", name); err != nil {
		return fmt.Errorf("stop Windows helper service: %w", err)
	}
	return nil
}

func queryWindowsService(ctx context.Context, runner windowsCommandRunner, name string) (int, []byte, error) {
	output, err := runner.Run(ctx, "sc.exe", "query", name)
	if err != nil {
		return 0, output, fmt.Errorf("query Windows helper service: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(strings.ToUpper(line), "STATE") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			break
		}
		var state int
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &state); scanErr == nil && state >= 1 && state <= 7 {
			return state, output, nil
		}
	}
	return 0, output, errors.New("query Windows helper service: response has no valid STATE")
}

func windowsServiceMissing(output []byte) bool {
	return strings.Contains(string(output), "1060")
}

func currentProcessIsAdmin() (bool, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false, fmt.Errorf("open current Windows token: %w", err)
	}
	defer token.Close()
	return token.IsElevated(), nil
}

func serviceCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("service command requires install, uninstall, start, stop, or run")
	}
	ctx := context.Background()
	runner := execWindowsCommandRunner{}
	switch args[0] {
	case "install":
		opts, err := parseWindowsOptions(args[1:])
		if err != nil {
			return err
		}
		admin, err := currentProcessIsAdmin()
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve helper executable: %w", err)
		}
		return installWindowsService(ctx, runner, admin, exe, opts.allowSID)
	case "uninstall":
		admin, err := currentProcessIsAdmin()
		if err != nil {
			return err
		}
		return uninstallWindowsService(ctx, runner, admin, windowsServiceName)
	case "start", "stop":
		admin, err := currentProcessIsAdmin()
		if err != nil {
			return err
		}
		if !admin {
			return errors.New("controlling the Windows helper service requires an elevated administrator")
		}
		if args[0] == "start" {
			return startWindowsService(ctx, runner, windowsServiceName)
		}
		return stopWindowsService(ctx, runner, windowsServiceName)
	case "run":
		opts, err := parseWindowsOptions(args[1:])
		if err != nil {
			return err
		}
		return helper.RunWindowsService(windowsServiceName, helper.Config{AllowSID: opts.allowSID, PipeName: opts.pipe})
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}
