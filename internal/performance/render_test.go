package performance

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderNginxSeparateIncludesAreDeterministic(t *testing.T) {
	v := mustCompute(t, highResources(), Throughput, "site")

	first, err := RenderNginx(v)
	if err != nil {
		t.Fatalf("RenderNginx: %v", err)
	}
	second, err := RenderNginx(v)
	if err != nil {
		t.Fatalf("RenderNginx again: %v", err)
	}
	if !bytes.Equal(first.Main, second.Main) || !bytes.Equal(first.Events, second.Events) || !bytes.Equal(first.HTTP, second.HTTP) {
		t.Fatal("nginx renders are not deterministic")
	}

	mainText := string(first.Main)
	for _, want := range []string{
		"worker_processes 9;",
		"worker_rlimit_nofile 17408;",
		"worker_cpu_affinity auto;",
		"pcre_jit on;",
	} {
		if !strings.Contains(mainText, want) {
			t.Fatalf("main include missing %q:\n%s", want, mainText)
		}
	}
	if strings.Contains(mainText, "use epoll") {
		t.Fatalf("main include must not select epoll explicitly:\n%s", mainText)
	}
	if strings.Contains(mainText, "events {") || strings.Contains(mainText, "http {") {
		t.Fatalf("main include must not contain block wrappers:\n%s", mainText)
	}

	eventsText := string(first.Events)
	if !strings.Contains(eventsText, "worker_connections 8192;") || !strings.Contains(eventsText, "multi_accept on;") {
		t.Fatalf("events include missing tuning:\n%s", eventsText)
	}
	if strings.Contains(eventsText, "events {") {
		t.Fatalf("events include must be a snippet for the events block:\n%s", eventsText)
	}
	if strings.Contains(eventsText, "use epoll") {
		t.Fatalf("events include must not select epoll explicitly:\n%s", eventsText)
	}

	httpText := string(first.HTTP)
	for _, want := range []string{
		"sendfile on;",
		"tcp_nopush on;",
		"tcp_nodelay on;",
		"keepalive_timeout 30s;",
		"keepalive_requests 10000;",
		"open_file_cache max=20000 inactive=60s;",
		"open_file_cache_valid 30s;",
		"open_file_cache_min_uses 2;",
		"open_file_cache_errors on;",
		"gzip off;",
	} {
		if !strings.Contains(httpText, want) {
			t.Fatalf("http include missing %q:\n%s", want, httpText)
		}
	}
	if strings.Contains(httpText, "http {") {
		t.Fatalf("http include must be a snippet for the http block:\n%s", httpText)
	}
	if strings.Contains(httpText, "http2") {
		t.Fatalf("HTTP/2 listener syntax belongs to the vhost renderer:\n%s", httpText)
	}
}

