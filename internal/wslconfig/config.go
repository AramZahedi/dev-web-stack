// Package wslconfig merges the managed .wslconfig keys without replacing
// unrelated user configuration.
package wslconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Values is the managed [wsl2] resource set.
type Values struct {
	MemoryMB   int
	Processors int
	SwapMB     int
}

// Apply writes the managed [wsl2] keys and returns the backup path. The caller
// must restart WSL for the new VM resources to take effect.
func Apply(path, backupPath string, values Values) (string, bool, error) {
	if strings.TrimSpace(path) == "" {
		return "", false, errors.New("wslconfig: path is required")
	}
	if values.MemoryMB < 2048 {
		return "", false, fmt.Errorf("wslconfig: memory must be at least 2048 MB")
	}
	if values.Processors < 1 || values.Processors > 256 {
		return "", false, fmt.Errorf("wslconfig: processors must be 1-256")
	}
	if values.SwapMB < 0 {
		return "", false, fmt.Errorf("wslconfig: swap must not be negative")
	}
	data, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", false, readErr
	}
	original := string(data)
	if exists && strings.TrimSpace(backupPath) != "" {
		if _, statErr := os.Stat(backupPath); errors.Is(statErr, os.ErrNotExist) {
			if err := writeAtomic(backupPath, []byte(original), 0o600); err != nil {
				return "", false, fmt.Errorf("wslconfig: backup: %w", err)
			}
		} else if statErr != nil {
			return "", false, statErr
		}
	}
	merged := merge(original, map[string]string{
		"memory":     strconv.Itoa(values.MemoryMB) + "MB",
		"processors": strconv.Itoa(values.Processors),
		"swap":       strconv.Itoa(values.SwapMB) + "MB",
	})
	if err := writeAtomic(path, []byte(merged), 0o600); err != nil {
		return "", false, err
	}
	return backupPath, true, nil
}

// Reset restores the backup when present. Without a backup it removes the
// managed keys and leaves unrelated configuration intact.
func Reset(path, backupPath string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("wslconfig: path is required")
	}
	if data, err := os.ReadFile(backupPath); err == nil {
		return writeAtomic(path, data, 0o600)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	next := removeManagedKeys(string(data))
	if strings.TrimSpace(next) == "" {
		return os.Remove(path)
	}
	return writeAtomic(path, []byte(next), 0o600)
}

func merge(original string, managed map[string]string) string {
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	var out []string
	inWSL := false
	seen := map[string]bool{}
	flush := func() {
		for key, value := range managed {
			if !seen[key] {
				out = append(out, key+"="+value)
			}
		}
		seen = map[string]bool{}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inWSL {
				flush()
			}
			inWSL = strings.EqualFold(trimmed, "[wsl2]")
			out = append(out, line)
			continue
		}
		if inWSL && trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, ";") {
			key := strings.ToLower(strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0]))
			if value, ok := managed[key]; ok {
				if !seen[key] {
					out = append(out, key+"="+value)
					seen[key] = true
				}
				continue
			}
		}
		out = append(out, line)
	}
	if inWSL {
		flush()
	}
	if !inWSL {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "[wsl2]")
		for key, value := range managed {
			out = append(out, key+"="+value)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

func removeManagedKeys(original string) string {
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	var out []string
	inWSL := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inWSL = strings.EqualFold(trimmed, "[wsl2]")
			out = append(out, line)
			continue
		}
		if inWSL && trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, ";") {
			key := strings.ToLower(strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0]))
			switch key {
			case "memory", "processors", "swap":
				continue
			}
		}
		out = append(out, line)
	}
	result := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if strings.EqualFold(strings.TrimSpace(result), "[wsl2]") {
		return ""
	}
	return result + "\n"
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".devstack-wslconfig-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
