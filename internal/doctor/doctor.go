// Package doctor inspects a local devstack installation, reports actionable
// problems, and repairs the parts that can be safely re-applied.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

// Status is the outcome of a single doctor check.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarn    Status = "warn"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// Check is one diagnostic result. Remediation is a concrete command or action
// the user can take; it is empty when the check passes.
type Check struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      Status `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation"`
}

// Report is the complete result of one doctor run. OK is true only when no
// check failed; warnings and skipped checks do not fail the report.
type Report struct {
	Checks []Check `json:"checks"`
	OK     bool    `json:"ok"`
}

// Deps are the services the doctor inspects. Optional dependencies may be nil;
// the corresponding check or repair step is then skipped.
type Deps struct {
	Config config.Config
	// Platform is normally runtime.GOOS; tests can force the Windows branch.
	Platform string
	Home     string
	Store    *store.Store
	Helper   Helper
	Trust    Trust
	Ports    PortChecker
	Runtimes RuntimeChecker
	Disk     DiskChecker
	// PHP validates a PHP site's composed configuration without starting a
	// master. It is nil when the daemon was built without PHP support.
	PHP PHPValidator
	// Vhosts reports the helper's external serving state. It may be nil in
	// builtin mode; an external mode without it fails the serving-mode check.
	Vhosts Vhosts
	// HostsPath is the hosts file inspected by the hosts check. An empty value
	// uses /etc/hosts. Tests point it at a temporary file.
	HostsPath string
	// ToolBinDir is the system bin directory expected to contain managed tool
	// shims. Empty uses /usr/local/bin. Tests use a temporary directory.
	ToolBinDir string
	// LookPath resolves a bare command. Nil uses exec.LookPath.
	LookPath func(string) (string, error)
	Windows  WindowsDiagnostics
}

// WindowsDiagnostics describes the Windows/WSL dependencies without granting
// doctor any host mutation privileges. Production wiring supplies the WSL and
// authenticated-agent probes; tests use a fake implementation.
type WindowsDiagnostics interface {
	CheckWSL(ctx context.Context, distro string) error
	CheckAgent(ctx context.Context, distro string) error
	CheckDocker(ctx context.Context, distro string) error
	CheckLocalhostForwarding(ctx context.Context, distro string, port int) error
	CheckPorts(ctx context.Context, distro string) error
}

// Helper is the privileged helper surface used by the doctor.
type Helper interface {
	Status(ctx context.Context) (helperproto.StatusResponse, error)
	SyncHosts(ctx context.Context, domains []string) error
	InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error)
}

// Trust reports the state of each trust store and browser profile.
type Trust interface {
	TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error)
}

// PortChecker reports whether a TCP address can be bound.
type PortChecker interface {
	Listenable(addr string) error
}

// RuntimeChecker verifies every installed runtime against its manifest.
type RuntimeChecker interface {
	VerifyAll(ctx context.Context) error
}

// DiskChecker reports the free bytes available at path.
type DiskChecker interface {
	FreeBytes(path string) (uint64, error)
}

// Vhosts reports the helper-managed external serving state and the daemon's
// current serving mode. ServingMode is read live so a runtime switch is not
// masked by the startup Config snapshot.
type Vhosts interface {
	VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error)
	ServingMode() string
}

// PHPValidator validates one PHP site's composed php.ini and php-fpm
// configuration without starting a master process.
type PHPValidator interface {
	ValidatePHP(ctx context.Context, site model.Site) error
}

// Service runs doctor checks and repairs against a fixed set of dependencies.
type Service struct {
	deps Deps
}

// NewService returns a doctor service for deps.
func NewService(deps Deps) *Service {
	return &Service{deps: deps}
}

// Run executes every check and returns their combined report. Individual
// failures are represented as failed checks rather than as an error, so callers
// can always render the report. The error is reserved for an invalid service.
func (s *Service) Run(ctx context.Context) (Report, error) {
	if s == nil {
		return Report{}, errors.New("doctor: nil service")
	}
	ctx = nonNilContext(ctx)

	expected, expectedErr := s.expectedDomains(ctx)
	helperCheck, helperStatus, helperOK := s.checkHelper(ctx)
	checks := []Check{
		helperCheck,
		s.checkCA(helperStatus, helperOK),
		s.checkHosts(expected, expectedErr),
		s.checkTrust(ctx, helperStatus.CAFingerprint),
		s.checkPorts(),
		s.checkRuntimes(ctx),
		s.checkTools(),
		s.checkDisk(),
	}
	checks = append(checks, s.windowsChecks(ctx)...)
	checks = append(checks, s.checkVhosts(ctx)...)
	checks = append(checks, s.checkPHP(ctx)...)
	return newReport(checks), nil
}

// Repair re-applies the idempotent parts of the installation (trust, hosts,
// runtime verification) and then reruns every check. A repair step that fails
// is not returned as an error: the refreshed report shows the remaining
// problem and its remediation. Data is never deleted.
func (s *Service) Repair(ctx context.Context) (Report, error) {
	if s == nil {
		return Report{}, errors.New("doctor: nil service")
	}
	ctx = nonNilContext(ctx)

	if s.deps.Helper != nil && strings.TrimSpace(s.deps.Home) != "" {
		_, _ = s.deps.Helper.InstallTrust(ctx, s.deps.Home)
	}

	if s.deps.Helper != nil {
		if domains, err := s.expectedDomains(ctx); err == nil {
			// hosts.Render refuses duplicated or unbalanced markers. Attempting
			// to sync would fail the same way and hide the manual fix that is
			// actually required, so leave the file untouched and let Run report
			// the malformed block.
			if !s.hostsBlockMalformed() {
				_ = s.deps.Helper.SyncHosts(ctx, domains)
			}
		}
	}

	if s.deps.Runtimes != nil {
		_ = s.deps.Runtimes.VerifyAll(ctx)
	}

	return s.Run(ctx)
}

// expectedDomains returns every domain the managed hosts block must contain:
// the dashboard plus each registered site and its optional www alias.
func (s *Service) expectedDomains(ctx context.Context) ([]string, error) {
	if s.deps.Store == nil {
		return nil, errors.New("store is not configured")
	}
	sites, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("list registered sites: %w", err)
	}

	set := map[string]struct{}{dashboardDomain: {}}
	for _, site := range sites {
		primary := normalizeDomain(site.Domain)
		if primary == "" {
			continue
		}
		set[primary] = struct{}{}
		if site.WWW {
			set["www."+primary] = struct{}{}
		}
	}

	domains := make([]string, 0, len(set))
	for domain := range set {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains, nil
}

// newReport marks the report healthy unless at least one check failed.
func newReport(checks []Check) Report {
	report := Report{Checks: checks, OK: true}
	for _, check := range checks {
		if check.Status == StatusFail {
			report.OK = false
			break
		}
	}
	return report
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
