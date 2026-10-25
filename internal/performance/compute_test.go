package performance

import (
	"errors"
	"strings"
	"testing"
)

func TestComputeBalancedLowResources(t *testing.T) {
	v := mustCompute(t, lowResources(), Balanced, "blog")

	if v.Profile != Balanced {
		t.Fatalf("Profile = %q", v.Profile)
	}
	if v.Hostname != "small" || v.Arch != "amd64" {
		t.Fatalf("host metadata = %q/%q, want small/amd64", v.Hostname, v.Arch)
	}
	if v.Nginx.Workers != 1 || v.Nginx.WorkerConnections != 1024 {
		t.Fatalf("nginx workers/connections = %d/%d, want 1/1024", v.Nginx.Workers, v.Nginx.WorkerConnections)
	}
	if v.Nginx.WorkerRLimitNoFile != 4096 {
		t.Fatalf("worker_rlimit_nofile = %d, want 4096", v.Nginx.WorkerRLimitNoFile)
	}
	if v.Nginx.OpenFileCacheMax != 1000 {
		t.Fatalf("open_file_cache = %d, want 1000", v.Nginx.OpenFileCacheMax)
	}
	if !v.Nginx.Gzip || v.Nginx.GzipCompLevel != 2 {
		t.Fatalf("balanced gzip = %v level %d, want true level 2", v.Nginx.Gzip, v.Nginx.GzipCompLevel)
	}

	if v.Apache.ThreadsPerChild != 25 {
		t.Fatalf("ThreadsPerChild = %d, want 25", v.Apache.ThreadsPerChild)
	}
	if v.Apache.MaxRequestWorkers != 50 || v.Apache.ServerLimit != 2 {
		t.Fatalf("MaxRequestWorkers/ServerLimit = %d/%d, want 50/2", v.Apache.MaxRequestWorkers, v.Apache.ServerLimit)
	}
	if v.Apache.MinSpareThreads != 25 || v.Apache.MaxSpareThreads != 50 {
		t.Fatalf("spare threads = %d/%d, want 25/50", v.Apache.MinSpareThreads, v.Apache.MaxSpareThreads)
	}

	if v.PHP.Mode != PHPModeDynamic {
		t.Fatalf("PHP mode = %q, want dynamic", v.PHP.Mode)
	}
	if v.PHP.GlobalChildren != 1 || len(v.PHP.Sites) != 1 || v.PHP.Sites[0].MaxChildren != 1 {
		t.Fatalf("PHP budget = %d with sites %+v, want 1 child", v.PHP.GlobalChildren, v.PHP.Sites)
	}
	if v.PHP.Sites[0].StartServers != 1 || v.PHP.Sites[0].MinSpareServers != 1 || v.PHP.Sites[0].MaxSpareServers != 1 {
		t.Fatalf("dynamic spares = %+v, want 1/1/1", v.PHP.Sites[0])
	}

	if v.Host.Somaxconn != 2048 {
		t.Fatalf("Somaxconn = %d, want 2048", v.Host.Somaxconn)
	}
	if v.Host.FileMax != 65536 {
		t.Fatalf("FileMax = %d, want 65536", v.Host.FileMax)
	}
	if v.Host.NoFileSoft != 65536 || v.Host.NoFileHard != 65536 {
		t.Fatalf("nofile = %d/%d, want 65536/65536", v.Host.NoFileSoft, v.Host.NoFileHard)
	}
	if v.Apache.ListenBacklog != v.Host.Somaxconn || v.PHP.ListenBacklog != v.Host.Somaxconn {
		t.Fatalf("backlogs apache/php = %d/%d, want %d", v.Apache.ListenBacklog, v.PHP.ListenBacklog, v.Host.Somaxconn)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("computed values invalid: %v", err)
	}
}

