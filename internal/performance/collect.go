package performance

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// CollectOptions supplies the test seams used by CollectFrom. Zero values use
// the real /proc, /sys, process limits, and current account.
type CollectOptions struct {
	ProcRoot   string
	SysRoot    string
	Hostname   func() (string, error)
	CPUCount   int
	PageSize   int
	Getrlimit  func() (soft, hard uint64, err error)
	LookupUser func() (name, group string, err error)
}

// Collect measures the host resources used by the performance engine.
func Collect() (Resources, error) {
	return CollectFrom(CollectOptions{})
}

// CollectFrom measures host resources with explicit roots and seams. Tests use
// temporary roots so no real host file is read or written.
func CollectFrom(opts CollectOptions) (Resources, error) {
	procRoot := opts.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	sysRoot := opts.SysRoot
	if sysRoot == "" {
		sysRoot = "/sys"
	}
	hostname := opts.Hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	cpuCount := opts.CPUCount
	if cpuCount == 0 {
		cpuCount = runtime.NumCPU()
	}
	pageSize := opts.PageSize
	if pageSize == 0 {
		pageSize = os.Getpagesize()
	}
	getrlimit := opts.Getrlimit
	if getrlimit == nil {
		getrlimit = func() (uint64, uint64, error) {
			var limit syscall.Rlimit
			if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
				return 0, 0, err
			}
			return limit.Cur, limit.Max, nil
		}
	}
	lookupUser := opts.LookupUser
	if lookupUser == nil {
		lookupUser = currentAccount
	}

	host, err := hostname()
	if err != nil {
		return Resources{}, fmt.Errorf("%w: hostname: %v", ErrInvalid, err)
	}
	total, available, err := readMeminfo(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return Resources{}, err
	}
	soft, hard, err := getrlimit()
	if err != nil {
		return Resources{}, fmt.Errorf("%w: RLIMIT_NOFILE: %v", ErrInvalid, err)
	}
	if soft == 0 && hard == 0 {
		return Resources{}, invalid("nofile", "RLIMIT_NOFILE is zero")
	}
	quota, err := readCPUQuota(procRoot, sysRoot)
	if err != nil {
		return Resources{}, err
	}
	name, group, err := lookupUser()
	if err != nil {
		return Resources{}, fmt.Errorf("%w: current account: %v", ErrInvalid, err)
	}
	sysctl := readSysctl(filepath.Join(procRoot, "sys"))

	res := Resources{
		Hostname:             host,
		Arch:                 runtime.GOARCH,
		CPUCount:             cpuCount,
		CPUQuotaCPUs:         quota,
		MemoryTotalBytes:     total,
		MemoryAvailableBytes: available,
		PageSizeBytes:        uint64(pageSize),
		NoFileSoft:           soft,
		NoFileHard:           hard,
		User:                 name,
		Group:                group,
		Sysctl:               sysctl,
	}
	if err := res.Validate(); err != nil {
		return Resources{}, err
	}
	return res, nil
}

func currentAccount() (string, string, error) {
	account, err := user.Current()
	if err != nil {
		return "", "", err
	}
	group := ""
	if groupID, err := user.LookupGroupId(account.Gid); err == nil {
		group = groupID.Name
	}
	return account.Username, group, nil
}

func readMeminfo(path string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: read memory: %v", ErrInvalid, err)
	}
	defer func() { _ = file.Close() }()

	var total, available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("%w: read memory: %v", ErrInvalid, err)
	}
	if total == 0 || available == 0 {
		return 0, 0, invalid("memory", "MemTotal or MemAvailable is missing from %s", path)
	}
	if available > total {
		available = total
	}
	return total, available, nil
}

