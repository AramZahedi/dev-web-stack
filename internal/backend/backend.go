// Package backend defines the execution boundary for site processes. The local
// implementation runs on the host; later implementations may target WSL or a
// remote agent without changing callers.
package backend

import (
	"context"
	"io"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// SiteSpec describes one site as the backend needs to see it. Paths are
// expressed in the backend's namespace.
type SiteSpec struct {
	ID              string
	Kind            string
	HostPath        string
	BackendPath     string
	DocumentRoot    string
	Command         string
	Env             map[string]string
	HTTPPort        int
	FastCGIPort     int
	SPAFallback     bool
	FrontController bool
	RuntimeVersion  string

	// PHP fields describe one prepared PHP site. PHPBuildPath is the
	// revision-pinned build root, and the three paths are the generated
	// configuration the master must use.
	PHPVersion      string
	PHPBuild        string
	PHPBuildPath    string
	PHPIniPath      string
	FPMConfigPath   string
	PHPHealthScript string
}

// PreparedSite reports where a prepared site will be reachable. BackendHost is
// the address the proxy should dial.
type PreparedSite struct {
	BackendHost string
	HTTPPort    int
	FastCGIPort int
}

// ProcessInfo identifies a started site process.
type ProcessInfo struct {
	PID       int
	StartedAt time.Time
}

// Status is the observed state of a site process. Message carries the last
// supervisor error, when there is one.
type Status struct {
	State   model.SiteStatus
	PID     int
	Message string
}

// Result is the outcome of an Exec call. A non-zero ExitCode is a normal result;
// only failure to start the process is reported as an error.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// RuntimeProvider installs and removes PHP and Node distributions for a
// backend. Installations are delegated so the backend does not own downloads
// or checksums.
type RuntimeProvider interface {
	Ensure(ctx context.Context, kind, version string) (model.RuntimeBuild, error)
	// EnsureBuild installs or returns the exact pinned build id.
	EnsureBuild(ctx context.Context, buildID string) (model.RuntimeBuild, error)
	Remove(ctx context.Context, buildID string) error
}

// PHPStager is the generation-aware PHP process path. A site can keep its
// default master running while a candidate generation starts on another port;
// the active generation is the one Status and callers should observe.
type PHPStager interface {
	PreparePHP(ctx context.Context, siteID, generation string, spec SiteSpec) (PreparedSite, error)
	StartPHP(ctx context.Context, siteID, generation string) (ProcessInfo, error)
	StopPHP(ctx context.Context, siteID, generation string) error
}

// Backend is one execution namespace for site processes.
type Backend interface {
	// Prepare validates a site and stores the information needed to start it.
	// It is idempotent and does not start a process.
	Prepare(ctx context.Context, spec SiteSpec) (PreparedSite, error)
	// Start launches the prepared site and waits until it is ready.
	Start(ctx context.Context, id string) (ProcessInfo, error)
	// Stop terminates the site. graceful requests a SIGTERM-first shutdown.
	Stop(ctx context.Context, id string, graceful bool) error
	// Status returns the current process state. Unknown sites are stopped.
	Status(ctx context.Context, id string) (Status, error)
	// Logs returns the site's log stream. When follow is false the reader is a
	// finite snapshot of recent lines; when true it streams until closed.
	Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error)
	// Exec runs argv with the site environment and the caller's extra
	// environment. A non-zero exit is returned in Result, not as an error;
	// cancelling the context kills the process group and returns ctx.Err.
	Exec(ctx context.Context, id string, argv []string, env map[string]string) (Result, error)
	// EnsureRuntime installs or returns the requested runtime build.
	EnsureRuntime(ctx context.Context, kind, version string) (model.RuntimeBuild, error)
	// EnsureRuntimeBuild installs or returns the exact pinned build id.
	EnsureRuntimeBuild(ctx context.Context, buildID string) (model.RuntimeBuild, error)
	// RemoveRuntime deletes an installed runtime build.
	RemoveRuntime(ctx context.Context, buildID string) error
	// InstallCA combines the provided root certificate with the system trust
	// store and installs the bundle for backend processes.
	InstallCA(ctx context.Context, pem []byte) error
	// Health verifies that the backend's directories and dependencies are
	// usable.
	Health(ctx context.Context) error
}
