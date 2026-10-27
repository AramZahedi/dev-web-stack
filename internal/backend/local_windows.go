//go:build windows

package backend

import (
	"context"
	"errors"
	"io"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// ErrLocalUnsupported is returned when Windows code accidentally uses the
// local Linux process backend. All Windows site backends run in the WSL agent;
// the daemon uses backend/wsl instead.
var ErrLocalUnsupported = errors.New("backend: the local host backend is not available on Windows")

// Local is a compile-time-compatible stub. It implements Backend and PHPStager
// but every lifecycle method returns ErrLocalUnsupported.
type Local struct{}

// NewLocal constructs the Windows stub.
func NewLocal(config.Config, *store.Store, *logs.Store, RuntimeProvider, string) (*Local, error) {
	return &Local{}, nil
}

func (l *Local) Prepare(context.Context, SiteSpec) (PreparedSite, error) {
	return PreparedSite{}, ErrLocalUnsupported
}

func (l *Local) Start(context.Context, string) (ProcessInfo, error) {
	return ProcessInfo{}, ErrLocalUnsupported
}

func (l *Local) Stop(context.Context, string, bool) error { return ErrLocalUnsupported }

// StopAll is a no-op because the Windows local stub owns no processes.
func (l *Local) StopAll(context.Context) error { return nil }

func (l *Local) Status(context.Context, string) (Status, error) {
	return Status{State: model.StatusStopped}, ErrLocalUnsupported
}

func (l *Local) Logs(context.Context, string, bool) (io.ReadCloser, error) {
	return nil, ErrLocalUnsupported
}

func (l *Local) Exec(context.Context, string, []string, map[string]string) (Result, error) {
	return Result{}, ErrLocalUnsupported
}

func (l *Local) EnsureRuntime(context.Context, string, string) (model.RuntimeBuild, error) {
	return model.RuntimeBuild{}, ErrLocalUnsupported
}

func (l *Local) EnsureRuntimeBuild(context.Context, string) (model.RuntimeBuild, error) {
	return model.RuntimeBuild{}, ErrLocalUnsupported
}

func (l *Local) RemoveRuntime(context.Context, string) error { return ErrLocalUnsupported }

func (l *Local) InstallCA(context.Context, []byte) error { return ErrLocalUnsupported }

func (l *Local) Health(context.Context) error { return ErrLocalUnsupported }

func (l *Local) PreparePHP(context.Context, string, string, SiteSpec) (PreparedSite, error) {
	return PreparedSite{}, ErrLocalUnsupported
}

func (l *Local) StartPHP(context.Context, string, string) (ProcessInfo, error) {
	return ProcessInfo{}, ErrLocalUnsupported
}

func (l *Local) StopPHP(context.Context, string, string) error { return ErrLocalUnsupported }

var _ Backend = (*Local)(nil)
var _ PHPStager = (*Local)(nil)
