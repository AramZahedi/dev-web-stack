package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
)

const (
	// defaultHostsPath is used when Deps.HostsPath is empty.
	defaultHostsPath = "/etc/hosts"
	// dashboardDomain is always served by the dashboard and must resolve.
	dashboardDomain = "devstack.test"
	// minFreeDiskBytes is the smallest amount of free space doctor accepts.
	minFreeDiskBytes uint64 = 1 << 30

	defaultHTTPPort  = 8080
	defaultHTTPSPort = 8443
	defaultUIPort    = 7070
)

// checkHelper reports whether the privileged helper is reachable and speaks the
// protocol version this build expects.
func (s *Service) checkHelper(ctx context.Context) (Check, helperproto.StatusResponse) {
	check := Check{ID: "helper", Title: "Helper service"}
	if s.deps.Helper == nil {
		check.Status = StatusFail
		check.Detail = "the privileged helper client is not configured"
		check.Remediation = "Start the devstack helper with `sudo systemctl start devstack-helper`, then restart the daemon."
		return check, helperproto.StatusResponse{}
	}

	status, err := s.deps.Helper.Status(ctx)
	if err != nil {
		check.Status = StatusFail
		check.Detail = "the privileged helper is not reachable: " + errorDetail(err)
		check.Remediation = "Start the helper with `sudo systemctl start devstack-helper`. If it is not installed, run `devstack init`."
		return check, helperproto.StatusResponse{}
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
		check.Remediation = "Restart or reinstall the helper so its protocol version matches the daemon."
		return check, status
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("devstack-helper protocol %s is responding; %s", version, ca)
	return check, status
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

	path := s.deps.HostsPath
	if strings.TrimSpace(path) == "" {
		path = defaultHostsPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("cannot read %s: %v", path, err)
		check.Remediation = "Run `sudo devstack doctor --repair` so the helper can rewrite the managed hosts block."
		return check
	}

	actual, err := parseManagedHosts(string(data))
	if err != nil {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("the managed block in %s is unusable: %v", path, err)
		check.Remediation = "Run `sudo devstack doctor --repair` to rewrite the managed hosts block."
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
		check.Remediation = "Run `sudo devstack doctor --repair` to resync the hosts block."
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
		return nil, errors.New("managed block is missing")
	}
	if begins != 1 || ends != 1 || begin > end {
		return nil, fmt.Errorf("expected exactly one begin and one end marker, found %d begin and %d end", begins, ends)
	}

	domains, found := hosts.Parse(content)
	if !found {
		return nil, errors.New("managed block is incomplete")
	}
	return domains, nil
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
		check.Remediation = "Run `sudo devstack trust repair`, then rerun `devstack doctor`."
		return check
	}
	if len(targets) == 0 {
		check.Status = StatusWarn
		check.Detail = "the helper reported no trust targets"
		check.Remediation = "Run `sudo devstack trust repair` to install the local CA."
		return check
	}

	var failed []string
	browserTargets := 0
	for _, target := range targets {
		if !target.OK {
			failed = append(failed, fmt.Sprintf("%s (%s)", target.Name, target.Detail))
		}
		if !strings.EqualFold(strings.TrimSpace(target.Name), "OS") {
			browserTargets++
		}
	}

	ca := "the local CA"
	if caFingerprint != "" {
		ca = "local CA " + shortFingerprint(caFingerprint)
	}
	if len(failed) != 0 {
		check.Status = StatusFail
		check.Detail = ca + " is not trusted by " + summarizeList(failed)
		check.Remediation = "Run `sudo devstack trust repair`, then restart the affected browsers."
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%s is trusted by %d targets", ca, len(targets))
	if browserTargets == 0 {
		check.Status = StatusWarn
		check.Detail += "; no browser trust targets were detected"
		check.Remediation = "Install Chrome or Firefox, then run `sudo devstack trust repair`."
	}
	return check
}

// portRequirement distinguishes privileged listeners from the builtin proxy's
// loopback ports, which are expected to be busy while the daemon runs.
type portRequirement int

const (
	requiredPort portRequirement = iota
	proxyPort
)

type portSpec struct {
	port int
	kind portRequirement
}

// checkPorts verifies the privileged and dashboard ports are free and treats an
// occupied proxy port as an expected warning.
func (s *Service) checkPorts() Check {
	check := Check{ID: "ports", Title: "Ports"}
	if s.deps.Ports == nil {
		check.Status = StatusSkipped
		check.Detail = "port checking is not configured"
		check.Remediation = "No action is required."
		return check
	}

	var (
		failures []string
		warnings []string
		free     []int
	)
	for _, spec := range s.portSpecs() {
		addr := ":" + strconv.Itoa(spec.port)
		err := s.deps.Ports.Listenable(addr)
		if err == nil {
			free = append(free, spec.port)
			continue
		}
		if spec.kind == proxyPort && isAddrInUse(err) {
			warnings = append(warnings, fmt.Sprintf("port %d is in use (expected while the devstack daemon is running)", spec.port))
			continue
		}
		failures = append(failures, fmt.Sprintf("port %d is unavailable: %v", spec.port, err))
	}

	switch {
	case len(failures) != 0:
		check.Status = StatusFail
		check.Detail = strings.Join(failures, "; ")
		if len(warnings) != 0 {
			check.Detail += "; " + strings.Join(warnings, "; ")
		}
		check.Remediation = "Stop the process using the port (`ss -ltnp` shows the owner) or change the port in config.yaml, then restart the daemon."
	case len(warnings) != 0:
		check.Status = StatusWarn
		check.Detail = strings.Join(warnings, "; ")
		if len(free) != 0 {
			check.Detail += "; all other required ports are free"
		}
		check.Remediation = "No action is needed while the daemon is running. Stop the daemon before starting another service on these ports."
	default:
		check.Status = StatusOK
		check.Detail = fmt.Sprintf("ports %s are available", joinPorts(free))
	}
	return check
}

// portSpecs returns the ports doctor inspects, honouring configured proxy and
// dashboard ports while always checking the privileged 80/443 listeners.
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

	specs := make([]portSpec, 0, 5)
	seen := make(map[int]struct{}, 5)
	add := func(port int, kind portRequirement) {
		if port <= 0 || port > 65535 {
			return
		}
		if _, ok := seen[port]; ok {
			return
		}
		seen[port] = struct{}{}
		specs = append(specs, portSpec{port: port, kind: kind})
	}
	add(80, requiredPort)
	add(443, requiredPort)
	add(uiPort, requiredPort)
	add(httpPort, proxyPort)
	add(httpsPort, proxyPort)
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
	if free < minFreeDiskBytes {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("only %s free at %s; at least 1 GiB is required", humanBytes(free), path)
		check.Remediation = "Free disk space, then rerun `devstack doctor`."
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%s free at %s", humanBytes(free), path)
	return check
}

// checkWSL reports the Windows backend as skipped until Plan 3 ships it.
func (s *Service) checkWSL() Check {
	return Check{
		ID:          "wsl",
		Title:       "WSL backend",
		Status:      StatusSkipped,
		Detail:      "WSL support is not part of this release; it is checked when the Windows backend ships.",
		Remediation: "No action is required.",
	}
}

// isAddrInUse recognises bind failures caused by an existing listener. The
// proxy owns 8080/8443, so this is normal while the daemon is running.
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") || strings.Contains(message, "eaddrinuse")
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

func joinPorts(ports []int) string {
	values := make([]string, len(ports))
	for i, port := range ports {
		values[i] = strconv.Itoa(port)
	}
	return strings.Join(values, ", ")
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
