package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidRecord is the on-disk record for one supervised process. StartTicks is
// field 22 of /proc/<pid>/stat, which lets a later daemon distinguish the
// original process from an unrelated one that reused its PID.
type pidRecord struct {
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

// CleanupOrphans kills process groups left behind by a previous daemon. It
// only acts on pid files whose recorded start time still matches the live
// process, then polls until the group is gone before deleting the file.
// Processes without a matching pid file are never touched.
func (m *Manager) CleanupOrphans() error {
	if m == nil {
		return errors.New("supervisor: nil manager")
	}
	if m.initErr != nil {
		return m.initErr
	}

	entries, err := os.ReadDir(m.runDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("supervisor: list run directory %s: %w", m.runDir, err)
	}

	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(m.runDir, entry.Name())
		id := strings.TrimSuffix(entry.Name(), ".json")

		rec, err := readPIDRecord(path)
		if err != nil {
			// Not one of ours (for example the daemon's own pid file). Leave
			// unrecognized files alone rather than deleting someone else's.
			m.logger.Debug("supervisor: ignoring unreadable pid file", "path", path, "error", err)
			continue
		}
		if rec.ID != id || rec.PID <= 0 || rec.StartTicks == 0 {
			m.logger.Debug("supervisor: ignoring unrecognized pid file", "path", path)
			continue
		}
		if rec.PID == os.Getpid() || rec.PID == syscall.Getpgrp() {
			m.logger.Warn("supervisor: refusing to signal our own process group", "id", rec.ID, "pid", rec.PID)
			continue
		}

		ticks, err := procStartTicks(rec.PID)
		switch {
		case errors.Is(err, os.ErrNotExist):
			// The process is already gone; only the file needs cleaning.
			errs = removePIDPath(errs, path)
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("supervisor: verify pid %d from %s: %w", rec.PID, path, err))
			continue
		case ticks != rec.StartTicks:
			// The PID now belongs to someone else. Never signal it.
			m.logger.Warn("supervisor: pid was reused; leaving it alone", "id", rec.ID, "pid", rec.PID)
			errs = removePIDPath(errs, path)
			continue
		}

		if err := cleanupGroup(rec.PID); err != nil {
			errs = append(errs, fmt.Errorf("supervisor: clean up %s: %w", path, err))
			continue
		}
		m.logger.Info("supervisor: killed orphan process group", "id", rec.ID, "pid", rec.PID)
		errs = removePIDPath(errs, path)
	}
	return errors.Join(errs...)
}

// cleanupGroup terminates an orphaned process group, escalating to SIGKILL
// when it does not exit promptly. It returns only after the group is gone or
// the bounded wait expires.
func cleanupGroup(pid int) error {
	alive, err := groupAlive(pid)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if waitGroupGone(pid, cleanupTermWait) {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if waitGroupGone(pid, cleanupKillWait) {
		return nil
	}
	return fmt.Errorf("process group %d did not exit", pid)
}

// ensureGroupGone SIGKILLs any members left in pid's group and waits for the
// group to disappear.
func (m *Manager) ensureGroupGone(pid int, timeout time.Duration) error {
	alive, err := groupAlive(pid)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if waitGroupGone(pid, timeout) {
		return nil
	}
	return fmt.Errorf("supervisor: process group %d is still alive", pid)
}

// groupAlive reports whether any process remains in the group. A nil error
// with alive=false means the group is gone.
func groupAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(-pid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return true, fmt.Errorf("supervisor: probe process group %d: %w", pid, err)
	}
}

// waitGroupGone polls until the process group disappears or timeout expires.
func waitGroupGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		alive, err := groupAlive(pid)
		if err == nil && !alive {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(groupPollInterval)
	}
}

// procStartTicks returns field 22 (starttime) from /proc/<pid>/stat. The comm
// field is parenthesized and may itself contain spaces or parentheses, so
// parsing starts after the last ')'.
func procStartTicks(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("supervisor: invalid pid %d", pid)
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return 0, fmt.Errorf("supervisor: malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(text[end+1:])
	// The suffix starts at field 3 (state); starttime is field 22.
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return 0, fmt.Errorf("supervisor: truncated /proc/%d/stat", pid)
	}
	ticks, err := strconv.ParseUint(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("supervisor: parse start time for pid %d: %w", pid, err)
	}
	return ticks, nil
}

func readPIDRecord(path string) (pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	var rec pidRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return pidRecord{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return rec, nil
}

func removePIDPath(errs []error, path string) []error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return append(errs, fmt.Errorf("supervisor: remove pid file %s: %w", path, err))
	}
	return errs
}
