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

// nodeSmokeTimeout bounds the version probe. `node --version` is immediate; the
// timeout exists only so a broken binary cannot hang registration.
const nodeSmokeTimeout = 30 * time.Second

// nodeSmoke verifies an extracted Node build before it is published: bin/node
// runs and reports exactly expectedVersion, and a packaged npm must have a
// usable bin/npm entry. Node and npm are Linux ELF/script artifacts, so this
// check only runs on the host that will execute them.
func nodeSmoke(buildRoot, expectedVersion string) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), nodeSmokeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, nodePath, "--version")
	command.Dir = buildRoot
	output, err := command.CombinedOutput()
	message := limitNodeSmokeOutput(strings.TrimSpace(string(output)))
	if err != nil {
		if message != "" {
			return fmt.Errorf("runtime: node smoke: %s --version: %w: %s", nodePath, err, message)
		}
		return fmt.Errorf("runtime: node smoke: %s --version: %w", nodePath, err)
	}
	if message == "" {
		return fmt.Errorf("runtime: node smoke: %s --version printed nothing", nodePath)
	}
	reported := strings.TrimPrefix(message, "v")
	if reported != expectedVersion {
		return fmt.Errorf("runtime: node smoke: %s --version reported %q, want %q", nodePath, reported, expectedVersion)
	}

	return checkNodeNPM(buildRoot)
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
