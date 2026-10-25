package performance

// Validate checks every generated value and cross-field invariant. It is used
// for computed profiles and for user-supplied custom profiles.
func (v Values) Validate() error {
	if err := v.Profile.Validate(); err != nil {
		return err
	}
	if err := validateLabel("hostname", v.Hostname); err != nil {
		return err
	}
	if err := validateLabel("arch", v.Arch); err != nil {
		return err
	}
	if err := v.Nginx.validate(); err != nil {
		return err
	}
	if err := v.Apache.validate(); err != nil {
		return err
	}
	if err := v.PHP.validate(); err != nil {
		return err
	}
	if err := v.Host.validate(); err != nil {
		return err
	}
	wantBacklog := minInt(v.Host.Somaxconn, maxBacklog)
	if v.Apache.ListenBacklog != wantBacklog {
		return invalid("apache.listen_backlog", "%d must follow host somaxconn %d (capped at %d)", v.Apache.ListenBacklog, v.Host.Somaxconn, maxBacklog)
	}
	if v.PHP.ListenBacklog != wantBacklog {
		return invalid("php.listen_backlog", "%d must follow host somaxconn %d (capped at %d)", v.PHP.ListenBacklog, v.Host.Somaxconn, maxBacklog)
	}
	if v.Host.NoFileHard < v.Nginx.WorkerRLimitNoFile {
		return invalid("host.nofile_hard", "%d is below worker_rlimit_nofile %d", v.Host.NoFileHard, v.Nginx.WorkerRLimitNoFile)
	}
	demand := uint64(v.Nginx.Workers)*uint64(v.Nginx.WorkerConnections)*2 +
		uint64(v.Apache.MaxRequestWorkers)*2 +
		uint64(v.PHP.GlobalChildren)*2 + 32768
	if v.Host.FileMax < demand {
		return invalid("host.file_max", "%d is below the computed descriptor demand %d", v.Host.FileMax, demand)
	}
	if v.Profile == Custom {
		return v.validateCustomCaps()
	}
	return nil
}

// validateCustomCaps keeps user-edited profiles inside the engine's own
// safety bounds. Computed profiles may preserve higher existing host values,
// but custom values must not exceed the caps the generator would emit.
func (v Values) validateCustomCaps() error {
	if v.Host.FileMax > maxFileMax {
		return invalid("host.file_max", "%d exceeds the custom-profile cap %d", v.Host.FileMax, maxFileMax)
	}
	if v.Host.NoFileSoft > maxNoFile || v.Host.NoFileHard > maxNoFile {
		return invalid("host.nofile", "%d/%d exceeds the custom-profile cap %d", v.Host.NoFileSoft, v.Host.NoFileHard, maxNoFile)
	}
	if v.Nginx.OpenFileCacheMax > maxOpenFileCache {
		return invalid("nginx.open_file_cache", "%d exceeds the custom-profile cap %d", v.Nginx.OpenFileCacheMax, maxOpenFileCache)
	}
	if v.PHP.Opcache.MemoryMB > maxOpcacheMemoryMB {
		return invalid("php.opcache.memory_mb", "%d exceeds the custom-profile cap %d", v.PHP.Opcache.MemoryMB, maxOpcacheMemoryMB)
	}
	if v.PHP.Opcache.InternedStringsMB > maxOpcacheInternedMB {
		return invalid("php.opcache.interned_strings_mb", "%d exceeds the custom-profile cap %d", v.PHP.Opcache.InternedStringsMB, maxOpcacheInternedMB)
	}
	if v.PHP.Opcache.MaxAcceleratedFiles > maxOpcacheFiles {
		return invalid("php.opcache.max_accelerated_files", "%d exceeds the custom-profile cap %d", v.PHP.Opcache.MaxAcceleratedFiles, maxOpcacheFiles)
	}
	return nil
}

func (n NginxValues) validate() error {
	if n.Workers < 1 || n.Workers > maxNginxWorkers {
		return invalid("nginx.workers", "%d is outside 1-%d", n.Workers, maxNginxWorkers)
	}
	if n.WorkerConnections < minNginxConnections || n.WorkerConnections > maxNginxConnections {
		return invalid("nginx.worker_connections", "%d is outside %d-%d", n.WorkerConnections, minNginxConnections, maxNginxConnections)
	}
	if n.WorkerRLimitNoFile == 0 {
		return invalid("nginx.worker_rlimit_nofile", "must be greater than zero")
	}
	if n.WorkerRLimitNoFile < uint64(n.WorkerConnections) {
		return invalid("nginx.worker_rlimit_nofile", "%d is below worker_connections %d", n.WorkerRLimitNoFile, n.WorkerConnections)
	}
	if n.OpenFileCacheMax < 1 || n.OpenFileCacheMax > 1_000_000 {
		return invalid("nginx.open_file_cache", "%d is outside 1-1000000", n.OpenFileCacheMax)
	}
	if n.KeepaliveTimeoutSeconds < 1 || n.KeepaliveTimeoutSeconds > 86400 {
		return invalid("nginx.keepalive_timeout", "%d is outside 1-86400", n.KeepaliveTimeoutSeconds)
	}
	if n.KeepaliveRequests < 1 || n.KeepaliveRequests > 1_000_000 {
		return invalid("nginx.keepalive_requests", "%d is outside 1-1000000", n.KeepaliveRequests)
	}
	if n.GzipCompLevel < 0 || n.GzipCompLevel > 9 {
		return invalid("nginx.gzip_comp_level", "%d is outside 0-9", n.GzipCompLevel)
	}
	return nil
}