func TestComputeThroughputHighResources(t *testing.T) {
	v := mustCompute(t, highResources(), Throughput, "d", "b", "a", "c")

	if v.Nginx.Workers != 8 {
		t.Fatalf("nginx workers = %d, want 8 (8.5 CPU quota floored)", v.Nginx.Workers)
	}
	if v.Nginx.WorkerConnections != 8192 {
		t.Fatalf("worker_connections = %d, want 8192", v.Nginx.WorkerConnections)
	}
	if v.Nginx.WorkerRLimitNoFile != 17408 {
		t.Fatalf("worker_rlimit_nofile = %d, want 17408", v.Nginx.WorkerRLimitNoFile)
	}
	if v.Nginx.OpenFileCacheMax != 20000 {
		t.Fatalf("open_file_cache = %d, want 20000", v.Nginx.OpenFileCacheMax)
	}
	if v.Nginx.Gzip {
		t.Fatal("throughput gzip must be off")
	}

	if v.Apache.ThreadsPerChild != 32 {
		t.Fatalf("ThreadsPerChild = %d, want 32", v.Apache.ThreadsPerChild)
	}
	if v.Apache.MaxRequestWorkers != 512 || v.Apache.ServerLimit != 16 {
		t.Fatalf("MaxRequestWorkers/ServerLimit = %d/%d, want 512/16", v.Apache.MaxRequestWorkers, v.Apache.ServerLimit)
	}
	if v.Apache.MaxRequestWorkers%v.Apache.ThreadsPerChild != 0 {
		t.Fatalf("MaxRequestWorkers %d is not a multiple of ThreadsPerChild %d", v.Apache.MaxRequestWorkers, v.Apache.ThreadsPerChild)
	}

	if v.PHP.Mode != PHPModeStatic {
		t.Fatalf("PHP mode = %q, want static", v.PHP.Mode)
	}
	if v.PHP.GlobalChildren != 32 {
		t.Fatalf("PHP global children = %d, want 32", v.PHP.GlobalChildren)
	}
	if got := phpSiteIDs(v); len(got) != 4 || got[0] != "a" || got[1] != "b" || got[2] != "c" || got[3] != "d" {
		t.Fatalf("PHP sites = %v, want sorted a,b,c,d", got)
	}
	for _, site := range v.PHP.Sites {
		if site.MaxChildren != 8 {
			t.Fatalf("site %q max_children = %d, want 8", site.SiteID, site.MaxChildren)
		}
		if site.StartServers != 8 || site.MinSpareServers != 8 || site.MaxSpareServers != 8 {
			t.Fatalf("static site %q spares = %+v, want max_children for all", site.SiteID, site)
		}
	}

	if v.Host.Somaxconn != 65535 {
		t.Fatalf("Somaxconn = %d, want 65535", v.Host.Somaxconn)
	}
	if v.Host.NetdevMaxBacklog != 33792 {
		t.Fatalf("NetdevMaxBacklog = %d, want 33792", v.Host.NetdevMaxBacklog)
	}
	if v.Host.FileMax != 2<<20 {
		t.Fatalf("FileMax = %d, want existing 2 MiB preserved", v.Host.FileMax)
	}
	if v.Host.IPLocalPortRange != (PortRange{Min: 10240, Max: 65535}) {
		t.Fatalf("port range = %+v, want 10240-65535", v.Host.IPLocalPortRange)
	}
	if v.PHP.Opcache.MemoryMB != 64 || v.PHP.Opcache.InternedStringsMB != 8 || v.PHP.Opcache.MaxAcceleratedFiles != 4000 {
		t.Fatalf("opcache = %+v, want 64/8/4000", v.PHP.Opcache)
	}
	if v.PHP.Opcache.ValidateTimestamps != true {
		t.Fatal("opcache validate_timestamps must default to true")
	}
	if v.PHP.RealpathCacheSizeKB != 4096 || v.PHP.RealpathCacheTTLSeconds != 600 {
		t.Fatalf("realpath cache = %d/%d, want 4096/600", v.PHP.RealpathCacheSizeKB, v.PHP.RealpathCacheTTLSeconds)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("computed values invalid: %v", err)
	}
}

