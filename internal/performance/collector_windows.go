//go:build windows

package performance

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// defaultWindowsHandleLimit is the file handle quota used for Windows host resources.
const defaultWindowsHandleLimit = 65536

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func readWindowsMemory() (total, avail uint64, err error) {
	var ms memoryStatusEx
	ms.length = uint32(unsafe.Sizeof(ms))
	r1, _, lastErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return 0, 0, fmt.Errorf("GlobalMemoryStatusEx: %w", lastErr)
	}
	return ms.totalPhys, ms.availPhys, nil
}

// collectWindows gathers host resource metrics on Windows using GlobalMemoryStatusEx,
// runtime processor counts, and OS account metadata.
func collectWindows() (Resources, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return Resources{}, fmt.Errorf("%w: hostname: %v", ErrInvalid, err)
	}

	totalPhys, availPhys, err := readWindowsMemory()
	if err != nil {
		return Resources{}, fmt.Errorf("%w: read memory: %v", ErrInvalid, err)
	}

	user, group, err := currentAccount()
	if err != nil {
		return Resources{}, fmt.Errorf("%w: current account: %v", ErrInvalid, err)
	}

	pageSize := uint64(os.Getpagesize())
	if pageSize == 0 {
		pageSize = 4096
	}

	cpuCount := runtime.NumCPU()
	if cpuCount < 1 {
		cpuCount = 1
	}

	res := Resources{
		Hostname:             hostname,
		Arch:                 runtime.GOARCH,
		CPUCount:             cpuCount,
		CPUQuotaCPUs:         0,
		MemoryTotalBytes:     totalPhys,
		MemoryAvailableBytes: availPhys,
		PageSizeBytes:        pageSize,
		NoFileSoft:           defaultWindowsHandleLimit,
		NoFileHard:           defaultWindowsHandleLimit,
		User:                 user,
		Group:                group,
		Sysctl:               Sysctl{},
	}

	if err := res.Validate(); err != nil {
		return Resources{}, err
	}
	return res, nil
}
