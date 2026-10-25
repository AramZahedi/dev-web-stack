package performance

import (
	"fmt"
	"sort"
)

// PHPMode is the php-fpm process manager mode.
type PHPMode string

const (
	// PHPModeStatic pre-forks the whole computed budget.
	PHPModeStatic PHPMode = "static"
	// PHPModeDynamic grows and shrinks workers between the spare bounds.
	PHPModeDynamic PHPMode = "dynamic"
)

// NginxValues is the computed main/events/http tuning.
type NginxValues struct {
	Workers                 int    `json:"workers"`
	WorkerConnections       int    `json:"worker_connections"`
	WorkerRLimitNoFile      uint64 `json:"worker_rlimit_nofile"`
	OpenFileCacheMax        int    `json:"open_file_cache_max"`
	KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
	KeepaliveRequests       int    `json:"keepalive_requests"`
	Gzip                    bool   `json:"gzip"`
	GzipCompLevel           int    `json:"gzip_comp_level"`
}

// ApacheValues is the computed MPM event tuning.
type ApacheValues struct {
	ThreadsPerChild          int  `json:"threads_per_child"`
	ServerLimit              int  `json:"server_limit"`
	MaxRequestWorkers        int  `json:"max_request_workers"`
	StartServers             int  `json:"start_servers"`
	MinSpareThreads          int  `json:"min_spare_threads"`
	MaxSpareThreads          int  `json:"max_spare_threads"`
	AsyncRequestWorkerFactor int  `json:"async_request_worker_factor"`
	MaxConnectionsPerChild   int  `json:"max_connections_per_child"`
	KeepAlive                bool `json:"keep_alive"`
	MaxKeepAliveRequests     int  `json:"max_keepalive_requests"`
	KeepAliveTimeoutSeconds  int  `json:"keepalive_timeout_seconds"`
	ListenBacklog            int  `json:"listen_backlog"`
}

// OpcacheValues is applied to each PHP-FPM pool.
type OpcacheValues struct {
	Enabled               bool `json:"enabled"`
	MemoryMB              int  `json:"memory_mb"`
	InternedStringsMB     int  `json:"interned_strings_mb"`
	MaxAcceleratedFiles   int  `json:"max_accelerated_files"`
	ValidateTimestamps    bool `json:"validate_timestamps"`
	RevalidateFreqSeconds int  `json:"revalidate_freq_seconds"`
}

// PHPSite is one active PHP site's share of the global child budget.
type PHPSite struct {
	SiteID          string `json:"site_id"`
	MaxChildren     int    `json:"max_children"`
	StartServers    int    `json:"start_servers"`
	MinSpareServers int    `json:"min_spare_servers"`
	MaxSpareServers int    `json:"max_spare_servers"`
}

// PHPValues is the computed global and per-site PHP-FPM budget.
type PHPValues struct {
	Mode                    PHPMode       `json:"mode"`
	GlobalChildren          int           `json:"global_children"`
	Sites                   []PHPSite     `json:"sites"`
	ListenBacklog           int           `json:"listen_backlog"`
	Opcache                 OpcacheValues `json:"opcache"`
	RealpathCacheSizeKB     int           `json:"realpath_cache_size_kb"`
	RealpathCacheTTLSeconds int           `json:"realpath_cache_ttl_seconds"`
}

// HostValues is the allowlisted host tuning to apply through sysctl and
// limits.d. It is data only; this package never touches /etc.
type HostValues struct {
	FileMax          uint64    `json:"file_max"`
	Somaxconn        int       `json:"somaxconn"`
	NetdevMaxBacklog int       `json:"netdev_max_backlog"`
	IPLocalPortRange PortRange `json:"ip_local_port_range"`
	NoFileSoft       uint64    `json:"nofile_soft"`
	NoFileHard       uint64    `json:"nofile_hard"`
	User             string    `json:"user,omitempty"`
	Group            string    `json:"group,omitempty"`
}

