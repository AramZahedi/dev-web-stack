//go:build windows

package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ErrReadyTimeout is kept for API compatibility. Windows never runs a local
// site supervisor: every Windows site backend runs in WSL.
var ErrReadyTimeout = errors.New("supervisor: readiness timeout")

// ErrUnsupported is returned by Start on Windows. The Windows daemon delegates
// process lifecycle to the WSL agent backend instead of starting local
// processes.
var ErrUnsupported = errors.New("supervisor: local process supervision is not supported on Windows")

// ReadyCheck describes how to tell that a started process is ready.
type ReadyCheck struct {
	TCPAddr string
	Timeout time.Duration
}

// RestartPolicy bounds automatic restarts after unexpected exits.
type RestartPolicy struct {
	MaxRestarts int
	Window      time.Duration
	Backoff     []time.Duration
}

// ProcessSpec describes one supervised process.
type ProcessSpec struct {
	ID      string
	Argv    []string
	Env     map[string]string
	Dir     string
	Ready   ReadyCheck
	Restart RestartPolicy
	Stdout  io.Writer
	Stderr  io.Writer
}

// Status is a snapshot of a supervised process.
type Status struct {
	State     model.SiteStatus
	PID       int
	StartedAt time.Time
	Restarts  int
	LastError string
}

// Manager is a compile-time-compatible Windows stub. It owns no processes.
type Manager struct {
	StopGrace time.Duration
}

// NewManager returns a Windows supervisor stub.
func NewManager(string, *slog.Logger) *Manager {
	return &Manager{StopGrace: 10 * time.Second}
}

// Start always reports that local supervision is unavailable on Windows.
func (m *Manager) Start(context.Context, ProcessSpec) (Status, error) {
	return Status{State: model.StatusError, LastError: ErrUnsupported.Error()}, ErrUnsupported
}

// Stop is a no-op because the Windows manager owns no processes.
func (m *Manager) Stop(context.Context, string) error { return nil }

// StopGraceful is a no-op because the Windows manager owns no processes.
func (m *Manager) StopGraceful(context.Context, string) error { return nil }

// StopAll is a no-op because the Windows manager owns no processes.
func (m *Manager) StopAll(context.Context) error { return nil }

// Status reports that no process is known.
func (m *Manager) Status(string) (Status, bool) { return Status{}, false }

// CleanupOrphans is a no-op on Windows: there are no supervised local
// processes and no /proc pid records to verify.
func (m *Manager) CleanupOrphans() error { return nil }
