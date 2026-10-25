package servicecontrol

import (
	"context"
	"errors"
	"fmt"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// Credentials is the explicit credential bundle exposed by reveal and rotate.
// Passwords are returned only from those explicit operations and are never
// persisted in services.Record.
type Credentials struct {
	Engine   string `json:"engine"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

var (
	// ErrCredentialsUnsupported reports that a service has no credential flow.
	ErrCredentialsUnsupported = errors.New("service credentials are not supported")
	// ErrHelperUnavailable reports that the privileged helper could not serve
	// a service RPC. Callers map it to HTTP 503.
	ErrHelperUnavailable = errors.New("service helper is unavailable")
)

// Preparer supplies service-specific setup, credential, and rollback behavior.
// Lanes register one preparer per catalog ID. Services without special setup
// use NoopPreparer.
type Preparer interface {
	PrepareInstall(ctx context.Context, svc services.Service) error
	PrepareActivate(ctx context.Context, svc services.Service) error
	CommitActivate(ctx context.Context, svc services.Service) error
	CleanupActivate(ctx context.Context, svc services.Service, cause error) error
	Credentials(ctx context.Context, id services.ID) (Credentials, error)
	RotateCredentials(ctx context.Context, id services.ID) (Credentials, error)
}

// NoopPreparer is the default preparer for services that need no scaffolding
// or credentials.
type NoopPreparer struct{}

// PrepareInstall implements Preparer.
func (NoopPreparer) PrepareInstall(context.Context, services.Service) error { return nil }

// PrepareActivate implements Preparer.
func (NoopPreparer) PrepareActivate(context.Context, services.Service) error { return nil }

// CommitActivate implements Preparer.
func (NoopPreparer) CommitActivate(context.Context, services.Service) error { return nil }

// CleanupActivate implements Preparer.
func (NoopPreparer) CleanupActivate(context.Context, services.Service, error) error {
	return nil
}

// Credentials implements Preparer and reports that the service has no
// credential flow rather than inventing fake credentials.
func (NoopPreparer) Credentials(_ context.Context, id services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, id)
}

// RotateCredentials implements Preparer and reports that the service has no
// credential flow.
func (NoopPreparer) RotateCredentials(_ context.Context, id services.ID) (Credentials, error) {
	return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, id)
}
