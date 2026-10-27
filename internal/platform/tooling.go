package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Shim describes one global tool wrapper.
type Shim struct {
	Name    string
	Command string
}

// WriteShims writes Linux executable wrappers or Windows .cmd wrappers that
// run the command in the managed WSL distro.
func WriteShims(goos, dir string, shims []Shim) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("platform: create shim directory: %w", err)
	}
	for _, shim := range shims {
		if err := ValidateDevstackName(shim.Name); err != nil {
			return err
		}
		if strings.TrimSpace(shim.Command) == "" {
			return fmt.Errorf("platform: shim %q has no command", shim.Name)
		}
		if goos == "windows" {
			if err := writeShim(filepath.Join(dir, shim.Name+".cmd"), windowsShim(shim), 0o644); err != nil {
				return err
			}
			continue
		}
		if err := writeShim(filepath.Join(dir, shim.Name), unixShim(shim), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func windowsShim(shim Shim) string {
	return "@echo off\r\n" +
		"wsl.exe -d devstack -- bash -lc \"exec " + shim.Command + " \\\"$@\\\"\" -- %*\r\n"
}

func unixShim(shim Shim) string {
	return "#!/bin/sh\nexec " + shim.Command + " \"$@\"\n"
}

func writeShim(path, content string, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("platform: create shim %s: %w", path, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("platform: install shim %s: %w", path, err)
	}
	return nil
}
