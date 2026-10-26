//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ErrAlreadyRunning is returned by Run when another daemon holds the machine's
// lifetime lock.
var ErrAlreadyRunning = errors.New("daemon: already running")

// LockPath returns the single-instance lifetime lock path below home.
func LockPath(home string) string {
	return filepath.Join(home, "run", "daemon.lock")
}

// SpawnLockPath serializes EnsureRunning callers so only one process performs
// the check-and-spawn sequence. It is separate from the lifetime lock because
// the child must be able to acquire the lifetime lock while the parent still
// holds the spawn lock.
func SpawnLockPath(home string) string {
	return filepath.Join(home, "run", "daemon.spawn.lock")
}

// ReadyPath returns the readiness marker path below home. It exists only while
// the daemon has finished startup and has not begun shutting down.
func ReadyPath(home string) string {
	return filepath.Join(home, "run", "daemon.ready")
}

// prepareRunDir creates the run directory with owner-only permissions. A
// permissive run directory would briefly expose the control socket before its
// 0600 mode is applied.
func prepareRunDir(home string) error {
	dir := filepath.Join(home, "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("daemon: create run directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("daemon: set run directory permissions: %w", err)
	}
	return nil
}

// lifetimeLock is an exclusive advisory lock held for the daemon's lifetime.
// The lock file itself is left behind; the kernel releases the lock when the
// file descriptor closes or the process exits.
type lifetimeLock struct {
	file *os.File
}

// acquireLifetimeLock takes the machine's non-blocking exclusive lock. A busy
// lock is reported as ErrAlreadyRunning.
func acquireLifetimeLock(home string) (*lifetimeLock, error) {
	return acquireLockFile(LockPath(home))
}

func acquireSpawnLock(home string) (*lifetimeLock, error) {
	return acquireLockFile(SpawnLockPath(home))
}

func acquireLockFile(path string) (*lifetimeLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock %s: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("daemon: set lock permissions %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("daemon: lock %s: %w", path, err)
	}
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return &lifetimeLock{file: file}, nil
}

func (l *lifetimeLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// lockHeld reports whether another process owns the lifetime lock. It probes
// by acquiring and immediately releasing the lock when free.
func lockHeld(home string) bool {
	lock, err := acquireLifetimeLock(home)
	if err == nil {
		lock.release()
		return false
	}
	return errors.Is(err, ErrAlreadyRunning)
}

// daemonReady reports whether a live daemon owns the lock, accepts connections
// on the control socket, and has published a ready marker for a live PID.
func daemonReady(home string) bool {
	if !lockHeld(home) {
		return false
	}
	if !socketReachable(SocketPath(home)) {
		return false
	}
	data, err := os.ReadFile(ReadyPath(home))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err == nil && processAlive(pid)
}

// writeAtomic writes data through a temporary file in the same directory and
// renames it into place with the requested mode.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	name := temp.Name()
	defer func() {
		if name != "" {
			_ = os.Remove(name)
		}
	}()

	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set mode on %s: %w", name, err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	name = ""
	return nil
}

// removeOwnedSocket removes path only when it is still the exact socket inode
// recorded by this daemon. A replacement daemon's socket is never deleted.
func removeOwnedSocket(path string, recorded os.FileInfo) error {
	if recorded == nil {
		return nil
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(recorded, current) {
		return nil
	}
	if current.Mode()&os.ModeSocket == 0 {
		return nil
	}
	return os.Remove(path)
}

// removeOwnedPID removes path only when it still contains this process's PID.
func removeOwnedPID(path, pid string) error {
	if strings.TrimSpace(pid) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) != pid {
		return nil
	}
	return os.Remove(path)
}

// removableSocketError reports whether a dial failure proves the socket is
// stale. Timeouts and permission errors are deliberately not removable: the
// listener may belong to a live but wedged daemon.
func removableSocketError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}
