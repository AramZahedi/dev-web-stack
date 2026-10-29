//go:build !windows

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// nodeSmokeTimeout bounds one version probe. `node --version` is immediate;
	// the timeout exists only so a broken binary cannot hang registration.
	nodeSmokeTimeout = 30 * time.Second
	// nodeSmokeWaitDelay bounds how long Wait may block on inherited pipes
	// after the probe is killed or interrupted.
	nodeSmokeWaitDelay = 2 * time.Second
)

// nodeSmokeContext is the context-aware smoke used by installEntry. It is a
// package variable so tests can stop an install exactly at the registration
// boundary; production always points at runNodeSmokeContext.
var nodeSmokeContext = runNodeSmokeContext

// nodeSmoke is the signature-mandated wrapper for callers without a context.
func nodeSmoke(buildRoot, expectedVersion string) error {
	return nodeSmokeContext(context.Background(), buildRoot, expectedVersion)
}

// runNodeSmokeContext verifies an extracted Node build before it is published:
// bin/node runs and reports exactly expectedVersion on stdout, and a packaged
// npm must have a usable bin/npm entry. stderr is diagnostic only and never
// participates in the version comparison.
func runNodeSmokeContext(ctx context.Context, buildRoot, expectedVersion string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(buildRoot) == "" {
		return errors.New("runtime: node smoke: build root is required")
	}
	if strings.TrimSpace(expectedVersion) == "" {
		return errors.New("runtime: node smoke: expected version is required")
	}

	nodePath := filepath.Join(buildRoot, "bin", "node")
	info, err := os.Stat(nodePath)
	if err != nil {
		return fmt.Errorf("runtime: node smoke: %s: %w", nodePath, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("runtime: node smoke: %s is not executable", nodePath)
	}

	runCtx, cancel := context.WithTimeout(ctx, nodeSmokeTimeout)
	defer cancel()
	command := exec.CommandContext(runCtx, nodePath, "--version")
	command.Dir = buildRoot
	command.Env = nodeSmokeEnv()
	command.WaitDelay = nodeSmokeWaitDelay
	stdout, err := command.Output()
	if err != nil {
		// Cancellation wins over the process error so callers can match
		// context.Canceled/context.DeadlineExceeded reliably.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("runtime: node smoke: %s --version timed out after %s", nodePath, nodeSmokeTimeout)
		}
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = limitNodeSmokeOutput(strings.TrimSpace(string(exitErr.Stderr)))
		}
		if stderr != "" {
			return fmt.Errorf("runtime: node smoke: %s --version: %w: %s", nodePath, err, stderr)
		}
		return fmt.Errorf("runtime: node smoke: %s --version: %w", nodePath, err)
	}
	message := strings.TrimSpace(string(stdout))
	if message == "" {
		return fmt.Errorf("runtime: node smoke: %s --version printed nothing", nodePath)
	}
	reported := strings.TrimPrefix(message, "v")
	if reported != expectedVersion {
		return fmt.Errorf("runtime: node smoke: %s --version reported %q, want %q", nodePath, reported, expectedVersion)
	}

	return checkNodeNPM(buildRoot)
}

// nodeSmokeEnv removes host Node configuration that could change how the
// extracted binary starts, such as preload modules or extra module paths.
func nodeSmokeEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "NODE_OPTIONS=") || strings.HasPrefix(entry, "NODE_PATH=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// checkNodeNPM requires bin/npm to resolve when the extracted tree packages
// npm. Official Node tarballs ship both lib/node_modules/npm and a bin/npm
// link; minimal builds without npm are accepted. A present shim must point at
// an executable regular file so a dangling link is never published.
func checkNodeNPM(buildRoot string) error {
	npmPath := filepath.Join(buildRoot, "bin", "npm")
	info, err := os.Lstat(npmPath)
	if errors.Is(err, fs.ErrNotExist) {
		if _, statErr := os.Stat(filepath.Join(buildRoot, "lib", "node_modules", "npm")); statErr == nil {
			return fmt.Errorf("runtime: node smoke: packaged npm has no %s", npmPath)
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("runtime: node smoke: inspect packaged npm: %w", statErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("runtime: node smoke: inspect %s: %w", npmPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("runtime: node smoke: %s is a directory", npmPath)
	}
	target, err := os.Stat(npmPath)
	if err != nil {
		return fmt.Errorf("runtime: node smoke: %s: %w", npmPath, err)
	}
	if !target.Mode().IsRegular() || target.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("runtime: node smoke: %s is not executable", npmPath)
	}
	return nil
}

// nodeSmokeOutputLimit caps how much of a failing binary's output is echoed
// back. `node --version` prints one short line; anything larger is suspect.
const nodeSmokeOutputLimit = 4 << 10

func limitNodeSmokeOutput(output string) string {
	if len(output) <= nodeSmokeOutputLimit {
		return output
	}
	return output[:nodeSmokeOutputLimit]
}