func TestRenderNginxBalancedGzip(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "site")
	includes, err := RenderNginx(v)
	if err != nil {
		t.Fatal(err)
	}
	text := string(includes.HTTP)
	for _, want := range []string{"gzip on;", "gzip_comp_level 2;"} {
		if !strings.Contains(text, want) {
			t.Fatalf("balanced http include missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "gzip_comp_level 9") {
		t.Fatalf("balanced profile must not default to gzip level 9:\n%s", text)
	}
}

func TestRenderApacheDeterministicMPMEvent(t *testing.T) {
	v := mustCompute(t, highResources(), Throughput, "site")
	first, err := RenderApache(v)
	if err != nil {
		t.Fatalf("RenderApache: %v", err)
	}
	second, err := RenderApache(v)
	if err != nil {
		t.Fatalf("RenderApache again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Apache render is not deterministic")
	}

	text := string(first)
	for _, want := range []string{
		"<IfModule mpm_event_module>",
		"ThreadsPerChild 32",
		"ServerLimit 18",
		"MaxRequestWorkers 576",
		"StartServers 9",
		"MinSpareThreads 32",
		"MaxSpareThreads 288",
		"AsyncRequestWorkerFactor 2",
		"MaxConnectionsPerChild 0",
		"KeepAlive On",
		"MaxKeepAliveRequests 10000",
		"KeepAliveTimeout 3",
		"HostnameLookups Off",
		"EnableSendfile On",
		"ListenBacklog 65535",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Apache include missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "prefork") {
		t.Fatalf("Apache include must not reference prefork:\n%s", text)
	}
}

func TestRenderPHPPoolOverrides(t *testing.T) {
	v := mustCompute(t, highResources(), Throughput, "d", "a", "b", "c")
	pool, err := RenderPHPPool(v, "a")
	if err != nil {
		t.Fatalf("RenderPHPPool: %v", err)
	}
	text := string(pool)

	for _, want := range []string{
		"; profile = throughput",
		"; site = a",
		"pm = static",
		"pm.max_children = 9",
		"listen.backlog = 65535",
		"php_admin_flag[opcache.enable] = on",
		"php_admin_value[opcache.memory_consumption] = 72",
		"php_admin_value[opcache.interned_strings_buffer] = 9",
		"php_admin_value[opcache.max_accelerated_files] = 5000",
		"php_admin_flag[opcache.validate_timestamps] = on",
		"php_admin_value[realpath_cache_size] = 4096K",
		"php_admin_value[realpath_cache_ttl] = 600",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("PHP pool override missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "pm.start_servers") || strings.Contains(text, "pm.min_spare_servers") {
		t.Fatalf("static mode must not render dynamic spare settings:\n%s", text)
	}
	if strings.Contains(text, "\n[www]\n") {
		t.Fatalf("pool overrides are a fragment to merge into [www], not a second pool:\n%s", text)
	}

	balanced := mustCompute(t, testResources(), Balanced, "a")
	pool, err = RenderPHPPool(balanced, "a")
	if err != nil {
		t.Fatal(err)
	}
	text = string(pool)
	for _, want := range []string{
		"pm = dynamic",
		"pm.max_children = 16",
		"pm.start_servers = 4",
		"pm.min_spare_servers = 2",
		"pm.max_spare_servers = 8",
		"php_admin_flag[opcache.validate_timestamps] = on",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("balanced PHP pool override missing %q:\n%s", want, text)
		}
	}

	if _, err := RenderPHPPool(v, "missing"); err == nil {
		t.Fatal("RenderPHPPool accepted an unknown site")
	}
}

func TestRenderPHPPoolDisableTimestampValidation(t *testing.T) {
	v, err := Compute(testResources(), Options{
		Profile:                           Balanced,
		PHPSites:                          []string{"a"},
		DisableOpcacheTimestampValidation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := RenderPHPPool(v, "a")
	if err != nil {
		t.Fatal(err)
	}
	text := string(pool)
	if !strings.Contains(text, "php_admin_flag[opcache.validate_timestamps] = off") {
		t.Fatalf("missing disabled validate_timestamps:\n%s", text)
	}
	if !strings.Contains(text, "cache reset") {
		t.Fatalf("disabled validate_timestamps should warn about the required cache reset:\n%s", text)
	}
}

func TestRenderHostTuningAllowlist(t *testing.T) {
	v := mustCompute(t, highResources(), Throughput, "site")
	sysctl, err := RenderSysctl(v)
	if err != nil {
		t.Fatalf("RenderSysctl: %v", err)
	}
	limits, err := RenderLimits(v)
	if err != nil {
		t.Fatalf("RenderLimits: %v", err)
	}

	var keys []string
	for _, line := range strings.Split(string(sysctl), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("sysctl line is not key=value: %q", line)
		}
		keys = append(keys, strings.TrimSpace(key))
	}
	wantKeys := []string{
		"fs.file-max",
		"net.core.somaxconn",
		"net.core.netdev_max_backlog",
		"net.ipv4.ip_local_port_range",
	}
	if len(keys) != len(wantKeys) {
		t.Fatalf("sysctl keys = %v, want only %v", keys, wantKeys)
	}
	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("sysctl keys = %v, want ordered %v", keys, wantKeys)
		}
	}
	for _, forbidden := range []string{"vm.swappiness", "net.ipv4.tcp_tw_reuse", "kernel."} {
		if strings.Contains(string(sysctl), forbidden) {
			t.Fatalf("sysctl output contains non-allowlisted tuning %q:\n%s", forbidden, sysctl)
		}
	}

	limitsText := string(limits)
	for _, want := range []string{"* soft nofile 65536", "* hard nofile 1048576"} {
		if !strings.Contains(limitsText, want) {
			t.Fatalf("limits output missing %q:\n%s", want, limitsText)
		}
	}
	for _, line := range strings.Split(limitsText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "* soft nofile ") && !strings.HasPrefix(line, "* hard nofile ") {
			t.Fatalf("limits output contains unexpected line %q", line)
		}
	}

	if !bytes.Equal(sysctl, mustRenderSysctl(t, v)) {
		t.Fatal("sysctl render is not deterministic")
	}
	if !bytes.Equal(limits, mustRenderLimits(t, v)) {
		t.Fatal("limits render is not deterministic")
	}
}

func mustRenderSysctl(t *testing.T, v Values) []byte {
	t.Helper()
	out, err := RenderSysctl(v)
	if err != nil {
		t.Fatalf("RenderSysctl: %v", err)
	}
	return out
}

func mustRenderLimits(t *testing.T, v Values) []byte {
	t.Helper()
	out, err := RenderLimits(v)
	if err != nil {
		t.Fatalf("RenderLimits: %v", err)
	}
	return out
}

func TestHostTuningNeverLowersExistingValues(t *testing.T) {
	res := lowResources()
	res.Sysctl.FileMax = 8 << 20
	res.Sysctl.Somaxconn = 65535
	res.Sysctl.NetdevMaxBacklog = 65535
	res.Sysctl.IPLocalPortRange = PortRange{Min: 10000, Max: 65535}
	res.NoFileSoft = 200000
	res.NoFileHard = 1 << 20
	v := mustCompute(t, res, Balanced, "site")

	if v.Host.FileMax != 8<<20 {
		t.Fatalf("FileMax = %d, want existing 8 MiB preserved", v.Host.FileMax)
	}
	if v.Host.Somaxconn != 65535 || v.Host.NetdevMaxBacklog != 65535 {
		t.Fatalf("existing backlog caps were lowered: %+v", v.Host)
	}
	if v.Host.IPLocalPortRange.Min != 10000 || v.Host.IPLocalPortRange.Max != 65535 {
		t.Fatalf("port range = %+v, want existing 10000-65535 preserved", v.Host.IPLocalPortRange)
	}
	if v.Host.NoFileSoft != 200000 || v.Host.NoFileHard != 1<<20 {
		t.Fatalf("nofile = %d/%d, want existing 200000/1048576 preserved", v.Host.NoFileSoft, v.Host.NoFileHard)
	}
}
