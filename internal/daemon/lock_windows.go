//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
)

// ErrAlreadyRunning is returned by Run when another daemon holds the machine's
// lifetime lock.
var ErrAlreadyRunning = errors.New("daemon: already running")

// LockPath returns the single-instance lifetime lock path below home.
func LockPath(home string) string { return filepath.Join(home, "run", "daemon.lock") }

// SpawnLockPath serializes EnsureRunning callers.
func SpawnLockPath(home string) string { return filepath.Join(home, "run", "daemon.spawn.lock") }

// ReadyPath returns the readiness marker path below home.
func ReadyPath(home string) string { return filepath.Join(home, "run", "daemon.ready") }

func prepareRunDir(home string) error {
	dir := filepath.Join(home, "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("daemon: create run directory: %w", err)
	}
	return nil
}

// lifetimeLock holds a LockFileEx byte-range lock for the daemon lifetime.
type lifetimeLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireLifetimeLock(home string) (*lifetimeLock, error) {
	return acquireLockFile(LockPath(home))
}

func acquireSpawnLock(home string) (*lifetimeLock, error) {
	return acquireLockFile(SpawnLockPath(home))
}

func acquireLockFile(path string) (*lifetimeLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock %s: %w", path, err)
	}
	lock := &lifetimeLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &lock.overlapped,
	)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("daemon: lock %s: %w", path, err)
	}
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return lock, nil
}

func (l *lifetimeLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	_ = l.file.Close()
	l.file = nil
}

// lockHeld reports whether another process owns the lifetime lock.
func lockHeld(home string) bool {
	lock, err := acquireLifetimeLock(home)
	if err == nil {
		lock.release()
		return false
	}
	return errors.Is(err, ErrAlreadyRunning)
}
