package performance

import (
	"errors"
	"time"
)

// Service-level errors. Invalid input still wraps ErrInvalid.
var (
	// ErrConfirmationRequired reports that a mutation was missing confirm:true.
	ErrConfirmationRequired = errors.New("performance: explicit confirmation is required")
	// ErrBenchmarkSite reports that the requested site is not registered.
	ErrBenchmarkSite = errors.New("performance: benchmark site is not registered")
	// ErrBenchmarkNotFound reports an unknown stored benchmark id.
	ErrBenchmarkNotFound = errors.New("performance: benchmark result not found")
	// ErrBenchmarkBusy reports that another benchmark is already running.
	ErrBenchmarkBusy = errors.New("performance: another benchmark is already running")
	// ErrProfileConflict reports that the base web-server config no longer
	// includes the managed performance snippet.
	ErrProfileConflict = errors.New("performance: managed performance include is not referenced by the base config")
)

// ProfileRequest is the PUT /performance/profile body.
type ProfileRequest struct {
	Profile string  `json:"profile"`
	Custom  *Values `json:"custom,omitempty"`
}

// ApplyRequest is the POST /performance/apply body. HostTuning is intentionally
// separate from confirmation: host-wide sysctl/limits changes are opt-in.
type ApplyRequest struct {
	Confirm    bool `json:"confirm"`
	HostTuning bool `json:"host_tuning"`
}

// ResetRequest is the POST /performance/reset body.
type ResetRequest struct {
	Confirm bool `json:"confirm"`
}

// BenchmarkRequest is the POST /performance/benchmarks body.
type BenchmarkRequest struct {
	Site            string `json:"site"`
	DurationSeconds int    `json:"duration_s"`
	Connections     int    `json:"connections"`
}

// Step is one apply/reset step result.
type Step struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Backup string `json:"backup,omitempty"`
}

// AppliedState is the last successfully applied profile.
type AppliedState struct {
	Profile    Profile                      `json:"profile"`
	Values     Values                       `json:"values"`
	AppliedAt  time.Time                    `json:"applied_at"`
	HostTuning bool                         `json:"host_tuning"`
	BackupDir  string                       `json:"backup_dir,omitempty"`
	Servers    []string                     `json:"servers,omitempty"`
	PHPIni     map[string]map[string]string `json:"php_ini,omitempty"`
	Steps      []Step                       `json:"steps,omitempty"`
}

