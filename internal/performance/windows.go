package performance

import (
	"fmt"
	"strings"
)

// WindowsHostValues is the allowlisted Windows host tuning derived from the
// selected profile. The daemon never sends arbitrary registry keys.
type WindowsHostValues struct {
	PowerPlan                string `json:"power_plan"`
	DynamicPortMin           int    `json:"dynamic_port_min"`
	DynamicPortMax           int    `json:"dynamic_port_max"`
	TCPTimedWaitDelaySeconds int    `json:"tcp_timed_wait_delay_seconds"`
	NetworkThrottlingIndex   uint32 `json:"network_throttling_index"`
	SystemResponsiveness     uint32 `json:"system_responsiveness"`
	DisableLastAccess        bool   `json:"disable_last_access"`
	Disable8dot3             bool   `json:"disable_8dot3"`
	DefenderExclusions       bool   `json:"defender_exclusions"`
	WSLMemoryMB              int    `json:"wsl_memory_mb"`
	WSLProcessors            int    `json:"wsl_processors"`
	WSLSwapMB                int    `json:"wsl_swap_mb"`
}

const (
	powerPlanBalanced        = "balanced"
	powerPlanHighPerformance = "high_performance"
	networkThrottlingDefault = 10
	networkThrottlingOff     = ^uint32(0)
	systemResponsivenessDef  = 20
	minWSLMemoryMB           = 2048
	minDynamicPort           = 1024
	maxDynamicPort           = 65535
	minDynamicPortCount      = 1024
)

// computeWindowsHost builds the Windows layer from measured resources.
func computeWindowsHost(res Resources, profile Profile) WindowsHostValues {
	switch profile {
	case Throughput:
		return WindowsHostValues{
			PowerPlan:                powerPlanHighPerformance,
			DynamicPortMin:           32768,
			DynamicPortMax:           60999,
			TCPTimedWaitDelaySeconds: 30,
			NetworkThrottlingIndex:   networkThrottlingOff,
			SystemResponsiveness:     0,
			DisableLastAccess:        true,
			Disable8dot3:             true,
			DefenderExclusions:       true,
			WSLMemoryMB:              wslMemoryMB(res, 80, 4096),
			WSLProcessors:            maxInt(1, res.CPUCount),
			WSLSwapMB:                wslSwapMB(res),
		}
	case Custom:
		return fillWindowsDefaults(WindowsHostValues{}, res)
	default:
		return WindowsHostValues{
			PowerPlan:                powerPlanBalanced,
			DynamicPortMin:           49152,
			DynamicPortMax:           65535,
			TCPTimedWaitDelaySeconds: 0,
			NetworkThrottlingIndex:   networkThrottlingDefault,
			SystemResponsiveness:     systemResponsivenessDef,
			DisableLastAccess:        false,
			Disable8dot3:             false,
			DefenderExclusions:       true,
			WSLMemoryMB:              wslMemoryMB(res, 50, 4096),
			WSLProcessors:            maxInt(1, res.CPUCount/2),
			WSLSwapMB:                0,
		}
	}
}

// fillWindowsDefaults fills zero values with the Throughput posture. Custom
// booleans are explicit; the caller controls them when it supplies a profile.
func fillWindowsDefaults(v WindowsHostValues, res Resources) WindowsHostValues {
	empty := v == (WindowsHostValues{})
	defaults := computeWindowsHost(res, Throughput)
	if strings.TrimSpace(v.PowerPlan) == "" {
		v.PowerPlan = powerPlanHighPerformance
	}
	if v.DynamicPortMin == 0 {
		v.DynamicPortMin = defaults.DynamicPortMin
	}
	if v.DynamicPortMax == 0 {
		v.DynamicPortMax = defaults.DynamicPortMax
	}
	if v.TCPTimedWaitDelaySeconds == 0 {
		v.TCPTimedWaitDelaySeconds = defaults.TCPTimedWaitDelaySeconds
	}
	if v.NetworkThrottlingIndex == 0 {
		v.NetworkThrottlingIndex = defaults.NetworkThrottlingIndex
	}
	if v.SystemResponsiveness == 0 {
		v.SystemResponsiveness = defaults.SystemResponsiveness
	}
	if v.WSLMemoryMB == 0 {
		v.WSLMemoryMB = defaults.WSLMemoryMB
	}
	if v.WSLProcessors == 0 {
		v.WSLProcessors = defaults.WSLProcessors
	}
	if v.WSLSwapMB == 0 && v.WSLMemoryMB == 0 && v.WSLProcessors == 0 {
		v.WSLSwapMB = defaults.WSLSwapMB
	}
	if empty {
		v.DisableLastAccess = true
		v.Disable8dot3 = true
		v.DefenderExclusions = true
	}
	return v
}

