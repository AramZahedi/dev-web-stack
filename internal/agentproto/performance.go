package agentproto

import "time"

// Performance endpoints exposed by the keyless privileged agent.
const (
	PathPerformanceStatus = "/v1/performance/status"
	PathPerformanceApply  = "/v1/performance/apply"
	PathPerformanceReset  = "/v1/performance/reset"
)

// PerformanceHostValues is the WSL host tuning allowlist.
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

// PerformanceWSLVMValues is the daemon-managed .wslconfig layer. The agent
// reports it but never writes the Windows-side file.
type PerformanceWSLVMValues struct {
	MemoryMB      int  `json:"memory_mb"`
	Processors    int  `json:"processors"`
	SwapMB        int  `json:"swap_mb"`
	RestartNeeded bool `json:"restart_needed,omitempty"`
}

// PerformanceStep is one apply/reset step result.
type PerformanceStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
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

// PerformanceApplyRequest asks the agent to apply the WSL host layer.
type PerformanceApplyRequest struct {
	Profile string                 `json:"profile"`
	Host    PerformanceHostValues  `json:"host"`
	WSLVM   PerformanceWSLVMValues `json:"wsl_vm,omitempty"`
}

// PerformanceStatusResponse reports all agent-managed host layers.
type PerformanceStatusResponse struct {
	Targets       []PerformanceTargetStatus `json:"targets"`
	AppliedAt     *time.Time                `json:"applied_at,omitempty"`
	SysctlContent []byte                    `json:"sysctl_content,omitempty"`
	LimitsContent []byte                    `json:"limits_content,omitempty"`
}

// PerformanceApplyResponse reports the applied layers.
type PerformanceApplyResponse struct {
	Applied   bool                      `json:"applied"`
	BackupDir string                    `json:"backup_dir,omitempty"`
	Targets   []PerformanceTargetStatus `json:"targets,omitempty"`
	Steps     []PerformanceStep         `json:"steps,omitempty"`
	AppliedAt time.Time                 `json:"applied_at"`
}

// PerformanceResetRequest asks the agent to restore the previous WSL state.
type PerformanceResetRequest struct{}

// PerformanceResetResponse reports restored layers.
type PerformanceResetResponse struct {
	Restored  bool                      `json:"restored"`
	BackupDir string                    `json:"backup_dir,omitempty"`
	Targets   []PerformanceTargetStatus `json:"targets,omitempty"`
	Steps     []PerformanceStep         `json:"steps,omitempty"`
}