func TestComputeCgroupQuotaBoundsWorkers(t *testing.T) {
	tests := []struct {
		quota float64
		want  int
	}{
		{0, 16},
		{0.5, 1},
		{1.4, 1},
		{1.5, 1},
		{2.9, 2},
		{8.5, 8},
		{40, 16},
	}
	for _, tt := range tests {
		res := testResources()
		res.CPUCount = 16
		res.CPUQuotaCPUs = tt.quota
		v := mustCompute(t, res, Throughput, "site")
		if v.Nginx.Workers != tt.want {
			t.Fatalf("quota %.1f: workers = %d, want %d", tt.quota, v.Nginx.Workers, tt.want)
		}
		if v.Apache.MaxRequestWorkers < v.Apache.ThreadsPerChild {
			t.Fatalf("quota %.1f: MaxRequestWorkers %d below threads %d", tt.quota, v.Apache.MaxRequestWorkers, v.Apache.ThreadsPerChild)
		}
	}
}

func TestComputeLowFileDescriptorsAreBounded(t *testing.T) {
	res := testResources()
	res.CPUCount = 2
	res.NoFileSoft = 256
	res.NoFileHard = 512
	res.Sysctl.FileMax = 4096
	v := mustCompute(t, res, Balanced, "site")

	if v.Nginx.WorkerConnections >= 1024 {
		t.Fatalf("worker_connections = %d, want below the 1024 default under a 512 fd hard limit", v.Nginx.WorkerConnections)
	}
	if v.Nginx.WorkerRLimitNoFile > res.NoFileHard {
		t.Fatalf("worker_rlimit_nofile = %d exceeds hard limit %d", v.Nginx.WorkerRLimitNoFile, res.NoFileHard)
	}
	if v.Nginx.WorkerConnections < minNginxConnections {
		t.Fatalf("worker_connections = %d below floor %d", v.Nginx.WorkerConnections, minNginxConnections)
	}
	if v.Nginx.WorkerConnections != 128 {
		t.Fatalf("worker_connections = %d, want 128", v.Nginx.WorkerConnections)
	}
	if v.Nginx.WorkerRLimitNoFile != 512 {
		t.Fatalf("worker_rlimit_nofile = %d, want 512", v.Nginx.WorkerRLimitNoFile)
	}
	if v.Host.NoFileHard < v.Nginx.WorkerRLimitNoFile {
		t.Fatalf("host nofile hard %d below worker_rlimit %d", v.Host.NoFileHard, v.Nginx.WorkerRLimitNoFile)
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("computed values invalid: %v", err)
	}
}

