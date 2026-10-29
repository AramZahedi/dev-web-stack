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

const (
	// maxCapturedOutput bounds each captured stream. A build or exec that
	// prints without limit must not be able to exhaust the root agent's memory;
	// the response marks the stream as truncated instead.
	maxCapturedOutput = 8 << 20
	// maxCommandTimeout bounds a client-supplied timeout. The API default is
	// far lower; this only prevents an effectively unbounded process lifetime.
	maxCommandTimeout = time.Hour
)

// cappedBuffer keeps at most limit bytes and records whether more arrived.
// Write always reports the full length so the child never blocks or fails on a
// truncation.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit <= 0 {
		limit = maxCapturedOutput
	}
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() >= b.limit {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	if b == nil {
		return ""
	}
	if b.truncated {
		return b.buf.String() + "\n[devstack: output truncated]\n"
	}
	return b.buf.String()
}

func (b *cappedBuffer) WriteString(value string) {
	_, _ = b.Write([]byte(value))
}

// commandTimeout resolves a request timeout against the hard ceiling.
func commandTimeout(requestedMS int, fallback time.Duration) time.Duration {
	timeout := fallback
	if requestedMS > 0 {
		timeout = time.Duration(requestedMS) * time.Millisecond
	}
	if timeout > maxCommandTimeout {
		timeout = maxCommandTimeout
	}
	return timeout
}

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

	timeout := commandTimeout(req.TimeoutMS, 5*time.Minute)
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd.exe", "/c", commandStr)
	} else {
		cmd = exec.CommandContext(cmdCtx, "/bin/sh", "-c", commandStr)
	}
	cmd.Dir = site.spec.BackendPath
	// Kill the whole process tree on timeout: npm/pnpm/vite spawn children that
	// would otherwise keep running after the shell is killed.
	configureBuildCmd(cmd)

	// Build environment with Node runtime prepended if available
	envMap := make(map[string]string)
	for k, v := range site.spec.Env {
		envMap[k] = v
	}
	for k, v := range req.Env {
		envMap[k] = v
	}
	caEnv, err := r.Environment(envMap)
	if err != nil {
		return agentproto.BuildResponse{}, err
	}

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

	stdout := newCappedBuffer(maxCapturedOutput)
	stderr := newCappedBuffer(maxCapturedOutput)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

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

	timeout := commandTimeout(req.TimeoutMS, 2*time.Minute)
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, req.Argv[0], req.Argv[1:]...)
	cmd.Dir = site.spec.BackendPath
	configureBuildCmd(cmd)

	envMap := make(map[string]string)
	for k, v := range site.spec.Env {
		envMap[k] = v
	}
	for k, v := range req.Env {
		envMap[k] = v
	}
	caEnv, err := r.Environment(envMap)
	if err != nil {
		return agentproto.SiteExecResponse{}, err
	}
	envList := os.Environ()
	for k, v := range caEnv {
		envList = append(envList, k+"="+v)
	}
	cmd.Env = envList

	stdout := newCappedBuffer(maxCapturedOutput)
	stderr := newCappedBuffer(maxCapturedOutput)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

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
