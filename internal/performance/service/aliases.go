package service

import (
	"fmt"

	"github.com/AramZahedi/dev-web-stack/internal/performance"
)

// The pure engine owns the value types and renderers. These aliases keep the
// orchestration readable while the helper imports only the pure package.
type (
	Resources            = performance.Resources
	Profile              = performance.Profile
	Values               = performance.Values
	Options              = performance.Options
	PriorHost            = performance.PriorHost
	ManagedFile          = performance.ManagedFile
	Status               = performance.Status
	Preview              = performance.Preview
	ApplyResult          = performance.ApplyResult
	ResetResult          = performance.ResetResult
	Step                 = performance.Step
	AppliedState         = performance.AppliedState
	HostTuningStatus     = performance.HostTuningStatus
	IncludePreview       = performance.IncludePreview
	ApachePreview        = performance.ApachePreview
	PHPPreview           = performance.PHPPreview
	ManagedFilePreview   = performance.ManagedFilePreview
	HostFilePreview      = performance.HostFilePreview
	HostPreview          = performance.HostPreview
	ProfileRequest       = performance.ProfileRequest
	ApplyRequest         = performance.ApplyRequest
	ResetRequest         = performance.ResetRequest
	BenchmarkRequest     = performance.BenchmarkRequest
	BenchmarkResult      = performance.BenchmarkResult
	BenchmarkMeasurement = performance.BenchmarkMeasurement
	ApplyError           = performance.ApplyError
	ResetError           = performance.ResetError
	LoadOptions          = performance.LoadOptions
	LoadResult           = performance.LoadResult
	HostValues           = performance.HostValues
)

const (
	Balanced   = performance.Balanced
	Throughput = performance.Throughput
	Custom     = performance.Custom

	SysctlTarget = performance.SysctlTarget
	LimitsTarget = performance.LimitsTarget

	BenchmarkCompleted = performance.BenchmarkCompleted
	BenchmarkFailed    = performance.BenchmarkFailed
	BenchmarkCanceled  = performance.BenchmarkCanceled

	LoadToolName    = performance.LoadToolName
	LoadToolVersion = performance.LoadToolVersion
)

var (
	ErrInvalid              = performance.ErrInvalid
	ErrConfirmationRequired = performance.ErrConfirmationRequired
	ErrBenchmarkSite        = performance.ErrBenchmarkSite
	ErrBenchmarkNotFound    = performance.ErrBenchmarkNotFound
	ErrBenchmarkBusy        = performance.ErrBenchmarkBusy
	ErrProfileConflict      = performance.ErrProfileConflict
	LoadLimitations         = performance.LoadLimitations
	Compute                 = performance.Compute
	ParseProfile            = performance.ParseProfile
	RenderNginx             = performance.RenderNginx
	RenderApache            = performance.RenderApache
	RenderPHPPool           = performance.RenderPHPPool
	RenderPHPINI            = performance.RenderPHPINI
	RenderHostSysctl        = performance.RenderHostSysctl
	RenderHostLimits        = performance.RenderHostLimits
	BuildApplyPlan          = performance.BuildApplyPlan
	ResetManagedFiles       = performance.ResetManagedFiles
	RunLoad                 = performance.RunLoad
	Collect                 = performance.Collect
	PublicRouteURL          = performance.PublicRouteURL
)

func invalid(field, format string, args ...any) error {
	return &performance.ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Setting keys persisted in the daemon store.
const (
	settingProfile = "performance_profile"
	settingCustom  = "performance_custom"
	settingApplied = "performance_applied"
)