// HostTuningStatus reports the helper-owned host tuning state.
type HostTuningStatus struct {
	Available  bool       `json:"available"`
	Managed    bool       `json:"managed"`
	Applied    bool       `json:"applied"`
	SysctlPath string     `json:"sysctl_path"`
	LimitsPath string     `json:"limits_path"`
	BackupDir  string     `json:"backup_dir,omitempty"`
	AppliedAt  *time.Time `json:"applied_at,omitempty"`
	Profile    Profile    `json:"profile,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Status is the GET /performance response.
type Status struct {
	Profile      Profile          `json:"profile"`
	Custom       *Values          `json:"custom,omitempty"`
	Resources    Resources        `json:"resources"`
	Values       Values           `json:"values"`
	Applied      *AppliedState    `json:"applied,omitempty"`
	HostTuning   HostTuningStatus `json:"host_tuning"`
	ActiveServer string           `json:"active_server,omitempty"`
	Warnings     []string         `json:"warnings,omitempty"`
}

// IncludePreview is the three managed nginx snippets.
type IncludePreview struct {
	Main   string `json:"main"`
	Events string `json:"events"`
	HTTP   string `json:"http"`
}

// ApachePreview is the managed Apache performance file.
type ApachePreview struct {
	Config string `json:"config"`
}

// PHPPreview is one site's generated PHP fragments.
type PHPPreview struct {
	SiteID string `json:"site_id"`
	Pool   string `json:"pool"`
	INI    string `json:"ini"`
}

// ManagedFilePreview is one generated include shown without writing it.
type ManagedFilePreview struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Active  bool   `json:"active"`
}

// HostFilePreview is the current/proposed fixed host tuning file and diff.
type HostFilePreview struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
	Diff     string `json:"diff"`
}

// HostPreview is the host tuning section of a preview.
type HostPreview struct {
	OptIn    bool              `json:"opt_in"`
	Files    []HostFilePreview `json:"files"`
	Warnings []string          `json:"warnings,omitempty"`
}

// Preview is the GET /performance/preview response. It is produced entirely
// in memory and never writes a file.
type Preview struct {
	Profile      Profile              `json:"profile"`
	Values       Values               `json:"values"`
	Resources    Resources            `json:"resources"`
	ActiveServer string               `json:"active_server,omitempty"`
	Nginx        IncludePreview       `json:"nginx"`
	Apache       ApachePreview        `json:"apache"`
	PHP          []PHPPreview         `json:"php"`
	Managed      []ManagedFilePreview `json:"managed_files"`
	Host         HostPreview          `json:"host_tuning"`
	Warnings     []string             `json:"warnings,omitempty"`
}

// ApplyResult reports each step and the rollback location.
type ApplyResult struct {
	Applied       bool                 `json:"applied"`
	Profile       Profile              `json:"profile"`
	AppliedAt     time.Time            `json:"applied_at"`
	HostTuning    bool                 `json:"host_tuning"`
	ActiveServer  string               `json:"active_server,omitempty"`
	Files         []ManagedFilePreview `json:"files,omitempty"`
	Steps         []Step               `json:"steps"`
	BackupDir     string               `json:"backup_dir,omitempty"`
	RollbackError string               `json:"rollback_error,omitempty"`
	Warnings      []string             `json:"warnings,omitempty"`
}

// ResetResult reports each reset step.
type ResetResult struct {
	Reset         bool     `json:"reset"`
	Steps         []Step   `json:"steps"`
	BackupDir     string   `json:"backup_dir,omitempty"`
	RollbackError string   `json:"rollback_error,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// ApplyError carries the per-step result when apply fails after making changes.
type ApplyError struct {
	Err    error
	Result ApplyResult
}

// Error implements error.
func (e *ApplyError) Error() string {
	if e == nil || e.Err == nil {
		return "performance: apply failed"
	}
	return "performance: apply failed: " + e.Err.Error()
}

// Unwrap exposes the underlying apply failure.
func (e *ApplyError) Unwrap() error { return e.Err }

// ResetError carries the per-step result when reset fails after making changes.
type ResetError struct {
	Err    error
	Result ResetResult
}

// Error implements error.
func (e *ResetError) Error() string {
	if e == nil || e.Err == nil {
		return "performance: reset failed"
	}
	return "performance: reset failed: " + e.Err.Error()
}

// Unwrap exposes the underlying reset failure.
func (e *ResetError) Unwrap() error { return e.Err }

// Benchmark status values.
const (
	BenchmarkCompleted = "completed"
	BenchmarkFailed    = "failed"
	BenchmarkCanceled  = "canceled"
)

// BenchmarkMeasurement is measured load-generator output. It is omitted when a
// run failed or was canceled; no RPS is ever synthesized.
type BenchmarkMeasurement struct {
	Requests        int64   `json:"requests"`
	Errors          int64   `json:"errors"`
	RPS             float64 `json:"requests_per_second"`
	P50MS           float64 `json:"p50_ms"`
	P95MS           float64 `json:"p95_ms"`
	P99MS           float64 `json:"p99_ms"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// BenchmarkResult is one stored benchmark run.
type BenchmarkResult struct {
	ID               string                `json:"id"`
	Site             string                `json:"site"`
	Target           string                `json:"target"`
	Profile          Profile               `json:"profile"`
	Status           string                `json:"status"`
	Tool             string                `json:"tool"`
	ToolVersion      string                `json:"tool_version"`
	Limitations      []string              `json:"limitations,omitempty"`
	DurationSeconds  int                   `json:"duration_s"`
	Connections      int                   `json:"connections"`
	StartedAt        time.Time             `json:"started_at"`
	FinishedAt       time.Time             `json:"finished_at"`
	Measured         *BenchmarkMeasurement `json:"measured,omitempty"`
	Error            string                `json:"error,omitempty"`
	Directory        string                `json:"directory"`
	RawFile          string                `json:"raw_file"`
	ProfileAppliedAt *time.Time            `json:"profile_applied_at,omitempty"`
}
