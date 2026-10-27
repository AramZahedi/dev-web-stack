//go:build windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

var windowsFileSystemForPath = func(path string) (string, error) {
	volumeRoot := filepath.VolumeName(path) + `\`
	root, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return "", err
	}
	var fileSystem [256]uint16
	if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &fileSystem[0], uint32(len(fileSystem))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(fileSystem[:]), nil
}

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

func isNTFS(path string) (bool, error) {
	fileSystem, err := windowsFileSystemForPath(path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(fileSystem, "NTFS"), nil
}