func (a ApacheValues) validate() error {
	if a.ThreadsPerChild < 1 || a.ThreadsPerChild > maxAllowedThreads {
		return invalid("apache.threads_per_child", "%d is outside 1-%d", a.ThreadsPerChild, maxAllowedThreads)
	}
	if a.MaxRequestWorkers < a.ThreadsPerChild || a.MaxRequestWorkers > maxApacheWorkers {
		return invalid("apache.max_request_workers", "%d must be at least ThreadsPerChild and at most %d", a.MaxRequestWorkers, maxApacheWorkers)
	}
	if a.MaxRequestWorkers%a.ThreadsPerChild != 0 {
		return invalid("apache.max_request_workers", "%d is not a multiple of ThreadsPerChild %d", a.MaxRequestWorkers, a.ThreadsPerChild)
	}
	wantServerLimit := (a.MaxRequestWorkers + a.ThreadsPerChild - 1) / a.ThreadsPerChild
	if a.ServerLimit != wantServerLimit || a.ServerLimit < 1 || a.ServerLimit > maxApacheServerLimit {
		return invalid("apache.server_limit", "%d must be ceil(%d/%d) and at most %d", a.ServerLimit, a.MaxRequestWorkers, a.ThreadsPerChild, maxApacheServerLimit)
	}
	if a.StartServers < 1 || a.StartServers > a.ServerLimit {
		return invalid("apache.start_servers", "%d is outside 1-%d", a.StartServers, a.ServerLimit)
	}
	if a.MinSpareThreads < 1 || a.MinSpareThreads > a.MaxSpareThreads || a.MaxSpareThreads > a.MaxRequestWorkers {
		return invalid("apache.spare_threads", "min %d, max %d, MaxRequestWorkers %d are inconsistent", a.MinSpareThreads, a.MaxSpareThreads, a.MaxRequestWorkers)
	}
	if a.MaxSpareThreads < a.MinSpareThreads+a.ThreadsPerChild {
		return invalid("apache.spare_threads", "MaxSpareThreads %d must be at least MinSpareThreads %d + ThreadsPerChild %d",
			a.MaxSpareThreads, a.MinSpareThreads, a.ThreadsPerChild)
	}
	if a.AsyncRequestWorkerFactor < 1 || a.AsyncRequestWorkerFactor > 16 {
		return invalid("apache.async_request_worker_factor", "%d is outside 1-16", a.AsyncRequestWorkerFactor)
	}
	if a.MaxConnectionsPerChild < 0 {
		return invalid("apache.max_connections_per_child", "must not be negative")
	}
	if a.MaxKeepAliveRequests < 1 || a.MaxKeepAliveRequests > 1_000_000 {
		return invalid("apache.max_keepalive_requests", "%d is outside 1-1000000", a.MaxKeepAliveRequests)
	}
	if a.KeepAliveTimeoutSeconds < 0 || a.KeepAliveTimeoutSeconds > 86400 {
		return invalid("apache.keepalive_timeout", "%d is outside 0-86400", a.KeepAliveTimeoutSeconds)
	}
	if a.ListenBacklog < 1 || a.ListenBacklog > maxBacklog {
		return invalid("apache.listen_backlog", "%d is outside 1-%d", a.ListenBacklog, maxBacklog)
	}
	return nil
}