// Values is one fully computed performance profile.
type Values struct {
	Profile  Profile      `json:"profile"`
	Hostname string       `json:"hostname,omitempty"`
	Arch     string       `json:"arch,omitempty"`
	Nginx    NginxValues  `json:"nginx"`
	Apache   ApacheValues `json:"apache"`
	PHP      PHPValues    `json:"php"`
	Host     HostValues   `json:"host"`
}

// Options selects the profile and the active PHP sites. Custom values are
// required with ProfileCustom and are validated, not recomputed; PHPSites is
// ignored for a custom profile because the stored values already name sites.
type Options struct {
	Profile                           Profile
	PHPSites                          []string
	DisableOpcacheTimestampValidation bool
	Custom                            *Values
}

// Compute produces a bounded profile from measured host resources. Custom
// profiles are validated as supplied so devstack never silently regenerates
// user-edited values.
func Compute(res Resources, opts Options) (Values, error) {
	if err := res.Validate(); err != nil {
		return Values{}, err
	}
	if err := opts.validate(); err != nil {
		return Values{}, err
	}
	if opts.Profile == Custom {
		v := *opts.Custom
		v.Profile = Custom
		v.PHP.Sites = append([]PHPSite(nil), v.PHP.Sites...)
		if err := v.Validate(); err != nil {
			return Values{}, err
		}
		return v, nil
	}

	sites, err := normalizeSites(opts.PHPSites)
	if err != nil {
		return Values{}, err
	}
	cpu := res.EffectiveCPUUnits()
	nginx := computeNginx(res, cpu, opts.Profile)
	apache := computeApache(res, cpu, opts.Profile)
	php := computePHP(res, cpu, opts.Profile, sites, opts.DisableOpcacheTimestampValidation)
	php, err = validatePHPMemory(res, php)
	if err != nil {
		return Values{}, err
	}
	host := computeHost(res, nginx, apache, php)
	backlog := clampInt(host.Somaxconn, 1, maxBacklog)
	apache.ListenBacklog = backlog
	php.ListenBacklog = backlog

	v := Values{
		Profile:  opts.Profile,
		Hostname: res.Hostname,
		Arch:     res.Arch,
		Nginx:    nginx,
		Apache:   apache,
		PHP:      php,
		Host:     host,
	}
	if err := v.Validate(); err != nil {
		return Values{}, fmt.Errorf("performance: computed profile failed validation: %w", err)
	}
	return v, nil
}

