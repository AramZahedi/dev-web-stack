package tooling

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// wslNodeArgTranslate mirrors the argument translation used by the global PHP
// and Composer shims. It is duplicated here so Node shims can be added without
// touching the shared Windows tooling file during the Node toolchain rollout.
const wslNodeArgTranslate = `args=(); for a in \"$@\"; do a_norm=\"${a//\\//}\"; if [[ \"$a_norm\" =~ ^([a-zA-Z]):/(.*) ]]; then drive=\"${BASH_REMATCH[1],,}\"; rest=\"${BASH_REMATCH[2]}\"; args+=(\"/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=([a-zA-Z]):/(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; drive=\"${BASH_REMATCH[2],,}\"; rest=\"${BASH_REMATCH[3]}\"; args+=(\"$opt=/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then rest=\"${BASH_REMATCH[2]}\"; if [[ -z \"$rest\" ]]; then args+=(\"/\"); else args+=(\"/$rest\"); fi; elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; rest=\"${BASH_REMATCH[3]}\"; if [[ -z \"$rest\" ]]; then args+=(\"$opt=/\"); else args+=(\"$opt=/$rest\"); fi; elif [[ \"$a_norm\" =~ ^/[uU]sers/(.*) ]]; then rest=\"${BASH_REMATCH[1]}\"; args+=(\"/mnt/c/Users/$rest\"); else args+=(\"$a\"); fi; done;`

// WindowsNodeShimDir returns the public Windows command shim directory for the
// global Node default, matching the php.cmd and composer.cmd location.
func WindowsNodeShimDir(paths config.Paths) string {
	return WindowsShimDir(paths)
}

// EnsureWindowsNodeShims writes node.cmd, npm.cmd, and npx.cmd wrappers for
// the exact installed Node build. Each wrapper invokes the managed WSL distro
// and the absolute build binary; no native Windows Node is used.
func EnsureWindowsNodeShims(binDir, distro string, build model.RuntimeBuild) error {
	if !wslDistroPattern.MatchString(strings.TrimSpace(distro)) {
		return fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	if strings.TrimSpace(binDir) == "" {
		return errors.New("tooling: Windows Node shim directory is empty")
	}
	if build.Kind != "" && build.Kind != "node" {
		return fmt.Errorf("tooling: build %q is not a Node build", build.ID)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("tooling: create Windows Node shim directory: %w", err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		binary, err := nodeToolBinary(build, name)
		if err != nil {
			return err
		}
		path := filepath.Join(binDir, name+".cmd")
		content := fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s --exec bash -c \"%s exec %s \\\"${args[@]}\\\"\" _ %%*\r\n", distro, wslNodeArgTranslate, posixQuote(binary))
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
			return fmt.Errorf("tooling: write %s: %w", temporary, err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: replace %s: %w", path, err)
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: install %s: %w", path, err)
		}
	}
	return nil
}