func readCPUQuota(procRoot, sysRoot string) (float64, error) {
	cgroupRoot := filepath.Join(sysRoot, "fs", "cgroup")
	candidates := []string{cgroupRoot}
	if rel := cgroupV2Path(procRoot); rel != "" {
		if nested, err := safeJoin(cgroupRoot, rel); err == nil {
			candidates = append([]string{nested}, candidates...)
		}
	}
	var lastErr error
	for _, dir := range candidates {
		data, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
		if err == nil {
			return parseCPUQuota(string(data))
		}
		if !errors.Is(err, os.ErrNotExist) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return 0, fmt.Errorf("%w: read cgroup CPU quota: %v", ErrInvalid, lastErr)
	}
	quotaPath := filepath.Join(cgroupRoot, "cpu", "cpu.cfs_quota_us")
	periodPath := filepath.Join(cgroupRoot, "cpu", "cpu.cfs_period_us")

	quotaData, quotaErr := os.ReadFile(quotaPath)
	periodData, periodErr := os.ReadFile(periodPath)
	if errors.Is(quotaErr, os.ErrNotExist) && errors.Is(periodErr, os.ErrNotExist) {
		return 0, nil
	}
	if quotaErr != nil {
		return 0, fmt.Errorf("%w: read cgroup CPU quota: %v", ErrInvalid, quotaErr)
	}
	if periodErr != nil {
		return 0, fmt.Errorf("%w: read cgroup CPU period: %v", ErrInvalid, periodErr)
	}
	quota, err := strconv.ParseInt(strings.TrimSpace(string(quotaData)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: parse cgroup CPU quota: %v", ErrInvalid, err)
	}
	period, err := strconv.ParseInt(strings.TrimSpace(string(periodData)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: parse cgroup CPU period: %v", ErrInvalid, err)
	}
	return quotaFromParts(quota, period)
}

// cgroupV2Path returns the process's cgroup v2 path relative to the cgroup
// mount, or an empty string when /proc/self/cgroup has no v2 entry.
func cgroupV2Path(procRoot string) string {
	data, err := os.ReadFile(filepath.Join(procRoot, "self", "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 || fields[0] != "0" || fields[1] != "" {
			continue
		}
		rel := filepath.Clean("/" + fields[2])
		if rel == "/" {
			return ""
		}
		return strings.TrimPrefix(rel, "/")
	}
	return ""
}

// safeJoin keeps a cgroup path inside the mounted hierarchy.
func safeJoin(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("cgroup path is not relative")
	}
	joined := filepath.Join(root, rel)
	cleanRoot := filepath.Clean(root) + string(filepath.Separator)
	if !strings.HasPrefix(joined, cleanRoot) {
		return "", errors.New("cgroup path escapes the mount")
	}
	return joined, nil
}

func parseCPUQuota(raw string) (float64, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 {
		return 0, invalid("cpu quota", "%q must contain a quota and a period", raw)
	}
	if fields[0] == "max" {
		return 0, nil
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, invalid("cpu quota", "%q is not a number", fields[0])
	}
	period, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, invalid("cpu period", "%q is not a number", fields[1])
	}
	return quotaFromParts(quota, period)
}

func quotaFromParts(quota, period int64) (float64, error) {
	if quota <= 0 {
		return 0, nil
	}
	if period <= 0 {
		return 0, invalid("cpu period", "%d must be greater than zero", period)
	}
	return float64(quota) / float64(period), nil
}

func readSysctl(root string) Sysctl {
	var out Sysctl
	if value, ok := readUint(filepath.Join(root, "fs", "file-max")); ok {
		out.FileMax = value
	}
	if value, ok := readInt(filepath.Join(root, "net", "core", "somaxconn")); ok {
		out.Somaxconn = value
	}
	if value, ok := readInt(filepath.Join(root, "net", "core", "netdev_max_backlog")); ok {
		out.NetdevMaxBacklog = value
	}
	if data, err := os.ReadFile(filepath.Join(root, "net", "ipv4", "ip_local_port_range")); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 {
			min, errMin := strconv.Atoi(fields[0])
			max, errMax := strconv.Atoi(fields[1])
			if errMin == nil && errMax == nil && min > 0 && max > min && max <= 65535 {
				out.IPLocalPortRange = PortRange{Min: min, Max: max}
			}
		}
	}
	return out
}

func readUint(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func readInt(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return value, true
}