// Validate rejects values the Windows helper cannot safely apply.
func (w WindowsHostValues) Validate() error {
	switch strings.ToLower(strings.TrimSpace(w.PowerPlan)) {
	case powerPlanBalanced, powerPlanHighPerformance:
	default:
		return invalid("windows.power_plan", "must be balanced or high_performance")
	}
	if w.DynamicPortMin < minDynamicPort || w.DynamicPortMax > maxDynamicPort || w.DynamicPortMax <= w.DynamicPortMin {
		return invalid("windows.dynamic_port_range", "must be within %d-%d and increasing", minDynamicPort, maxDynamicPort)
	}
	if w.DynamicPortMax-w.DynamicPortMin+1 < minDynamicPortCount {
		return invalid("windows.dynamic_port_range", "must contain at least %d ports", minDynamicPortCount)
	}
	if w.TCPTimedWaitDelaySeconds != 0 && (w.TCPTimedWaitDelaySeconds < 30 || w.TCPTimedWaitDelaySeconds > 300) {
		return invalid("windows.tcp_timed_wait_delay_seconds", "must be 0 or 30-300")
	}
	if w.SystemResponsiveness > 100 {
		return invalid("windows.system_responsiveness", "must be 0-100")
	}
	if w.WSLMemoryMB < minWSLMemoryMB {
		return invalid("windows.wsl_memory_mb", "must be at least %d", minWSLMemoryMB)
	}
	if w.WSLProcessors < 1 || w.WSLProcessors > 256 {
		return invalid("windows.wsl_processors", "must be 1-256")
	}
	if w.WSLSwapMB < 0 {
		return invalid("windows.wsl_swap_mb", "must not be negative")
	}
	return nil
}

func wslMemoryMB(res Resources, percent, reserveMB int) int {
	if res.MemoryTotalBytes == 0 {
		return minWSLMemoryMB
	}
	total := int(res.MemoryTotalBytes / (1 << 20))
	if total <= reserveMB+minWSLMemoryMB {
		return maxInt(minWSLMemoryMB, total-reserveMB)
	}
	value := total * percent / 100
	if value > total-reserveMB {
		value = total - reserveMB
	}
	if value < minWSLMemoryMB {
		value = minWSLMemoryMB
	}
	return value
}

func wslSwapMB(res Resources) int {
	if res.MemoryTotalBytes == 0 {
		return 0
	}
	total := int(res.MemoryTotalBytes / (1 << 20))
	swap := total / 4
	if swap > 16384 {
		swap = 16384
	}
	return swap
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// String is used in diagnostics and test failures.
func (w WindowsHostValues) String() string {
	return fmt.Sprintf("power=%s ports=%d-%d timedwait=%d throttle=%d responsiveness=%d lastaccess=%t 8dot3=%t defender=%t wsl=%dMB/%dCPU/%dswap",
		w.PowerPlan, w.DynamicPortMin, w.DynamicPortMax, w.TCPTimedWaitDelaySeconds,
		w.NetworkThrottlingIndex, w.SystemResponsiveness, w.DisableLastAccess,
		w.Disable8dot3, w.DefenderExclusions, w.WSLMemoryMB, w.WSLProcessors, w.WSLSwapMB)
}
