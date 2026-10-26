//go:build !windows

package supervisor

import (
	"bytes"
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
	// Token is injected into the child environment and inherited by its
	// descendants. It lets orphan cleanup identify a surviving group after the
	// original leader has exited, without trusting a reused PGID.
	Token string `json:"token,omitempty"`
}

const supervisorTokenEnv = "DEVSTACK_SUPERVISOR_TOKEN"

// CleanupOrphans kills process groups left behind by a previous daemon. It
// only acts on pid files whose recorded start time still matches the live
// process, then polls until the group is gone before deleting the file.
//
// A process group can outlive its leader. If the recorded PID is gone or has
// been reused but its group still exists, the start time can no longer be
// verified; the pid file is kept (and an error reported) rather than risking a
// signal to an unrelated group. Processes without a matching pid file are
// never touched.
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
		if rec.PID <= 1 {
			// kill(-1) signals every process we can reach, not "group 1".
			m.logger.Warn("supervisor: refusing to signal reserved pid", "id", rec.ID, "pid", rec.PID)
			errs = append(errs, fmt.Errorf("supervisor: pid %d in %s is not signalable; left pid file in place", rec.PID, path))
			continue
		}
		if rec.PID == os.Getpid() || rec.PID == syscall.Getpgrp() {
			m.logger.Warn("supervisor: refusing to signal our own process group", "id", rec.ID, "pid", rec.PID)
			continue
		}

		ticks, err := procStartTicks(rec.PID)
		switch {
		case errors.Is(err, os.ErrNotExist):
			// The leader is gone, but its group may still contain a server.
			alive, probeErr := groupAlive(rec.PID)
			if probeErr != nil {
				errs = append(errs, fmt.Errorf("supervisor: probe group %d from %s: %w", rec.PID, path, probeErr))
				continue
			}
			if alive {
				if rec.Token != "" && groupMembersHaveToken(rec.PID, rec.Token) {
					if err := cleanupVerifiedGroup(rec.PID); err != nil {
						errs = append(errs, fmt.Errorf("supervisor: clean up leaderless group %d from %s: %w", rec.PID, path, err))
						continue
					}
					m.logger.Info("supervisor: killed verified leaderless process group", "id", rec.ID, "pid", rec.PID)
					errs = removePIDPath(errs, path)
					continue
				}
				m.logger.Warn("supervisor: process group outlived its leader; leaving pid file", "id", rec.ID, "pid", rec.PID)
				errs = append(errs, fmt.Errorf("supervisor: process group %d outlived its leader; left %s in place", rec.PID, path))
				continue
			}
			errs = removePIDPath(errs, path)
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("supervisor: verify pid %d from %s: %w", rec.PID, path, err))
			continue
		case ticks != rec.StartTicks:
			// The PID now belongs to someone else. Only remove the file when
			// no group with that ID remains; otherwise a surviving descendant
			// may still be holding the port.
			alive, probeErr := groupAlive(rec.PID)
			if probeErr != nil {
				errs = append(errs, fmt.Errorf("supervisor: probe group %d from %s: %w", rec.PID, path, probeErr))
				continue
			}
			if alive {
				if rec.Token != "" && groupMembersHaveToken(rec.PID, rec.Token) {
					if err := cleanupVerifiedGroup(rec.PID); err != nil {
						errs = append(errs, fmt.Errorf("supervisor: clean up reused-pid group %d from %s: %w", rec.PID, path, err))
						continue
					}
					m.logger.Info("supervisor: killed verified reused-pid process group", "id", rec.ID, "pid", rec.PID)
					errs = removePIDPath(errs, path)
					continue
				}
				m.logger.Warn("supervisor: pid was reused but its group is alive; leaving pid file", "id", rec.ID, "pid", rec.PID)
				errs = append(errs, fmt.Errorf("supervisor: pid %d was reused and process group %d is still alive; left %s in place", rec.PID, rec.PID, path))
				continue
			}
			m.logger.Warn("supervisor: pid was reused; removing stale pid file", "id", rec.ID, "pid", rec.PID)
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
// the bounded wait expires. The group leader guard runs before any signal so a
// malformed pid file can never turn into kill(-1) or a signal to an unrelated
// process group.
// groupMembersHaveToken reports whether every live member of pgid carries the
// per-process token in its environment. A recycled group cannot satisfy this
// unless it inherited a token from one of our children.
func groupMembersHaveToken(pgid int, token string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	found := false
	needle := []byte(supervisorTokenEnv + "=" + token + "\x00")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		pgrp, err := procPgrp(pid)
		if err != nil || pgrp != pgid {
			continue
		}
		if state, err := procState(pid); err == nil && state == "Z" {
			// Zombies have no readable environment; they will be reaped after
			// the live members are terminated.
			continue
		}
		found = true
		env, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil || !bytes.Contains(env, needle) {
			return false
		}
	}
	return found
}

