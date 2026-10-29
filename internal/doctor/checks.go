package doctor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
)

const (
	// defaultHostsPath is used when Deps.HostsPath is empty.
	defaultHostsPath = "/etc/hosts"
	// dashboardDomain is always served by the dashboard and must resolve.
	dashboardDomain = "devstack.test"
	// minFreeDiskBytes is the minimum required free space. Exactly this value
	// fails because doctor requires strictly more than 1 GiB.
	minFreeDiskBytes uint64 = 1 << 30

	defaultHTTPPort  = 8080
	defaultHTTPSPort = 8443
	defaultUIPort    = 7070

	// defaultVhostTLSDir holds the helper-managed vhost certificates.
	defaultVhostTLSDir = "/etc/devstack/tls"
	// Documented bridge paths, used when the helper reports no path.
	defaultNginxBridge  = "/etc/nginx/conf.d/devstack-vhosts.conf"
	defaultApacheBridge = "/etc/apache2/conf-enabled/devstack-vhosts.conf"
)

var (
	errManagedBlockMissing   = errors.New("managed block is missing")
	errManagedBlockMalformed = errors.New("managed block is malformed")
)

// checkHelper reports whether the privileged helper is reachable and speaks the
// protocol version this build expects.
func (s *Service) checkHelper(ctx context.Context) (Check, helperproto.StatusResponse, bool) {
	check := Check{ID: "helper", Title: "Helper service"}
	if s.deps.Helper == nil {
		check.Status = StatusFail
		check.Detail = "the privileged helper client is not configured"
		check.Remediation = "Run `devstack init` to install and start the helper."
		return check, helperproto.StatusResponse{}, false
	}

	status, err := s.deps.Helper.Status(ctx)
	if err != nil {
		check.Status = StatusFail
		check.Detail = "the privileged helper is not reachable: " + errorDetail(err)
		check.Remediation = "Run `devstack init` to install and start the helper."
		return check, helperproto.StatusResponse{}, false
	}

	version := status.Version
	if strings.TrimSpace(version) == "" {
		version = "unknown"
	}
	ca := "the local CA is not initialized"
	if status.CAFingerprint != "" {
		ca = "local CA " + shortFingerprint(status.CAFingerprint)
	}

	if status.Version != helperproto.Version {
		check.Status = StatusWarn
		check.Detail = fmt.Sprintf("the helper speaks protocol %q but this build requires %q; %s", version, helperproto.Version, ca)
		check.Remediation = "Run `devstack init` to reinstall matching helper and daemon versions."
		return check, status, true
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("devstack-helper protocol %s is responding; %s", version, ca)
	return check, status, true
}

// checkCA verifies that the helper has created its local certificate authority.
// When the helper itself is unavailable the helper check already fails, so the
// CA state is unknown and reported as skipped rather than as a second failure.
func (s *Service) checkCA(status helperproto.StatusResponse, helperOK bool) Check {
	check := Check{ID: "ca", Title: "Local certificate authority"}
	if !helperOK {
		check.Status = StatusSkipped
		check.Detail = "the helper is unavailable, so the local CA cannot be inspected"
		check.Remediation = "No action is required until the helper is reachable."
		return check
	}
	if strings.TrimSpace(status.CAFingerprint) == "" {
		check.Status = StatusFail
		check.Detail = "the helper has no local certificate authority"
		check.Remediation = "Run `devstack init` to create the local CA."
		return check
	}

	check.Status = StatusOK
	check.Detail = "local CA " + shortFingerprint(status.CAFingerprint) + " is available"
	return check
}

// checkHosts compares the managed block in HostsPath with the registered site
// domains plus the dashboard domain.
func (s *Service) checkHosts(expected []string, expectedErr error) Check {
	check := Check{ID: "hosts", Title: "Hosts entries"}
	if expectedErr != nil {
		check.Status = StatusFail
		check.Detail = "cannot determine the registered domains: " + errorDetail(expectedErr)
		check.Remediation = "Run `devstack init` to recreate the devstack state, then rerun `devstack doctor`."
		return check
	}

	path := s.hostsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("cannot read %s: %v", path, err)
		check.Remediation = "Run `devstack doctor --repair` so the managed hosts block can be written."
		return check
	}

	actual, err := parseManagedHosts(string(data))
	if err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("the managed block in %s is unusable: %v", path, err)
		if errors.Is(err, errManagedBlockMalformed) {
			check.Remediation = fmt.Sprintf("Remove the duplicated or unbalanced %s block from %s, then rerun `devstack doctor`.", hosts.BeginMarker, path)
		} else {
			check.Remediation = "Run `devstack doctor --repair` to add the managed hosts block."
		}
		return check
	}

	missing, extra := diffDomains(expected, actual)
	if len(missing) != 0 || len(extra) != 0 {
		var problems []string
		if len(missing) != 0 {
			problems = append(problems, "missing "+summarizeList(missing))
		}
		if len(extra) != 0 {
			problems = append(problems, "unexpected "+summarizeList(extra))
		}
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("the managed block in %s does not match the registered domains (%s)", path, strings.Join(problems, "; "))
		check.Remediation = "Run `devstack doctor --repair` to resync the hosts block."
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%d registered domains match the managed block in %s", len(expected), path)
	return check
}

