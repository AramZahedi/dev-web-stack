package helperproto

import "time"

// Performance endpoints exposed by the helper. Host tuning is limited to fixed
// system paths; the request never carries a path.
const (
	PathPerformanceStatus = "/v1/performance/status"
	PathPerformanceApply  = "/v1/performance/apply"
	PathPerformanceReset  = "/v1/performance/reset"
)

// PerformanceHostValues is the allowlisted host tuning metadata. The helper
// renders the fixed sysctl/limits files from these validated values and never
// accepts a target path, command, or free-form directive list.
type PerformanceHostValues struct {
	FileMax          uint64 `json:"file_max"`
	Somaxconn        int    `json:"somaxconn"`
	NetdevMaxBacklog int    `json:"netdev_max_backlog"`
	IPLocalPortMin   int    `json:"ip_local_port_range_min"`
	IPLocalPortMax   int    `json:"ip_local_port_range_max"`
	NoFileSoft       uint64 `json:"nofile_soft"`
	NoFileHard       uint64 `json:"nofile_hard"`
	User             string `json:"user,omitempty"`
	Group            string `json:"group,omitempty"`
}

// PerformanceApplyRequest asks the helper to apply host tuning to its fixed
// paths. Profile is metadata for the generated file header.
type PerformanceApplyRequest struct {
	Profile string                `json:"profile"`
	Host    PerformanceHostValues `json:"host"`
}

// PerformanceFileResult reports one fixed target file.
type PerformanceFileResult struct {
	Path     string `json:"path"`
	Bytes    int    `json:"bytes"`
	BackedUp bool   `json:"backed_up"`
}

// PerformanceStep is one apply/reset step result.
type PerformanceStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// PerformanceApplyResponse reports the fixed files written and verified.
type PerformanceApplyResponse struct {
	Applied   bool                    `json:"applied"`
	Profile   string                  `json:"profile"`
	BackupDir string                  `json:"backup_dir"`
	AppliedAt time.Time               `json:"applied_at"`
	Files     []PerformanceFileResult `json:"files"`
	Verified  []string                `json:"verified,omitempty"`
	Steps     []PerformanceStep       `json:"steps"`
}

// PerformanceResetRequest asks the helper to restore the pre-devstack files.
type PerformanceResetRequest struct{}

// PerformanceResetResponse reports the restored or removed fixed files.
type PerformanceResetResponse struct {
	Restored  bool                    `json:"restored"`
	BackupDir string                  `json:"backup_dir,omitempty"`
	Files     []PerformanceFileResult `json:"files,omitempty"`
	Steps     []PerformanceStep       `json:"steps"`
}

// PerformanceStatusResponse reports the current host tuning files. Contents are
// read from the helper's fixed paths only and are used for the preview diff.
type PerformanceStatusResponse struct {
	Managed       bool                   `json:"managed"`
	Profile       string                 `json:"profile,omitempty"`
	SysctlPath    string                 `json:"sysctl_path"`
	LimitsPath    string                 `json:"limits_path"`
	SysctlPresent bool                   `json:"sysctl_present"`
	LimitsPresent bool                   `json:"limits_present"`
	SysctlContent []byte                 `json:"sysctl_content,omitempty"`
	LimitsContent []byte                 `json:"limits_content,omitempty"`
	BackupDir     string                 `json:"backup_dir,omitempty"`
	AppliedAt     *time.Time             `json:"applied_at,omitempty"`
	Host          *PerformanceHostValues `json:"host,omitempty"`
}
