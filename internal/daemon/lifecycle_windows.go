//go:build windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func writeWindowsReady(path string, pid int) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "daemon-ready-*.tmp")
	if err != nil {
		return fmt.Errorf("daemon: create readiness marker: %w", err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("daemon: secure readiness marker: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(pid) + "\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("daemon: write readiness marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("daemon: sync readiness marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("daemon: close readiness marker: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("daemon: publish readiness marker: %w", err)
	}
	return nil
}

func readyWindows(home string) bool {
	data, err := os.ReadFile(ReadyPath(home))
	return err == nil && len(data) > 0
}