// parseManagedHosts extracts the domains from a single, complete managed block.
// Duplicate or unbalanced markers are treated as malformed rather than
// silently repaired, because they usually mean a concurrent writer or a manual
// edit that should be surfaced.
func parseManagedHosts(content string) ([]string, error) {
	begins, ends, begin, end := 0, 0, -1, -1
	for i, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case hosts.BeginMarker:
			begins++
			if begin < 0 {
				begin = i
			}
		case hosts.EndMarker:
			ends++
			if end < 0 {
				end = i
			}
		}
	}
	if begins == 0 && ends == 0 {
		return nil, errManagedBlockMissing
	}
	if begins != 1 || ends != 1 || begin > end {
		return nil, fmt.Errorf("%w: expected exactly one begin and one end marker, found %d begin and %d end", errManagedBlockMalformed, begins, ends)
	}

	domains, found := hosts.Parse(content)
	if !found {
		return nil, fmt.Errorf("%w: incomplete managed block", errManagedBlockMalformed)
	}
	return domains, nil
}

// hostsPath returns the configured hosts file or the system default.
func (s *Service) hostsPath() string {
	path := s.deps.HostsPath
	if strings.TrimSpace(path) == "" {
		if runtime.GOOS == "windows" || s.deps.Platform == "windows" {
			if root := os.Getenv("SystemRoot"); root != "" {
				return filepath.Join(root, "System32", "drivers", "etc", "hosts")
			}
			return `C:\Windows\System32\drivers\etc\hosts`
		}
		return defaultHostsPath
	}
	return path
}

// hostsBlockMalformed reports whether the hosts file contains duplicated or
// unbalanced markers. hosts.Render cannot rewrite those blocks, so Repair must
// leave them for the user to fix manually. A missing block is not malformed: it
// can be appended safely.
func (s *Service) hostsBlockMalformed() bool {
	data, err := os.ReadFile(s.hostsPath())
	if err != nil {
		return false
	}
	_, err = parseManagedHosts(string(data))
	return errors.Is(err, errManagedBlockMalformed)
}