func (opts Options) validate() error {
	if err := opts.Profile.Validate(); err != nil {
		return err
	}
	if opts.Custom != nil && opts.Profile != Custom {
		return invalid("custom", "values are only accepted with the custom profile")
	}
	if opts.Profile == Custom && opts.Custom == nil {
		return invalid("custom", "the custom profile requires explicit values")
	}
	if opts.Custom != nil && opts.Custom.Profile != "" && opts.Custom.Profile != Custom {
		return invalid("custom.profile", "must be custom, got %q", opts.Custom.Profile)
	}
	if opts.Profile != Custom {
		if _, err := normalizeSites(opts.PHPSites); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSites(ids []string) ([]string, error) {
	if len(ids) > maxPHPSites {
		return nil, invalid("php sites", "%d exceeds the %d-site cap", len(ids), maxPHPSites)
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := validateSiteID(id); err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, invalid("php sites", "%q is duplicated", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func computeNginx(res Resources, cpu int, profile Profile) NginxValues {
	workers := clampInt(cpu, 1, maxNginxWorkers)
	desiredConnections := 1024
	if profile == Throughput {
		desiredConnections = clampInt(workers*1024, 2048, 8192)
	}

	fdBound := minNginxConnections
	if res.NoFileHard > 256 {
		bound := (res.NoFileHard - 256) / 2
		if bound > maxNginxConnections {
			bound = maxNginxConnections
		}
		if bound < minNginxConnections {
			bound = minNginxConnections
		}
		fdBound = int(bound)
	}
	sysBound := maxNginxConnections
	if res.Sysctl.FileMax > 0 {
		const systemReserve = 1024
		if res.Sysctl.FileMax <= systemReserve {
			sysBound = minNginxConnections
		} else {
			bound := (res.Sysctl.FileMax - systemReserve) / uint64(2*workers)
			if bound > maxNginxConnections {
				bound = maxNginxConnections
			}
			if bound < minNginxConnections {
				bound = minNginxConnections
			}
			sysBound = int(bound)
		}
	}

	connections := desiredConnections
	if connections > fdBound {
		connections = fdBound
	}
	if connections > sysBound {
		connections = sysBound
	}
	if connections < minNginxConnections {
		connections = minNginxConnections
	}

	need := uint64(connections)*2 + 1024
	if need < 4096 {
		need = 4096
	}
	rlimit := need
	if res.NoFileHard < rlimit {
		rlimit = res.NoFileHard
	}

	cache := workers * connections / 2
	cache = clampInt(cache, minOpenFileCache, maxOpenFileCache)

	gzip := true
	level := 2
	if profile == Throughput {
		gzip = false
		level = 0
	}
	return NginxValues{
		Workers:                 workers,
		WorkerConnections:       connections,
		WorkerRLimitNoFile:      rlimit,
		OpenFileCacheMax:        cache,
		KeepaliveTimeoutSeconds: 30,
		KeepaliveRequests:       10000,
		Gzip:                    gzip,
		GzipCompLevel:           level,
	}
}

func computeApache(res Resources, cpu int, profile Profile) ApacheValues {
	threads := minApacheThreads
	perCPU := 32
	fraction := uint64(10)
	if profile == Throughput {
		threads = maxApacheThreads
		perCPU = 64
		fraction = 15
	}

	cpuLimit := cpu
	if cpuLimit > maxApacheWorkers/perCPU {
		cpuLimit = maxApacheWorkers
	} else {
		cpuLimit *= perCPU
	}
	if cpuLimit < threads {
		cpuLimit = threads
	}

	const perProcessBytes = uint64(32 << 20)
	memBudget := pageAligned(res.EffectiveMemoryBytes()/100*fraction, res.PageSizeBytes)
	maxBudget := uint64(maxApacheWorkers) * perProcessBytes
	if memBudget > maxBudget {
		memBudget = maxBudget
	}
	memLimit := int(memBudget/perProcessBytes) * threads
	if memLimit < threads {
		memLimit = threads
	}

	fdLimit := maxApacheWorkers
	if res.Sysctl.FileMax > 0 {
		limit := res.Sysctl.FileMax / 4
		if limit > maxApacheWorkers {
			limit = maxApacheWorkers
		}
		fdLimit = int(limit)
		if fdLimit < threads {
			fdLimit = threads
		}
	}

	limit := minInt(cpuLimit, minInt(memLimit, fdLimit))
	if limit < threads {
		limit = threads
	}
	maxRequestWorkers := (limit / threads) * threads
	if maxRequestWorkers < 2*threads {
		// Apache's spare-thread invariant needs at least two child processes:
		// MaxSpareThreads >= MinSpareThreads + ThreadsPerChild.
		maxRequestWorkers = 2 * threads
	}
	serverLimit := (maxRequestWorkers + threads - 1) / threads
	if serverLimit > maxApacheServerLimit {
		serverLimit = maxApacheServerLimit
		maxRequestWorkers = serverLimit * threads
	}

	start := 2
	if profile == Throughput {
		start = cpu
	} else {
		start = (cpu + 1) / 2
	}
	if start < 2 {
		start = 2
	}
	if start > serverLimit {
		start = serverLimit
	}

	spareProcesses := serverLimit / 2
	if spareProcesses < 2 {
		spareProcesses = 2
	}
	if spareProcesses > 16 {
		spareProcesses = 16
	}
	maxSpare := threads * spareProcesses
	if maxSpare > maxRequestWorkers {
		maxSpare = maxRequestWorkers
	}
	minSpare := threads
	if minSpare >= maxSpare {
		minSpare = 1
	}
	if minSpare > maxSpare {
		minSpare = maxSpare
	}

	return ApacheValues{
		ThreadsPerChild:          threads,
		ServerLimit:              serverLimit,
		MaxRequestWorkers:        maxRequestWorkers,
		StartServers:             start,
		MinSpareThreads:          minSpare,
		MaxSpareThreads:          maxSpare,
		AsyncRequestWorkerFactor: 2,
		MaxConnectionsPerChild:   0,
		KeepAlive:                true,
		MaxKeepAliveRequests:     10000,
		KeepAliveTimeoutSeconds:  3,
	}
}

func computePHP(res Resources, cpu int, profile Profile, sites []string, disableOpcacheTimestamps bool) PHPValues {
	fraction := uint64(40)
	perCPU := 2
	mode := PHPModeDynamic
	realpathSize := 2048
	realpathTTL := 120
	if profile == Throughput {
		fraction = 60
		perCPU = 4
		mode = PHPModeStatic
		realpathSize = 4096
		realpathTTL = 600
	}

	memBudget := pageAligned(res.EffectiveMemoryBytes()/100*fraction, res.PageSizeBytes)
	byMemory := maxPHPGlobalChild
	if memBudget/phpChildMemoryBytes < uint64(maxPHPGlobalChild) {
		byMemory = int(memBudget / phpChildMemoryBytes)
	}
	byCPU := maxPHPGlobalChild
	if cpu <= maxPHPGlobalChild/perCPU {
		byCPU = cpu * perCPU
	}

	global := minInt(byMemory, byCPU)
	if len(sites) > 0 && global < len(sites) {
		// Every active site gets at least one child even on a tiny host.
		global = len(sites)
	}
	if len(sites) == 0 {
		global = 0
	}

	pools := make([]PHPSite, 0, len(sites))
	if len(sites) > 0 {
		base := global / len(sites)
		remainder := global % len(sites)
		for i, id := range sites {
			children := base
			if i < remainder {
				children++
			}
			pool := PHPSite{SiteID: id, MaxChildren: children}
			if mode == PHPModeStatic {
				pool.StartServers = children
				pool.MinSpareServers = children
				pool.MaxSpareServers = children
			} else {
				pool.StartServers = clampInt((children+3)/4, 1, children)
				pool.MinSpareServers = clampInt((children+7)/8, 1, children)
				pool.MaxSpareServers = clampInt((children+1)/2, pool.MinSpareServers, children)
				pool.StartServers = clampInt(pool.StartServers, pool.MinSpareServers, pool.MaxSpareServers)
			}
			pools = append(pools, pool)
		}
	}

	perSite := 1
	if len(sites) > 0 {
		perSite = global / len(sites)
	}
	opcacheMemory := clampInt(perSite*8, 32, maxOpcacheMemoryMB)
	opcacheInterned := clampInt(opcacheMemory/8, 8, maxOpcacheInternedMB)
	opcacheFiles := clampInt(perSite*500, 2000, maxOpcacheFiles)
	opcacheFiles = ((opcacheFiles + 999) / 1000) * 1000

	return PHPValues{
		Mode:           mode,
		GlobalChildren: global,
		Sites:          pools,
		Opcache: OpcacheValues{
			Enabled:               true,
			MemoryMB:              opcacheMemory,
			InternedStringsMB:     opcacheInterned,
			MaxAcceleratedFiles:   opcacheFiles,
			ValidateTimestamps:    !disableOpcacheTimestamps,
			RevalidateFreqSeconds: 0,
		},
		RealpathCacheSizeKB:     realpathSize,
		RealpathCacheTTLSeconds: realpathTTL,
	}
}

// validatePHPMemory enforces the aggregate PHP memory floor. Every active site
// needs at least one 64 MiB child and one OPcache allocation, so a host that
// cannot fit those minimums is rejected instead of silently overcommitting.
func validatePHPMemory(res Resources, php PHPValues) (PHPValues, error) {
	if len(php.Sites) == 0 {
		return php, nil
	}
	required := uint64(php.GlobalChildren)*phpChildMemoryBytes +
		uint64(len(php.Sites))*uint64(php.Opcache.MemoryMB)*(1<<20)
	if required <= res.EffectiveMemoryBytes() {
		return php, nil
	}
	return PHPValues{}, invalid("php memory budget",
		"minimum %d MiB for %d sites (%d children x 64 MiB + %d pools x %d MiB OPcache) exceeds available memory %d MiB",
		required>>20, len(php.Sites), php.GlobalChildren, len(php.Sites), php.Opcache.MemoryMB, res.EffectiveMemoryBytes()>>20)
}

func computeHost(res Resources, nginx NginxValues, apache ApacheValues, php PHPValues) HostValues {
	totalConnections := nginx.Workers*nginx.WorkerConnections + apache.MaxRequestWorkers
	fdDemand := uint64(nginx.Workers)*uint64(nginx.WorkerConnections)*2 +
		uint64(apache.MaxRequestWorkers)*2 +
		uint64(php.GlobalChildren)*2 + 32768
	fileMax := nextPowerOfTwo(fdDemand)
	if fileMax < minFileMax {
		fileMax = minFileMax
	}
	if fileMax > maxFileMax {
		fileMax = maxFileMax
	}
	if res.Sysctl.FileMax > fileMax {
		fileMax = res.Sysctl.FileMax
	}

	somaxconn := roundUpMultiple(totalConnections, 1024)
	if somaxconn < 1024 {
		somaxconn = 1024
	}
	if somaxconn > maxBacklog {
		somaxconn = maxBacklog
	}
	if res.Sysctl.Somaxconn > somaxconn {
		somaxconn = res.Sysctl.Somaxconn
	}

	netdev := roundUpMultiple(totalConnections/2, 1024)
	if netdev < 2048 {
		netdev = 2048
	}
	if netdev > maxBacklog {
		netdev = maxBacklog
	}
	if res.Sysctl.NetdevMaxBacklog > netdev {
		netdev = res.Sysctl.NetdevMaxBacklog
	}

	portMin, portMax := 10240, 65535
	if res.Sysctl.IPLocalPortRange.Min > 0 {
		if res.Sysctl.IPLocalPortRange.Min < portMin {
			portMin = res.Sysctl.IPLocalPortRange.Min
		}
		if res.Sysctl.IPLocalPortRange.Max > portMax {
			portMax = res.Sysctl.IPLocalPortRange.Max
		}
	}

	desiredNoFile := uint64(65536)
	if nginx.WorkerRLimitNoFile*2 > desiredNoFile {
		desiredNoFile = nginx.WorkerRLimitNoFile * 2
	}
	if uint64(apache.ServerLimit)*1024 > desiredNoFile {
		desiredNoFile = uint64(apache.ServerLimit) * 1024
	}
	if desiredNoFile > maxNoFile {
		desiredNoFile = maxNoFile
	}
	soft := res.NoFileSoft
	if desiredNoFile > soft {
		soft = desiredNoFile
	}
	hard := res.NoFileHard
	if desiredNoFile > hard {
		hard = desiredNoFile
	}
	if soft > hard {
		soft = hard
	}

	return HostValues{
		FileMax:          fileMax,
		Somaxconn:        somaxconn,
		NetdevMaxBacklog: netdev,
		IPLocalPortRange: PortRange{Min: portMin, Max: portMax},
		NoFileSoft:       soft,
		NoFileHard:       hard,
		User:             res.User,
		Group:            res.Group,
	}
}
