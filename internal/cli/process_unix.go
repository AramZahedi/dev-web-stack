//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// probeProcess checks process existence without delivering a signal.
func probeProcess(pid int) error {
	return syscall.Kill(pid, 0)
}

// processRunning treats a zombie as stopped; a detached daemon may remain a
// zombie until its new parent reaps it.
func processRunning(pid int) bool {
	if err := probeProcess(pid); err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	return !(end >= 0 && end+2 < len(text) && text[end+2] == 'Z')
}