// diffDomains compares domain lists regardless of order and returns the
// expected entries that are absent and the entered entries that are unexpected.
func diffDomains(expected, actual []string) (missing, extra []string) {
	actualSet := make(map[string]struct{}, len(actual))
	for _, domain := range actual {
		actualSet[normalizeDomain(domain)] = struct{}{}
	}
	expectedSet := make(map[string]struct{}, len(expected))
	for _, domain := range expected {
		expectedSet[normalizeDomain(domain)] = struct{}{}
	}

	missingSet := map[string]struct{}{}
	for _, domain := range expected {
		domain = normalizeDomain(domain)
		if _, ok := actualSet[domain]; !ok {
			missingSet[domain] = struct{}{}
		}
	}
	extraSet := map[string]struct{}{}
	for _, domain := range actual {
		domain = normalizeDomain(domain)
		if _, ok := expectedSet[domain]; !ok {
			extraSet[domain] = struct{}{}
		}
	}

	for domain := range missingSet {
		missing = append(missing, domain)
	}
	for domain := range extraSet {
		extra = append(extra, domain)
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// checkTrust reports whether the helper CA is present in every detected trust
// store. A machine with no browser profiles is a warning, not a failure.
func (s *Service) checkTrust(ctx context.Context, caFingerprint string) Check {
	check := Check{ID: "trust", Title: "Browser trust"}
	if s.deps.Trust == nil {
		check.Status = StatusSkipped
		check.Detail = "trust status is not configured"
		check.Remediation = "No action is required."
		return check
	}
	if strings.TrimSpace(s.deps.Home) == "" {
		check.Status = StatusSkipped
		check.Detail = "the user home is unknown, so browser trust stores cannot be checked"
		check.Remediation = "Run the daemon as the user whose browser profiles should be trusted."
		return check
	}

	targets, err := s.deps.Trust.TrustStatus(ctx, s.deps.Home)
	if err != nil {
		check.Status = StatusFail
		check.Detail = "cannot read trust status: " + errorDetail(err)
		check.Remediation = "Run `devstack trust repair`, then rerun `devstack doctor`."
		return check
	}
	if len(targets) == 0 {
		check.Status = StatusWarn
		check.Detail = "the helper reported no trust targets"
		check.Remediation = "Run `devstack trust repair` to install the local CA."
		return check
	}

	var failed []string
	installedBrowsers := 0
	for _, target := range targets {
		if !target.OK {
			failed = append(failed, fmt.Sprintf("%s (%s)", target.Name, target.Detail))
		}
		if !strings.EqualFold(strings.TrimSpace(target.Name), "OS") &&
			target.OK &&
			!strings.Contains(strings.ToLower(target.Detail), "not installed") {
			installedBrowsers++
		}
	}

	ca := "the local CA"
	if caFingerprint != "" {
		ca = "local CA " + shortFingerprint(caFingerprint)
	}
	if len(failed) != 0 {
		check.Status = StatusFail
		check.Detail = ca + " is not trusted by " + summarizeList(failed)
		check.Remediation = "Run `devstack trust repair`, then restart the affected browsers."
		return check
	}

	if installedBrowsers == 0 {
		check.Status = StatusWarn
		check.Detail = ca + " is in the OS trust store, but no installed browser trust targets were detected"
		check.Remediation = "Install Chrome or Firefox, then run `devstack trust repair`."
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%s is trusted by the OS and %d browser profiles", ca, installedBrowsers)
	return check
}

type portSpec struct {
	port  int
	owner string
	// free inverts the expectation: external mode expects the built-in proxy
	// ports to be unused because nginx/Apache owns 80/443.
	free bool
}

// checkPorts diagnoses the expected listeners rather than requiring free
// ports. The helper normally owns 80/443 and the daemon owns the dashboard and
// proxy ports, so an occupied port is healthy. In external mode the server
// owns 80/443 and the built-in proxy ports must be free. Free ports,
// permission-denied probes, and unexpected probe failures are warnings, never
// failures.
func (s *Service) checkPorts() Check {
	check := Check{ID: "ports", Title: "Ports"}
	if s.deps.Ports == nil {
		check.Status = StatusSkipped
		check.Detail = "port checking is not configured"
		check.Remediation = "No action is required."
		return check
	}

	external := s.effectiveServingMode() != config.ServingModeBuiltin
	var (
		owned    []string
		free     []string
		warnings []string
	)
	for _, spec := range s.portSpecs() {
		addr := ":" + strconv.Itoa(spec.port)
		err := s.deps.Ports.Listenable(addr)
		switch {
		case spec.free:
			switch {
			case err == nil:
				// A wildcard bind can succeed while another process owns a
				// specific loopback address (Go sets SO_REUSEADDR on Unix).
				// Ask the port directly before calling it free.
				if s.portResponding(spec.port) {
					warnings = append(warnings, fmt.Sprintf("port %d is in use; external mode expects the built-in proxy ports to be free", spec.port))
				} else {
					free = append(free, strconv.Itoa(spec.port))
				}
			case isAddrInUse(err) || s.portResponding(spec.port):
				warnings = append(warnings, fmt.Sprintf("port %d is in use; external mode expects the built-in proxy ports to be free", spec.port))
			case isProbePermission(err):
				warnings = append(warnings, fmt.Sprintf("cannot probe port %d as this user; external mode expects it to be free", spec.port))
			default:
				warnings = append(warnings, fmt.Sprintf("could not probe port %d (%v); external mode expects it to be free", spec.port, err))
			}
		case err == nil:
			if s.portResponding(spec.port) {
				owned = append(owned, fmt.Sprintf("%d (%s)", spec.port, spec.owner))
			} else {
				warnings = append(warnings, fmt.Sprintf("port %d is free; expected owner is %s", spec.port, spec.owner))
			}
		case isProbePermission(err):
			warnings = append(warnings, fmt.Sprintf("cannot probe port %d as this user; expected owner is %s", spec.port, spec.owner))
		case isAddrInUse(err) || s.isPortInUseByDevstack(spec, err):
			owned = append(owned, fmt.Sprintf("%d (%s)", spec.port, spec.owner))
		default:
			warnings = append(warnings, fmt.Sprintf("could not probe port %d (%v); expected owner is %s", spec.port, err, spec.owner))
		}
	}

	if len(warnings) != 0 {
		check.Status = StatusWarn
		check.Detail = strings.Join(warnings, "; ")
		check.Remediation = "No action is required while devstack is running. If it is not running, start it with `devstack init`."
		return check
	}

	check.Status = StatusOK
	check.Detail = "ports " + strings.Join(owned, ", ") + " are owned by the expected services"
	if external && len(free) != 0 {
		check.Detail += "; built-in proxy ports " + strings.Join(free, ", ") + " are free"
	}
	return check
}

// portSpecs returns the ports doctor inspects, honouring configured proxy and
// dashboard ports while always checking the privileged 80/443 listeners. In
// external mode 80/443 belong to the selected server and the proxy ports are
// expected to be free.
func (s *Service) portSpecs() []portSpec {
	cfg := s.deps.Config
	httpPort := cfg.HTTPPort
	if httpPort <= 0 {
		httpPort = defaultHTTPPort
	}
	httpsPort := cfg.HTTPSPort
	if httpsPort <= 0 {
		httpsPort = defaultHTTPSPort
	}
	uiPort := cfg.UIPort
	if uiPort <= 0 {
		uiPort = defaultUIPort
	}

	mode := s.effectiveServingMode()
	external := mode != config.ServingModeBuiltin
	privilegedOwner := "the helper forwarder"
	if external {
		privilegedOwner = "the " + mode + " server"
	}

	specs := make([]portSpec, 0, 5)
	seen := make(map[int]struct{}, 5)
	add := func(port int, owner string, free bool) {
		if port <= 0 || port > 65535 {
			return
		}
		if _, ok := seen[port]; ok {
			return
		}
		seen[port] = struct{}{}
		specs = append(specs, portSpec{port: port, owner: owner, free: free})
	}
	add(80, privilegedOwner, false)
	add(443, privilegedOwner, false)
	add(uiPort, "the devstack dashboard", false)
	if external {
		add(httpPort, "the built-in proxy", true)
		add(httpsPort, "the built-in proxy", true)
	} else {
		add(httpPort, "the devstack proxy", false)
		add(httpsPort, "the devstack proxy", false)
	}
	return specs
}

// checkRuntimes verifies every installed PHP and Node build against its
// manifest.
func (s *Service) checkRuntimes(ctx context.Context) Check {
	check := Check{ID: "runtimes", Title: "Runtimes"}
	if s.deps.Runtimes == nil {
		check.Status = StatusSkipped
		check.Detail = "runtime verification is not configured"
		check.Remediation = "No action is required."
		return check
	}
	if err := s.deps.Runtimes.VerifyAll(ctx); err != nil {
		check.Status = StatusFail
		check.Detail = "installed runtimes failed integrity verification: " + errorDetail(err)
		check.Remediation = "Reinstall the affected runtime with `devstack runtime install <id>`, then rerun `devstack doctor`."
		return check
	}
	check.Status = StatusOK
	check.Detail = "all installed runtimes passed integrity verification"
	return check
}

// checkDisk ensures there is enough space for runtimes, certificates, logs, and
// the SQLite database.
func (s *Service) checkDisk() Check {
	check := Check{ID: "disk", Title: "Disk space"}
	if s.deps.Disk == nil {
		check.Status = StatusSkipped
		check.Detail = "disk checking is not configured"
		check.Remediation = "No action is required."
		return check
	}

	path := s.deps.Config.Home
	if strings.TrimSpace(path) == "" {
		path = s.deps.Home
	}
	if strings.TrimSpace(path) == "" {
		path = "."
	}

	free, err := s.deps.Disk.FreeBytes(path)
	if err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("cannot determine free space at %s: %v", path, err)
		check.Remediation = "Check that the devstack home exists and is readable, then rerun `devstack doctor`."
		return check
	}
	if free <= minFreeDiskBytes {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("only %s free at %s; more than 1 GiB is required", humanBytes(free), path)
		check.Remediation = "Free disk space, then rerun `devstack doctor`."
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%s free at %s", humanBytes(free), path)
	return check
}

func (s *Service) windowsChecks(ctx context.Context) []Check {
	platform := strings.TrimSpace(s.deps.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "windows" {
		return []Check{{
			ID: "wsl", Title: "WSL backend", Status: StatusSkipped,
			Detail:      "WSL diagnostics apply only to the Windows host; native Linux sites continue to use the local backend.",
			Remediation: "No action is required.",
		}}
	}
	distro := strings.TrimSpace(s.deps.Config.WSLDistro)
	if distro == "" {
		distro = "devstack"
	}
	if s.deps.Windows == nil {
		return []Check{
			skippedWindowsCheck("wsl", "Managed WSL distro", "the Windows diagnostics provider is not configured"),
			skippedWindowsCheck("agent", "WSL agent", "agent diagnostics are unavailable"),
			skippedWindowsCheck("docker", "WSL Docker Engine", "Docker diagnostics are unavailable"),
			skippedWindowsCheck("wsl-localhost-forwarding", "WSL localhost forwarding", "the forwarding probe is unavailable"),
			skippedWindowsCheck("wsl-ports", "WSL backend ports", "agent port diagnostics are unavailable"),
		}
	}
	forwardPort := s.deps.Config.HTTPPort
	if forwardPort <= 0 {
		forwardPort = defaultHTTPPort
	}
	return []Check{
		windowsDiagnosticCheck("wsl", "Managed WSL distro", "WSL_UNAVAILABLE", "Start or repair the managed WSL distro, then rerun `devstack doctor`.", s.deps.Windows.CheckWSL(ctx, distro), "managed WSL distro "+distro+" is available"),
		windowsDiagnosticCheck("agent", "WSL agent", "AGENT_UNAUTHENTICATED", "Run `devstack init` to restore the authenticated WSL agent connection.", s.deps.Windows.CheckAgent(ctx, distro), "the WSL agent is authenticated and responding"),
		windowsDiagnosticCheck("docker", "WSL Docker Engine", "DOCKER_UNAVAILABLE", "Start or repair Docker Engine inside the managed WSL distro.", s.deps.Windows.CheckDocker(ctx, distro), "Docker Engine is responding inside WSL"),
		windowsDiagnosticCheck("wsl-localhost-forwarding", "WSL localhost forwarding", "WSL_LOCALHOST_FORWARDING_UNAVAILABLE", "Enable WSL localhost forwarding in `%UserProfile%\\.wslconfig`, then run `wsl --shutdown`.", s.deps.Windows.CheckLocalhostForwarding(ctx, distro, forwardPort), "WSL localhost forwarding reaches the Windows daemon proxy"),
		windowsDiagnosticCheck("wsl-ports", "WSL backend ports", "WSL_PORTS_UNAVAILABLE", "Stop the conflicting WSL listener or restart the site agent to release stale backend ports.", s.deps.Windows.CheckPorts(ctx, distro), "the WSL agent can allocate site backend ports"),
	}
}

func skippedWindowsCheck(id, title, detail string) Check {
	return Check{ID: id, Title: title, Status: StatusSkipped, Detail: detail, Remediation: "No action is available until Windows diagnostics are configured."}
}

func windowsDiagnosticCheck(id, title, code, remediation string, err error, okDetail string) Check {
	check := Check{ID: id, Title: title, Remediation: remediation}
	if err != nil {
		check.Status = StatusFail
		check.Detail = code + ": " + errorDetail(err)
		return check
	}
	check.Status = StatusOK
	check.Detail = okDetail
	check.Remediation = ""
	return check
}

func (s *Service) portResponding(port int) bool {
	if s.deps.PortResponding != nil {
		return s.deps.PortResponding(port)
	}
	return s.isPortResponding(port)
}

func (s *Service) isPortResponding(port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true
	}
	client := &http.Client{
		Timeout: 200 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	if resp, err := client.Get("http://" + addr); err == nil {
		_ = resp.Body.Close()
		return true
	}
	if resp, err := client.Get("https://" + addr); err == nil {
		_ = resp.Body.Close()
		return true
	}
	return false
}

func (s *Service) isPortInUseByDevstack(spec portSpec, probeErr error) bool {
	if spec.free {
		return false
	}
	switch spec.port {
	case 80, 443, 7070, 8080, 8443, s.deps.Config.UIPort, s.deps.Config.HTTPPort, s.deps.Config.HTTPSPort:
	default:
		return false
	}
	if isAddrInUse(probeErr) {
		return true
	}
	if isProbePermission(probeErr) && s.portResponding(spec.port) {
		return true
	}
	return false
}

// isAddrInUse recognises bind failures caused by an existing listener. The
// proxy owns 8080/8443, so this is normal while the daemon is running.
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == 10048 || errno == syscall.EADDRINUSE {
			return true
		}
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "eaddrinuse") ||
		strings.Contains(message, "only one usage of each socket address") ||
		strings.Contains(message, "wsaeaddrinuse")
}

// isProbePermission recognises probes that this user is not allowed to perform.
// Ports below 1024 commonly return EACCES to an unprivileged daemon.
func isProbePermission(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "permission denied") || strings.Contains(message, "operation not permitted")
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

func shortFingerprint(fingerprint string) string {
	value := strings.TrimSpace(fingerprint)
	if value == "" {
		return "unknown"
	}
	if len(value) > 16 {
		return value[:16] + "..."
	}
	return value
}

func summarizeList(values []string) string {
	const limit = 8
	if len(values) <= limit {
		return strings.Join(values, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(values[:limit], ", "), len(values)-limit)
}

func humanBytes(bytes uint64) string {
	const (
		mib = uint64(1 << 20)
		gib = uint64(1 << 30)
	)
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
	case bytes >= mib:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(mib))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// errorDetail trims an error for display in a check while keeping it useful.
func errorDetail(err error) string {
	message := strings.TrimSpace(err.Error())
	const limit = 300
	if len(message) > limit {
		return message[:limit] + "..."
	}
	return message
}

// checkVhosts reports the external serving mode, server, bridge, config-test,
// release freshness, and TLS material state. Builtin mode skips every external
// vhost check; an external mode without a vhost status fails only the
// serving-mode check and skips the rest because their inputs are unavailable.
func (s *Service) checkVhosts(ctx context.Context) []Check {
	mode := s.effectiveServingMode()
	checks := []Check{
		{ID: "serving-mode", Title: "Serving mode"},
		{ID: "vhost-server", Title: "Vhost server"},
		{ID: "vhost-include", Title: "Vhost include"},
		{ID: "vhost-config", Title: "Vhost config test"},
		{ID: "vhost-freshness", Title: "Vhost release freshness"},
		{ID: "vhost-tls", Title: "Vhost TLS permissions"},
		{ID: "vhost-default-server", Title: "Vhost default server"},
	}
	skipRest := func(from int, detail string) {
		for i := from; i < len(checks); i++ {
			checks[i].Status = StatusSkipped
			checks[i].Detail = detail
			checks[i].Remediation = "No action is required."
		}
	}

	if mode == config.ServingModeBuiltin {
		checks[0].Status = StatusOK
		checks[0].Detail = "builtin mode: the built-in proxy serves sites on ports 80 and 443"
		checks[0].Remediation = "No action is required."
		skipRest(1, "the built-in proxy is serving sites, so external vhosts are not in use")
		return checks
	}
	if !config.ValidServingMode(mode) {
		checks[0].Status = StatusFail
		checks[0].Detail = fmt.Sprintf("config.yaml selects unsupported serving mode %q", mode)
		checks[0].Remediation = "Set serving_mode to builtin, nginx, or apache in config.yaml, then restart the daemon."
		skipRest(1, "the configured serving mode is invalid")
		return checks
	}
	if s.deps.Vhosts == nil {
		checks[0].Status = StatusFail
		checks[0].Detail = "vhost status is not configured"
		checks[0].Remediation = "Run `devstack init` to restart the daemon with vhost support, then rerun `devstack doctor`."
		skipRest(1, "vhost status is not configured")
		return checks
	}

	status, err := s.deps.Vhosts.VhostStatus(ctx)
	if err != nil {
		checks[0].Status = StatusFail
		checks[0].Detail = fmt.Sprintf("cannot read the helper's vhost status for %s mode: %s", mode, errorDetail(err))
		checks[0].Remediation = "Run `devstack vhost status` to inspect the helper, then rerun `devstack doctor`."
		skipRest(1, "the helper's vhost status could not be read")
		return checks
	}

	helperMode := strings.ToLower(strings.TrimSpace(status.Mode))
	if helperMode == "" {
		helperMode = config.ServingModeBuiltin
	}
	if helperMode == mode {
		checks[0].Status = StatusOK
		checks[0].Detail = fmt.Sprintf("config.yaml and the helper both select %s", mode)
	} else {
		checks[0].Status = StatusFail
		checks[0].Detail = fmt.Sprintf("config.yaml selects %s but the helper reports %s", mode, helperMode)
		checks[0].Remediation = fmt.Sprintf("Run `devstack vhost use %s` to reconcile the helper, then rerun `devstack doctor`.", mode)
	}

	server := strings.ToLower(strings.TrimSpace(status.Server))
	if server == "" {
		server = mode
	}
	version := strings.TrimSpace(status.ServerVersion)
	switch {
	case version == "":
		checks[1].Status = StatusFail
		checks[1].Detail = fmt.Sprintf("%s is selected but its version could not be detected", server)
		checks[1].Remediation = fmt.Sprintf("Install %s and start its service, then run `devstack vhost sync`.", server)
	case !status.ServiceActive:
		checks[1].Status = StatusFail
		checks[1].Detail = fmt.Sprintf("%s %s is installed but its systemd service is not active", server, version)
		checks[1].Remediation = fmt.Sprintf("Start the %s service (`%s`), then run `devstack vhost sync`.",
			server, vhostServiceStartCommand(mode))
	default:
		checks[1].Status = StatusOK
		checks[1].Detail = fmt.Sprintf("%s %s is available and its service is active", server, version)
	}

	bridge := vhostBridgePath(status, mode)
	if status.IncludePresent {
		checks[2].Status = StatusOK
		checks[2].Detail = "the server includes the devstack bridge " + bridge
	} else {
		checks[2].Status = StatusFail
		checks[2].Detail = "the server does not include the devstack bridge " + bridge
		checks[2].Remediation = "Add the devstack bridge to the server's include directory (see `devstack vhost path`), then run `devstack vhost sync`."
	}

	if status.ConfigOK {
		checks[3].Status = StatusOK
		checks[3].Detail = "the server configuration test passed"
	} else {
		checks[3].Status = StatusFail
		checks[3].Detail = "the server configuration test failed"
		if lastError := strings.TrimSpace(status.LastError); lastError != "" {
			checks[3].Detail += ": " + errorDetail(errors.New(lastError))
		}
		checks[3].Remediation = "Fix the reported server configuration error, then run `devstack vhost sync`."
	}

	checks[4] = s.checkVhostFreshness(ctx, status.LastSync)
	checks[5] = s.checkVhostTLS(status)
	checks[6] = checkVhostDefaultServer(status)
	return checks
}

func vhostServiceStartCommand(mode string) string {
	if mode == config.ServingModeApache {
		return "sudo systemctl start apache2 # or httpd on Fedora/RHEL"
	}
	return "sudo systemctl start nginx"
}

func checkVhostDefaultServer(status helperproto.VhostStatusResponse) Check {
	check := Check{ID: "vhost-default-server", Title: "Vhost default server"}
	if status.DefaultServerConflict {
		check.Status = StatusWarn
		check.Detail = "another server configuration already declares default_server on the devstack listen ports; the devstack 404 vhost is installed without the marker, so unknown hosts are answered by the distribution default"
		check.Remediation = "Remove default_server from the conflicting vhost (usually the distribution's default site) and run `devstack vhost sync`; otherwise accept the distribution's 404 page for unknown hosts."
		return check
	}
	check.Status = StatusOK
	check.Detail = "no foreign default_server conflicts with the devstack vhosts"
	return check
}

// effectiveServingMode prefers the daemon's live mode so a runtime switch is
// reflected without a restart, then falls back to the startup configuration.
func (s *Service) effectiveServingMode() string {
	if s.deps.Vhosts != nil {
		if mode := strings.ToLower(strings.TrimSpace(s.deps.Vhosts.ServingMode())); mode != "" {
			return mode
		}
	}
	mode := strings.ToLower(strings.TrimSpace(s.deps.Config.ServingMode))
	if mode == "" {
		return config.ServingModeBuiltin
	}
	return mode
}

// checkVhostFreshness compares the last successful helper sync with the newest
// registered site update. Missing inputs are skipped rather than guessed.
func (s *Service) checkVhostFreshness(ctx context.Context, lastSync time.Time) Check {
	check := Check{ID: "vhost-freshness", Title: "Vhost release freshness"}
	if s.deps.Store == nil {
		check.Status = StatusSkipped
		check.Detail = "the site store is not configured, so release freshness cannot be checked"
		check.Remediation = "No action is required."
		return check
	}
	sites, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		check.Status = StatusSkipped
		check.Detail = "cannot list registered sites: " + errorDetail(err)
		check.Remediation = "No action is required."
		return check
	}

	var newest time.Time
	for _, site := range sites {
		if site.UpdatedAt.After(newest) {
			newest = site.UpdatedAt
		}
	}
	if newest.IsZero() {
		check.Status = StatusSkipped
		check.Detail = "no registered site has an update timestamp"
		check.Remediation = "No action is required."
		return check
	}
	if lastSync.IsZero() {
		check.Status = StatusSkipped
		check.Detail = "the helper has not recorded a vhost sync timestamp"
		check.Remediation = "Run `devstack vhost sync` after the external mode is active."
		return check
	}

	if lastSync.Before(newest) {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("the vhost release is older than site state (last sync %s, newest site update %s)",
			vhostTime(lastSync), vhostTime(newest))
		check.Remediation = "Run `devstack vhost sync` to publish the latest site state."
		return check
	}
	check.Status = StatusOK
	check.Detail = fmt.Sprintf("the vhost release is newer than the newest site update (%s)", vhostTime(newest))
	return check
}

// checkVhostTLS verifies the metadata reported by the root helper. Key and
// certificate contents are never read, and doctor never opens the TLS
// directory itself because it is root-only in a healthy install.
func (s *Service) checkVhostTLS(status helperproto.VhostStatusResponse) Check {
	check := Check{ID: "vhost-tls", Title: "Vhost TLS permissions"}
	if strings.TrimSpace(status.CurrentRelease) == "" {
		check.Status = StatusSkipped
		check.Detail = "no vhost release is installed, so TLS material cannot be inspected"
		check.Remediation = "Run `devstack vhost sync` after an external mode is active."
		return check
	}

	tlsDir := strings.TrimSpace(status.TLSDir)
	if tlsDir == "" {
		tlsDir = defaultVhostTLSDir
	}
	if status.TLSDirMode == 0 {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("the helper reports no TLS directory; expected %s with mode 0700", tlsDir)
		check.Remediation = fmt.Sprintf("Run `devstack vhost sync` to recreate %s, then `chmod 700 %s` if needed and rerun `devstack doctor`.", tlsDir, tlsDir)
		return check
	}

	var (
		bad       []string
		dirBad    bool
		keyBad    bool
		certBad   bool
		keyFiles  int
		certFiles int
	)
	if status.TLSDirMode != 0o700 {
		dirBad = true
		bad = append(bad, fmt.Sprintf("%s has mode %04o (want 0700)", tlsDir, status.TLSDirMode))
	}

	for _, file := range status.TLSFiles {
		var want uint32
		switch {
		case strings.HasSuffix(file.Name, ".key"):
			want = 0o600
			keyFiles++
		case strings.HasSuffix(file.Name, ".crt"):
			want = 0o644
			certFiles++
		default:
			continue
		}
		path := tlsDir + "/" + file.Name
		badFile := false
		if !file.Regular {
			bad = append(bad, path+" is not a regular file")
			badFile = true
		} else if file.Mode != want {
			bad = append(bad, fmt.Sprintf("%s has mode %04o (want %04o)", path, file.Mode, want))
			badFile = true
		}
		if badFile {
			if want == 0o600 {
				keyBad = true
			} else {
				certBad = true
			}
		}
	}

	if len(bad) != 0 {
		check.Status = StatusFail
		check.Detail = "TLS material is unsafe: " + strings.Join(bad, "; ")
		var fixes []string
		if dirBad {
			fixes = append(fixes, fmt.Sprintf("`chmod 700 %s`", tlsDir))
		}
		if keyBad {
			fixes = append(fixes, fmt.Sprintf("`chmod 600 %s/*.key`", tlsDir))
		}
		if certBad {
			fixes = append(fixes, fmt.Sprintf("`chmod 644 %s/*.crt`", tlsDir))
		}
		check.Remediation = "Run " + strings.Join(fixes, ", ") + ", then rerun `devstack doctor`."
		return check
	}

	if certFiles == 0 {
		check.Status = StatusWarn
		check.Detail = fmt.Sprintf("no TLS certificate files are present in %s", tlsDir)
		check.Remediation = "Run `devstack vhost sync` to install certificates for TLS sites."
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%d key(s) are 0600 and %d certificate(s) are 0644 in %s", keyFiles, certFiles, tlsDir)
	return check
}

func vhostBridgePath(status helperproto.VhostStatusResponse, mode string) string {
	if bridge := strings.TrimSpace(status.BridgePath); bridge != "" {
		return bridge
	}
	if mode == config.ServingModeApache {
		return defaultApacheBridge
	}
	return defaultNginxBridge
}

func vhostTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}