func TestApacheInvariants(t *testing.T) {
	tests := []struct {
		name    string
		res     Resources
		profile Profile
	}{
		{"one cpu balanced", lowResources(), Balanced},
		{"two cpu throughput", func() Resources {
			r := testResources()
			r.CPUCount = 2
			r.MemoryAvailableBytes = 512 << 20
			return r
		}(), Throughput},
		{"eight cpu balanced", testResources(), Balanced},
		{"eight cpu throughput", testResources(), Throughput},
		{"quota two throughput", func() Resources {
			r := testResources()
			r.CPUCount = 32
			r.CPUQuotaCPUs = 1.5
			return r
		}(), Throughput},
		{"sixty four cpu throughput", func() Resources {
			r := testResources()
			r.CPUCount = 64
			r.MemoryAvailableBytes = 64 << 30
			return r
		}(), Throughput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := mustCompute(t, tt.res, tt.profile, "site")
			a := v.Apache
			if a.ThreadsPerChild <= 0 || a.ThreadsPerChild > 64 {
				t.Fatalf("ThreadsPerChild = %d", a.ThreadsPerChild)
			}
			if a.MaxRequestWorkers%a.ThreadsPerChild != 0 {
				t.Fatalf("MaxRequestWorkers %d not a multiple of ThreadsPerChild %d", a.MaxRequestWorkers, a.ThreadsPerChild)
			}
			wantLimit := (a.MaxRequestWorkers + a.ThreadsPerChild - 1) / a.ThreadsPerChild
			if a.ServerLimit != wantLimit {
				t.Fatalf("ServerLimit = %d, want ceil(%d/%d) = %d", a.ServerLimit, a.MaxRequestWorkers, a.ThreadsPerChild, wantLimit)
			}
			if a.ServerLimit*a.ThreadsPerChild < a.MaxRequestWorkers {
				t.Fatalf("ServerLimit*Threads = %d < MaxRequestWorkers %d", a.ServerLimit*a.ThreadsPerChild, a.MaxRequestWorkers)
			}
			if a.StartServers < 1 || a.StartServers > a.ServerLimit {
				t.Fatalf("StartServers = %d, ServerLimit = %d", a.StartServers, a.ServerLimit)
			}
			if a.MinSpareThreads < 1 || a.MinSpareThreads > a.MaxSpareThreads {
				t.Fatalf("spare threads min/max = %d/%d", a.MinSpareThreads, a.MaxSpareThreads)
			}
			if a.MaxSpareThreads > a.MaxRequestWorkers {
				t.Fatalf("MaxSpareThreads = %d > MaxRequestWorkers %d", a.MaxSpareThreads, a.MaxRequestWorkers)
			}
			if a.ListenBacklog != v.Host.Somaxconn {
				t.Fatalf("ListenBacklog = %d, want somaxconn %d", a.ListenBacklog, v.Host.Somaxconn)
			}
			if a.MaxConnectionsPerChild != 0 || a.AsyncRequestWorkerFactor != 2 {
				t.Fatalf("MPM event tuning = %+v", a)
			}
		})
	}
}

func TestPHPBudgetSumsAcrossSites(t *testing.T) {
	tests := []struct {
		name    string
		res     Resources
		profile Profile
		sites   []string
		want    int
	}{
		{"no sites", testResources(), Throughput, nil, 0},
		{"one site low", lowResources(), Balanced, []string{"a"}, 1},
		{"three balanced", testResources(), Balanced, []string{"c", "a", "b"}, 16},
		{"five throughput", highResources(), Throughput, []string{"e", "a", "d", "b", "c"}, 32},
		{"more sites than cpu budget", func() Resources {
			r := lowResources()
			r.MemoryTotalBytes = 2 << 30
			r.MemoryAvailableBytes = 2 << 30
			return r
		}(), Balanced, []string{"a", "b", "c", "d", "e", "f"}, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := mustCompute(t, tt.res, tt.profile, tt.sites...)
			if v.PHP.GlobalChildren != tt.want {
				t.Fatalf("GlobalChildren = %d, want %d", v.PHP.GlobalChildren, tt.want)
			}
			sum := 0
			for _, site := range v.PHP.Sites {
				if site.MaxChildren < 1 {
					t.Fatalf("site %q MaxChildren = %d", site.SiteID, site.MaxChildren)
				}
				sum += site.MaxChildren
			}
			if sum != tt.want {
				t.Fatalf("sum(MaxChildren) = %d, want %d", sum, tt.want)
			}
			if len(v.PHP.Sites) > 1 {
				min, max := v.PHP.Sites[0].MaxChildren, v.PHP.Sites[0].MaxChildren
				for _, site := range v.PHP.Sites[1:] {
					if site.MaxChildren < min {
						min = site.MaxChildren
					}
					if site.MaxChildren > max {
						max = site.MaxChildren
					}
				}
				if max-min > 1 {
					t.Fatalf("per-site children spread = %d, want at most 1", max-min)
				}
			}
			wantMode := PHPModeDynamic
			if tt.profile == Throughput {
				wantMode = PHPModeStatic
			}
			if len(v.PHP.Sites) > 0 && v.PHP.Mode != wantMode {
				t.Fatalf("mode = %q, want %q", v.PHP.Mode, wantMode)
			}
			if err := v.Validate(); err != nil {
				t.Fatalf("computed values invalid: %v", err)
			}
		})
	}
}