// cleanupVerifiedGroup terminates a group whose members have already been
// verified by token. It is used when the original leader is gone, so the
// leader-only safety check in cleanupGroup cannot run.
func cleanupVerifiedGroup(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("supervisor: refusing to signal reserved pid %d", pid)
	}
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

func cleanupGroup(pid int) error {
	if err := signalableGroupLeader(pid); err != nil {
		return err
	}
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

// signalableGroupLeader rejects PIDs that cannot safely be used with
// kill(-pid): reserved PIDs and processes that are not their own group leader.
func signalableGroupLeader(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("supervisor: refusing to signal reserved pid %d", pid)
	}
	pgrp, err := procPgrp(pid)
	if err != nil {
		return fmt.Errorf("supervisor: inspect process group for pid %d: %w", pid, err)
	}
	if pgrp != pid {
		return fmt.Errorf("supervisor: pid %d is in process group %d, not a group leader", pid, pgrp)
	}
	return nil
}

// ensureGroupGone SIGKILLs any members left in pid's group and waits for the
// group to disappear.
func (m *Manager) ensureGroupGone(pid int, timeout time.Duration) error {
	if pid <= 1 {
		return fmt.Errorf("supervisor: refusing to signal reserved pid %d", pid)
	}
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

// procStatFields returns the fields of /proc/<pid>/stat after the comm field.
// The comm field is parenthesized and may itself contain spaces or
// parentheses, so parsing starts after the last ')'.
func procStatFields(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("supervisor: invalid pid %d", pid)
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, err
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return nil, fmt.Errorf("supervisor: malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) == 0 {
		return nil, fmt.Errorf("supervisor: truncated /proc/%d/stat", pid)
	}
	return fields, nil
}

// procStartTicks returns field 22 (starttime) from /proc/<pid>/stat.
func procStartTicks(pid int) (uint64, error) {
	fields, err := procStatFields(pid)
	if err != nil {
		return 0, err
	}
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

// procState returns field 3 (state) from /proc/<pid>/stat.
func procState(pid int) (string, error) {
	fields, err := procStatFields(pid)
	if err != nil {
		return "", err
	}
	if len(fields) == 0 {
		return "", fmt.Errorf("supervisor: truncated /proc/%d/stat", pid)
	}
	return fields[0], nil
}

// procPgrp returns field 5 (pgrp) from /proc/<pid>/stat.
func procPgrp(pid int) (int, error) {
	fields, err := procStatFields(pid)
	if err != nil {
		return 0, err
	}
	// The suffix starts at field 3 (state); pgrp is field 5.
	const pgrpIndex = 5 - 3
	if len(fields) <= pgrpIndex {
		return 0, fmt.Errorf("supervisor: truncated /proc/%d/stat", pid)
	}
	pgrp, err := strconv.Atoi(fields[pgrpIndex])
	if err != nil {
		return 0, fmt.Errorf("supervisor: parse process group for pid %d: %w", pid, err)
	}
	return pgrp, nil
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
