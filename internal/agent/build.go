package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// DetectNodePackageManager inspects lockfiles to identify the project package manager.
func DetectNodePackageManager(backendPath string) string {
	for _, entry := range []struct {
		file string
		pm   string
	}{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"package-lock.json", "npm"},
		{"package.json", "npm"},
	} {
		if _, err := os.Stat(filepath.Join(backendPath, entry.file)); err == nil {
			return entry.pm
		}
	}
	return ""
}

// DefaultBuildCommand returns the detected build script based on project lockfiles.
func DefaultBuildCommand(backendPath string) string {
	switch DetectNodePackageManager(backendPath) {
	case "pnpm":
		return "pnpm run build"
	case "yarn":
		return "yarn build"
	case "npm":
		return "npm run build"
	default:
		return ""
	}
}

// BuildSite executes a project build in WSL with the site's Node runtime.
func (r *Runtime) BuildSite(ctx context.Context, id string, req agentproto.BuildRequest) (agentproto.BuildResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.BuildResponse{}, err
	}
	site, err := r.site(id)
	if err != nil {
		return agentproto.BuildResponse{}, err
	}

	commandStr := strings.TrimSpace(req.Command)
	if commandStr == "" {
		commandStr = DefaultBuildCommand(site.spec.BackendPath)
	}
	if commandStr == "" {
		commandStr = strings.TrimSpace(site.spec.Command)
	}
	if commandStr == "" {
		return agentproto.BuildResponse{}, errors.New("agent: no build command configured or detected")
	}

	// Performance warning for Windows source mode (/mnt/c)
	isWindowsSource := strings.EqualFold(site.spec.SourceMode, "windows") || strings.HasPrefix(site.spec.BackendPath, "/mnt/")
	if isWindowsSource {
		warning := "warning: project resides on Windows filesystem (/mnt/); build performance may be degraded compared to native WSL ext4"
		r.logs.append(id, warning)
	}

	timeout := 5 * time.Minute
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd.exe", "/c", commandStr)
	} else {
		cmd = exec.CommandContext(cmdCtx, "/bin/sh", "-c", commandStr)
	}
	cmd.Dir = site.spec.BackendPath

	// Build environment with Node runtime prepended if available
	envMap := make(map[string]string)
	for k, v := range site.spec.Env {
		envMap[k] = v
	}
	for k, v := range req.Env {
		envMap[k] = v
	}
	caEnv, _ := r.Environment(envMap)

	if site.spec.RuntimeVersion != "" {
		nodeBin := filepath.Join(r.root, "runtimes", "node@"+site.spec.RuntimeVersion, "bin")
		if info, err := os.Stat(nodeBin); err == nil && info.IsDir() {
			existingPath := caEnv["PATH"]
			if existingPath == "" {
				existingPath = os.Getenv("PATH")
			}
			caEnv["PATH"] = nodeBin + ":" + existingPath
		}
	}

	envList := os.Environ()
	for k, v := range caEnv {
		envList = append(envList, k+"="+v)
	}
	cmd.Env = envList

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startErr := cmd.Run()
	exitCode := 0
	if startErr != nil {
		var exitErr *exec.ExitError
		if errors.As(startErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
			stderr.WriteString("\n" + startErr.Error())
		}
	}

	r.logs.append(id, fmt.Sprintf("build completed with exit code %d", exitCode))

	return agentproto.BuildResponse{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

// ExecSite runs an argv command in the site environment.
func (r *Runtime) ExecSite(ctx context.Context, id string, req agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SiteExecResponse{}, err
	}
	if len(req.Argv) == 0 {
		return agentproto.SiteExecResponse{}, errors.New("agent: exec requires a command")
	}
	site, err := r.site(id)
	if err != nil {
		return agentproto.SiteExecResponse{}, err
	}

	timeout := 2 * time.Minute
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, req.Argv[0], req.Argv[1:]...)
	cmd.Dir = site.spec.BackendPath

	envMap := make(map[string]string)
	for k, v := range site.spec.Env {
		envMap[k] = v
	}
	for k, v := range req.Env {
		envMap[k] = v
	}
	caEnv, _ := r.Environment(envMap)
	envList := os.Environ()
	for k, v := range caEnv {
		envList = append(envList, k+"="+v)
	}
	cmd.Env = envList

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startErr := cmd.Run()
	exitCode := 0
	if startErr != nil {
		var exitErr *exec.ExitError
		if errors.As(startErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
			stderr.WriteString("\n" + startErr.Error())
		}
	}

	return agentproto.SiteExecResponse{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}