func TestComputeCustomProfile(t *testing.T) {
	res := testResources()
	custom := mustCompute(t, res, Balanced, "a", "b")
	custom.Profile = Custom
	custom.Nginx.Workers = 2
	custom.Nginx.WorkerConnections = 2048
	custom.Nginx.WorkerRLimitNoFile = 8192
	custom.Apache.MaxRequestWorkers = 200
	custom.Apache.ServerLimit = 8
	custom.Apache.StartServers = 2
	custom.PHP.GlobalChildren = 2
	custom.PHP.Sites[0].MaxChildren = 1
	custom.PHP.Sites[0].StartServers = 1
	custom.PHP.Sites[0].MinSpareServers = 1
	custom.PHP.Sites[0].MaxSpareServers = 1
	custom.PHP.Sites[1].MaxChildren = 1
	custom.PHP.Sites[1].StartServers = 1
	custom.PHP.Sites[1].MinSpareServers = 1
	custom.PHP.Sites[1].MaxSpareServers = 1
	custom.Host.FileMax = 1 << 20
	custom.Host.NoFileHard = 1 << 20

	got, err := Compute(res, Options{Profile: Custom, Custom: &custom})
	if err != nil {
		t.Fatalf("Compute(custom): %v", err)
	}
	if got.Nginx.Workers != 2 || got.Apache.MaxRequestWorkers != 200 {
		t.Fatalf("custom values were not preserved: %+v", got)
	}
	if got.Profile != Custom {
		t.Fatalf("Profile = %q, want custom", got.Profile)
	}

	if _, err := Compute(res, Options{Profile: Custom}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Compute(custom without values) error = %v, want ErrInvalid", err)
	}

	bad := custom
	bad.Apache.MaxRequestWorkers = 201
	if _, err := Compute(res, Options{Profile: Custom, Custom: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Compute(custom non-multiple) error = %v, want ErrInvalid", err)
	}

	if _, err := Compute(res, Options{Profile: Balanced, Custom: &custom}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Compute(balanced with custom values) error = %v, want ErrInvalid", err)
	}

	bad = custom
	bad.Nginx.Workers = 0
	if _, err := Compute(res, Options{Profile: Custom, Custom: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Compute(custom zero workers) error = %v, want ErrInvalid", err)
	}
}

func TestComputeRejectsInvalidSites(t *testing.T) {
	res := testResources()
	for _, sites := range [][]string{
		{"a", "a"},
		{""},
		{"../escape"},
		{"a/b"},
	} {
		if _, err := Compute(res, Options{Profile: Balanced, PHPSites: sites}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Compute(sites=%q) error = %v, want ErrInvalid", sites, err)
		}
	}
}

func TestDisabledOpcacheTimestampValidation(t *testing.T) {
	v, err := Compute(lowResources(), Options{
		Profile:                           Balanced,
		PHPSites:                          []string{"a"},
		DisableOpcacheTimestampValidation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.PHP.Opcache.ValidateTimestamps {
		t.Fatal("ValidateTimestamps = true, want false")
	}
	if v.PHP.Opcache.RevalidateFreqSeconds != 0 {
		t.Fatalf("RevalidateFreqSeconds = %d, want 0", v.PHP.Opcache.RevalidateFreqSeconds)
	}
}

func TestComputeAllowsExistingSysctlAboveEngineCap(t *testing.T) {
	res := testResources()
	res.Sysctl.Somaxconn = 100000
	res.Sysctl.NetdevMaxBacklog = 100000
	v := mustCompute(t, res, Balanced, "site")

	if v.Host.Somaxconn != 100000 {
		t.Fatalf("host somaxconn = %d, want existing 100000 preserved", v.Host.Somaxconn)
	}
	if v.Host.NetdevMaxBacklog != 100000 {
		t.Fatalf("host netdev backlog = %d, want existing 100000 preserved", v.Host.NetdevMaxBacklog)
	}
	if v.Apache.ListenBacklog != maxBacklog || v.PHP.ListenBacklog != maxBacklog {
		t.Fatalf("listener backlogs = %d/%d, want engine cap %d", v.Apache.ListenBacklog, v.PHP.ListenBacklog, maxBacklog)
	}

	sysctl, err := RenderSysctl(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sysctl), "net.core.somaxconn = 100000") ||
		!strings.Contains(string(sysctl), "net.core.netdev_max_backlog = 100000") {
		t.Fatalf("existing high sysctl values were lowered:\n%s", sysctl)
	}
	apache, err := RenderApache(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apache), "ListenBacklog 65535") {
		t.Fatalf("Apache listener backlog was not clamped:\n%s", apache)
	}
}

func TestComputeRejectsPHPBudgetBelowPerSiteMinimums(t *testing.T) {
	res := lowResources()
	res.MemoryTotalBytes = 128 << 20
	res.MemoryAvailableBytes = 128 << 20
	sites := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	_, err := Compute(res, Options{Profile: Balanced, PHPSites: sites})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Compute(128 MiB, 8 sites) error = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "php memory") {
		t.Fatalf("error = %v, want a php memory budget explanation", err)
	}

	// Children fit in 600 MiB but the per-pool OPcache minimums do not.
	res.MemoryTotalBytes = 600 << 20
	res.MemoryAvailableBytes = 600 << 20
	if _, err := Compute(res, Options{Profile: Balanced, PHPSites: sites}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Compute(600 MiB, 8 sites) error = %v, want ErrInvalid", err)
	}

	res.MemoryTotalBytes = 2 << 30
	res.MemoryAvailableBytes = 2 << 30
	if _, err := Compute(res, Options{Profile: Balanced, PHPSites: sites}); err != nil {
		t.Fatalf("Compute(2 GiB, 8 sites): %v", err)
	}
}

func TestComputeCustomProfileSafetyCaps(t *testing.T) {
	base := mustCompute(t, testResources(), Balanced, "a", "b")
	base.Profile = Custom

	tests := []struct {
		name   string
		mutate func(*Values)
	}{
		{"file max", func(v *Values) { v.Host.FileMax = maxFileMax + 1 }},
		{"nofile soft", func(v *Values) {
			v.Host.NoFileSoft = maxNoFile + 1
			v.Host.NoFileHard = maxNoFile + 1
		}},
		{"nofile hard", func(v *Values) {
			v.Host.NoFileHard = maxNoFile + 1
			if v.Host.NoFileSoft > v.Host.NoFileHard {
				v.Host.NoFileSoft = v.Host.NoFileHard
			}
		}},
		{"open file cache", func(v *Values) { v.Nginx.OpenFileCacheMax = maxOpenFileCache + 1 }},
		{"opcache memory", func(v *Values) { v.PHP.Opcache.MemoryMB = 257 }},
		{"opcache interned", func(v *Values) {
			v.PHP.Opcache.MemoryMB = 256
			v.PHP.Opcache.InternedStringsMB = 65
		}},
		{"opcache max files", func(v *Values) { v.PHP.Opcache.MaxAcceleratedFiles = 100001 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := base
			v.PHP.Sites = append([]PHPSite(nil), base.PHP.Sites...)
			tt.mutate(&v)
			if _, err := Compute(testResources(), Options{Profile: Custom, Custom: &v}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Compute(custom) error = %v, want ErrInvalid", err)
			}
		})
	}
}
