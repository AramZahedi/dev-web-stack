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
	Profile string                    `json:"profile"`
	Host    PerformanceHostValues     `json:"host"`
	Windows *PerformanceWindowsValues `json:"windows,omitempty"`
	WSLVM   *PerformanceWSLVMValues   `json:"wsl_vm,omitempty"`
}

// PerformanceWindowsValues is the allowlisted Windows host tuning.
type PerformanceWindowsValues struct {
	PowerPlan                string `json:"power_plan"`
	DynamicPortMin           int    `json:"dynamic_port_min"`
	DynamicPortMax           int    `json:"dynamic_port_max"`
	TCPTimedWaitDelaySeconds int    `json:"tcp_timed_wait_delay_seconds"`
	NetworkThrottlingIndex   uint32 `json:"network_throttling_index"`
	SystemResponsiveness     uint32 `json:"system_responsiveness"`
	DisableLastAccess        bool   `json:"disable_last_access"`
	Disable8dot3             bool   `json:"disable_8dot3"`
	DefenderExclusions       bool   `json:"defender_exclusions"`
	VHDXPath                 string `json:"vhdx_path,omitempty"`
}

// PerformanceWSLVMValues is the daemon-managed .wslconfig layer.
type PerformanceWSLVMValues struct {
	MemoryMB      int  `json:"memory_mb"`
	Processors    int  `json:"processors"`
	SwapMB        int  `json:"swap_mb"`
	RestartNeeded bool `json:"restart_needed,omitempty"`
}

// PerformanceTargetStatus reports one host layer.
type PerformanceTargetStatus struct {
	Target          string            `json:"target"`
	Available       bool              `json:"available"`
	Managed         bool              `json:"managed"`
	Applied         bool              `json:"applied"`
	Paths           []string          `json:"paths,omitempty"`
	BackupDir       string            `json:"backup_dir,omitempty"`
	RestartRequired bool              `json:"restart_required,omitempty"`
	Error           string            `json:"error,omitempty"`
	Steps           []PerformanceStep `json:"steps,omitempty"`
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
	Applied   bool                      `json:"applied"`
	Profile   string                    `json:"profile"`
	BackupDir string                    `json:"backup_dir"`
	AppliedAt time.Time                 `json:"applied_at"`
	Files     []PerformanceFileResult   `json:"files"`
	Verified  []string                  `json:"verified,omitempty"`
	Targets   []PerformanceTargetStatus `json:"targets,omitempty"`
	Steps     []PerformanceStep         `json:"steps"`
}

// PerformanceResetRequest asks the helper to restore the pre-devstack files.
type PerformanceResetRequest struct{}

// PerformanceResetResponse reports the restored or removed fixed files.
type PerformanceResetResponse struct {
	Restored  bool                      `json:"restored"`
	BackupDir string                    `json:"backup_dir,omitempty"`
	Files     []PerformanceFileResult   `json:"files,omitempty"`
	Targets   []PerformanceTargetStatus `json:"targets,omitempty"`
	Steps     []PerformanceStep         `json:"steps"`
}

// PerformanceStatusResponse reports the current host tuning files. Contents are
// read from the helper's fixed paths only and are used for the preview diff.
type PerformanceStatusResponse struct {
	Managed       bool                      `json:"managed"`
	Profile       string                    `json:"profile,omitempty"`
	SysctlPath    string                    `json:"sysctl_path"`
	LimitsPath    string                    `json:"limits_path"`
	SysctlPresent bool                      `json:"sysctl_present"`
	LimitsPresent bool                      `json:"limits_present"`
	SysctlContent []byte                    `json:"sysctl_content,omitempty"`
	LimitsContent []byte                    `json:"limits_content,omitempty"`
	BackupDir     string                    `json:"backup_dir,omitempty"`
	AppliedAt     *time.Time                `json:"applied_at,omitempty"`
	Host          *PerformanceHostValues    `json:"host,omitempty"`
	Targets       []PerformanceTargetStatus `json:"targets,omitempty"`
}
