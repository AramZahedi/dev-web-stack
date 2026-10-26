package platform

import (
	"context"
	"errors"
	"os/exec"
)

// ErrUnsupported is returned when a seam has no implementation on the current
// platform yet.
var ErrUnsupported = errors.New("platform: operation is not supported on this platform")

// NewCommand builds an exec.Cmd with the context.
func NewCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return exec.CommandContext(ctx, name, args...)
}

// Terminal is the platform terminal session seam. Linux owns a local PTY;
// Windows proxies to the WSL agent.
type Terminal interface {
	Close() error
}

type terminalStub struct{}

func (terminalStub) Close() error { return nil }

// NewTerminal returns the platform terminal implementation.
func NewTerminal(goos string) (Terminal, error) {
	if goos == "windows" {
		return nil, ErrUnsupported
	}
	return terminalStub{}, nil
}

// ResourceCollector collects host performance data.
type ResourceCollector interface {
	Collect(context.Context) (any, error)
}

// NewResourceCollector returns the platform collector.
func NewResourceCollector(goos string) (ResourceCollector, error) {
	if goos == "windows" {
		return nil, ErrUnsupported
	}
	return linuxCollector{}, nil
}

// DoctorCheck is one platform doctor result.
type DoctorCheck struct {
	ID      string
	Status  string
	Message string
}

// Doctor runs platform-specific doctor checks.
type Doctor interface {
	Checks(context.Context) []DoctorCheck
}

type platformDoctor struct{ goos string }

func (d platformDoctor) Checks(context.Context) []DoctorCheck {
	if d.goos == "windows" {
		return []DoctorCheck{
			{ID: "wsl", Status: "warn", Message: "WSL agent checks run after provisioning"},
		}
	}
	return nil
}

// NewDoctor returns the platform doctor.
func NewDoctor(goos string) (Doctor, error) {
	return platformDoctor{goos: goos}, nil
}

// BuildResult is the outcome of a site build.
type BuildResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Builder runs a site build command.
type Builder interface {
	Build(ctx context.Context, dir, command string, env map[string]string) (BuildResult, error)
}

type localBuilder struct{}

func (localBuilder) Build(ctx context.Context, dir, command string, env map[string]string) (BuildResult, error) {
	return runShell(ctx, dir, command, env)
}

// NewBuilder returns the platform build executor.
func NewBuilder(goos string, _ any) (Builder, error) {
	if goos == "windows" {
		return nil, ErrUnsupported
	}
	return localBuilder{}, nil
}
