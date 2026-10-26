// Package wslbackend is the Windows daemon's WSL agent backend. T0.9 freezes
// the contract; Lane C owns the implementation.
package wslbackend

import (
	"context"
	"errors"
	"io"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ErrNotImplemented is returned by the frozen skeleton until Lane C wires the
// agent methods.
var ErrNotImplemented = errors.New("backend/wsl: the WSL agent backend is implemented by Lane C")

// Backend adapts the agent client to the backend.Backend contract.
type Backend struct {
	Client any
}

// New returns a skeleton Backend.
func New(client any) *Backend { return &Backend{Client: client} }

func (b *Backend) Prepare(context.Context, backend.SiteSpec) (backend.PreparedSite, error) {
	return backend.PreparedSite{}, ErrNotImplemented
}
func (b *Backend) Start(context.Context, string) (backend.ProcessInfo, error) {
	return backend.ProcessInfo{}, ErrNotImplemented
}
func (b *Backend) Stop(context.Context, string, bool) error { return ErrNotImplemented }
func (b *Backend) Status(context.Context, string) (backend.Status, error) {
	return backend.Status{}, ErrNotImplemented
}
func (b *Backend) Logs(context.Context, string, bool) (io.ReadCloser, error) {
	return nil, ErrNotImplemented
}
func (b *Backend) Exec(context.Context, string, []string, map[string]string) (backend.Result, error) {
	return backend.Result{}, ErrNotImplemented
}
func (b *Backend) EnsureRuntime(context.Context, string, string) (model.RuntimeBuild, error) {
	return model.RuntimeBuild{}, ErrNotImplemented
}
func (b *Backend) EnsureRuntimeBuild(context.Context, string) (model.RuntimeBuild, error) {
	return model.RuntimeBuild{}, ErrNotImplemented
}
func (b *Backend) RemoveRuntime(context.Context, string) error { return ErrNotImplemented }
func (b *Backend) InstallCA(context.Context, []byte) error     { return ErrNotImplemented }
func (b *Backend) Health(context.Context) error                { return ErrNotImplemented }

func (b *Backend) PreparePHP(context.Context, string, string, backend.SiteSpec) (backend.PreparedSite, error) {
	return backend.PreparedSite{}, ErrNotImplemented
}
func (b *Backend) StartPHP(context.Context, string, string) (backend.ProcessInfo, error) {
	return backend.ProcessInfo{}, ErrNotImplemented
}
func (b *Backend) StopPHP(context.Context, string, string) error { return ErrNotImplemented }

var _ backend.Backend = (*Backend)(nil)
var _ backend.PHPStager = (*Backend)(nil)