func (p PHPValues) validate() error {
	if p.Mode != PHPModeStatic && p.Mode != PHPModeDynamic {
		return invalid("php.mode", "%q must be static or dynamic", p.Mode)
	}
	if len(p.Sites) > maxPHPSites {
		return invalid("php.sites", "%d exceeds the %d-site cap", len(p.Sites), maxPHPSites)
	}
	if p.GlobalChildren < 0 || p.GlobalChildren > maxPHPGlobalChild {
		return invalid("php.global_children", "%d is outside 0-%d", p.GlobalChildren, maxPHPGlobalChild)
	}
	if len(p.Sites) == 0 {
		if p.GlobalChildren != 0 {
			return invalid("php.global_children", "must be zero when no PHP sites are active")
		}
	} else {
		if p.GlobalChildren < len(p.Sites) {
			return invalid("php.global_children", "%d is below one child per site (%d)", p.GlobalChildren, len(p.Sites))
		}
		seen := make(map[string]bool, len(p.Sites))
		sum := 0
		for _, site := range p.Sites {
			if err := validateSiteID(site.SiteID); err != nil {
				return err
			}
			if seen[site.SiteID] {
				return invalid("php.sites", "%q is duplicated", site.SiteID)
			}
			seen[site.SiteID] = true
			if site.MaxChildren < 1 || site.MaxChildren > maxPHPPerSite {
				return invalid("php.sites["+site.SiteID+"].max_children", "%d is outside 1-%d", site.MaxChildren, maxPHPPerSite)
			}
			sum += site.MaxChildren
			switch p.Mode {
			case PHPModeStatic:
				if site.StartServers != site.MaxChildren || site.MinSpareServers != site.MaxChildren || site.MaxSpareServers != site.MaxChildren {
					return invalid("php.sites["+site.SiteID+"].spares", "static mode requires all spare values to equal max_children")
				}
			case PHPModeDynamic:
				if site.MinSpareServers < 1 || site.MinSpareServers > site.StartServers ||
					site.StartServers > site.MaxSpareServers || site.MaxSpareServers > site.MaxChildren {
					return invalid("php.sites["+site.SiteID+"].spares", "min %d, start %d, max %d, max_children %d are inconsistent",
						site.MinSpareServers, site.StartServers, site.MaxSpareServers, site.MaxChildren)
				}
			}
		}
		if sum != p.GlobalChildren {
			return invalid("php.global_children", "%d does not equal the sum of per-site max_children %d", p.GlobalChildren, sum)
		}
	}
	if p.ListenBacklog < 1 || p.ListenBacklog > maxBacklog {
		return invalid("php.listen_backlog", "%d is outside 1-%d", p.ListenBacklog, maxBacklog)
	}
	if p.Opcache.MemoryMB < 1 || p.Opcache.MemoryMB > 1024 {
		return invalid("php.opcache.memory_mb", "%d is outside 1-1024", p.Opcache.MemoryMB)
	}
	if p.Opcache.InternedStringsMB < 1 || p.Opcache.InternedStringsMB > p.Opcache.MemoryMB {
		return invalid("php.opcache.interned_strings_mb", "%d must be between 1 and memory_mb %d", p.Opcache.InternedStringsMB, p.Opcache.MemoryMB)
	}
	if p.Opcache.MaxAcceleratedFiles < 1 || p.Opcache.MaxAcceleratedFiles > 1_000_000 {
		return invalid("php.opcache.max_accelerated_files", "%d is outside 1-1000000", p.Opcache.MaxAcceleratedFiles)
	}
	if p.Opcache.RevalidateFreqSeconds < 0 || p.Opcache.RevalidateFreqSeconds > 86400 {
		return invalid("php.opcache.revalidate_freq", "%d is outside 0-86400", p.Opcache.RevalidateFreqSeconds)
	}
	if p.RealpathCacheSizeKB < 1 || p.RealpathCacheSizeKB > 65536 {
		return invalid("php.realpath_cache_size", "%d is outside 1-65536 KB", p.RealpathCacheSizeKB)
	}
	if p.RealpathCacheTTLSeconds < 0 || p.RealpathCacheTTLSeconds > 86400 {
		return invalid("php.realpath_cache_ttl", "%d is outside 0-86400", p.RealpathCacheTTLSeconds)
	}
	return nil
}

func (h HostValues) validate() error {
	if h.FileMax < minFileMax {
		return invalid("host.file_max", "%d is below the %d safety floor", h.FileMax, minFileMax)
	}
	if h.Somaxconn < 1 {
		return invalid("host.somaxconn", "must be greater than zero")
	}
	if h.NetdevMaxBacklog < 1 {
		return invalid("host.netdev_max_backlog", "must be greater than zero")
	}
	if err := validateAccount("host.user", h.User); err != nil {
		return err
	}
	if err := validateAccount("host.group", h.Group); err != nil {
		return err
	}
	if err := h.IPLocalPortRange.validate("host.ip_local_port_range"); err != nil {
		return err
	}
	if h.IPLocalPortRange.Min == 0 || h.IPLocalPortRange.Max == 0 {
		return invalid("host.ip_local_port_range", "must be set")
	}
	if h.NoFileSoft == 0 || h.NoFileHard == 0 {
		return invalid("host.nofile", "limits must be greater than zero")
	}
	if h.NoFileSoft > h.NoFileHard {
		return invalid("host.nofile", "soft %d exceeds hard %d", h.NoFileSoft, h.NoFileHard)
	}
	return nil
}
