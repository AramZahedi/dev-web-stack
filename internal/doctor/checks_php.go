package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const (
	phpPortMin = 43000
	phpPortMax = 43999

	// phpConfigFailureLimit caps how many per-site configuration errors are
	// shown before doctor summarizes the rest.
	phpConfigFailureLimit = 3
	// phpMasterTimeout bounds one TCP probe of a running php-fpm master.
	phpMasterTimeout = 500 * time.Millisecond
)

// checkPHP reports the state of installed PHP builds, per-site configuration
// validation, FastCGI port allocation, and running masters. All four checks are
// skipped when the store is unavailable or no PHP build and no PHP site exist.
func (s *Service) checkPHP(ctx context.Context) []Check {
	checks := []Check{
		{ID: "php-builds", Title: "PHP builds"},
		{ID: "php-config", Title: "PHP configuration"},
		{ID: "php-ports", Title: "PHP FastCGI ports"},
		{ID: "php-master", Title: "PHP masters"},
	}
	skipAll := func(detail, remediation string) []Check {
		for i := range checks {
			checks[i].Status = StatusSkipped
			checks[i].Detail = detail
			checks[i].Remediation = remediation
		}
		return checks
	}

	if s.deps.Store == nil {
		return skipAll(
			"the site store is not configured, so PHP state cannot be inspected",
			"No action is required until the site store is available.",
		)
	}

	sites, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return skipAll(
			"cannot list registered sites: "+errorDetail(err),
			"Restart the daemon with `devstack init`, then rerun `devstack doctor`.",
		)
	}
	phpSites := make([]model.Site, 0, len(sites))
	for _, site := range sites {
		if site.Kind == model.SitePHP {
			phpSites = append(phpSites, site)
		}
	}

	runtimes, buildErr := s.deps.Store.ListRuntimes(ctx)
	phpBuilds := make([]model.RuntimeBuild, 0, len(runtimes))
	if buildErr == nil {
		for _, build := range runtimes {
			if build.Kind == "php" {
				phpBuilds = append(phpBuilds, build)
			}
		}
	}

	if len(phpSites) == 0 && len(phpBuilds) == 0 && buildErr == nil {
		return skipAll(
			"no PHP build is installed and no PHP site is registered",
			"No action is required.",
		)
	}

	checks[0] = checkPHPBuilds(phpSites, phpBuilds, buildErr)
	checks[1] = checkPHPConfig(ctx, s.deps.PHP, phpSites)
	checks[2] = checkPHPPorts(phpSites)
	checks[3] = checkPHPMasters(ctx, phpSites)
	return checks
}

// checkPHPBuilds verifies that every installed PHP build has the binaries and
// extension directory a working FPM installation needs.
func checkPHPBuilds(sites []model.Site, builds []model.RuntimeBuild, listErr error) Check {
	check := Check{ID: "php-builds", Title: "PHP builds"}
	if listErr != nil {
		check.Status = StatusFail
		check.Detail = "cannot list installed runtimes: " + errorDetail(listErr)
		check.Remediation = "Restart the daemon with `devstack init`, then rerun `devstack doctor`."
		return check
	}
	if len(builds) == 0 {
		if len(sites) == 0 {
			check.Status = StatusSkipped
			check.Detail = "no PHP build is installed and no PHP site is registered"
			check.Remediation = "No action is required."
			return check
		}
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("%d PHP site(s) are registered but no PHP build is installed", len(sites))
		buildID := strings.TrimSpace(sites[0].PHPBuild)
		if buildID == "" {
			buildID = "<id>"
		}
		check.Remediation = fmt.Sprintf(
			"Run `devstack runtime install %s` to install a PHP build, then `devstack site php %s %s` to pin the site to it, and rerun `devstack doctor`.",
			buildID, sites[0].Name, phpVersionLabel(sites[0]),
		)
		return check
	}

	var problems, badIDs []string
	for _, build := range builds {
		if err := verifyPHPBuild(build); err != nil {
			problems = append(problems, fmt.Sprintf("%s (%s): %v", build.ID, build.Version, err))
			badIDs = append(badIDs, build.ID)
		}
	}
	if len(problems) != 0 {
		check.Status = StatusFail
		check.Detail = "installed PHP builds are incomplete: " + summarizeList(problems)
		check.Remediation = fmt.Sprintf(
			"Run `devstack runtime install %s` to reinstall the affected build, then rerun `devstack doctor`.",
			badIDs[0],
		)
		return check
	}

	labels := make([]string, 0, len(builds))
	for _, build := range builds {
		labels = append(labels, fmt.Sprintf("%s (%s)", build.ID, build.Version))
	}
	check.Status = StatusOK
	check.Detail = "PHP builds " + strings.Join(labels, ", ") + " provide php, php-fpm, and extension modules"
	return check
}

// verifyPHPBuild checks the layout produced by the runtime installer. A build
// needs bin/php, sbin/php-fpm, and extension modules either directly under
// lib/php/extensions or in one architecture subdirectory.
func verifyPHPBuild(build model.RuntimeBuild) error {
	if build.Backend == model.BackendWSL {
		return nil
	}
	if strings.TrimSpace(build.Path) == "" {
		return errors.New("build path is empty")
	}
	for _, rel := range []string{
		filepath.Join("bin", "php"),
		filepath.Join("sbin", "php-fpm"),
	} {
		info, err := os.Stat(filepath.Join(build.Path, rel))
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
	}

	extDir := filepath.Join(build.Path, "lib", "php", "extensions")
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Join("lib", "php", "extensions"), err)
	}
	if !phpExtensionsPresent(extDir, entries) {
		return errors.New("lib/php/extensions has no PHP extension modules")
	}
	return nil
}

func phpExtensionsPresent(dir string, entries []os.DirEntry) bool {
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".so") {
			return true
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modules, err := os.ReadDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		for _, module := range modules {
			if !module.IsDir() && strings.HasSuffix(module.Name(), ".so") {
				return true
			}
		}
	}
	return false
}

// checkPHPConfig validates the composed PHP configuration of every registered
// PHP site. It reports at most three failures and summarizes the remainder.
func checkPHPConfig(ctx context.Context, validator PHPValidator, sites []model.Site) Check {
	check := Check{ID: "php-config", Title: "PHP configuration"}
	if len(sites) == 0 {
		check.Status = StatusSkipped
		check.Detail = "no PHP site is registered, so there is no PHP configuration to validate"
		check.Remediation = "No action is required."
		return check
	}
	if validator == nil {
		check.Status = StatusFail
		check.Detail = "PHP sites are registered but the PHP validator is not configured"
		check.Remediation = "Run `devstack init` to restart the daemon with PHP validation enabled, then rerun `devstack doctor`."
		return check
	}

	var failures []string
	var firstFailed *model.Site
	for i := range sites {
		site := sites[i]
		if err := validator.ValidatePHP(ctx, site); err != nil {
			if firstFailed == nil {
				firstFailed = &sites[i]
			}
			failures = append(failures, fmt.Sprintf("%s: %s", site.Name, errorDetail(err)))
		}
	}
	if len(failures) == 0 {
		check.Status = StatusOK
		check.Detail = fmt.Sprintf("PHP configuration validated for %d site(s)", len(sites))
		return check
	}

	check.Status = StatusFail
	shown := failures
	if len(shown) > phpConfigFailureLimit {
		shown = shown[:phpConfigFailureLimit]
	}
	check.Detail = fmt.Sprintf("%d PHP site(s) have invalid configuration: %s", len(failures), strings.Join(shown, "; "))
	if remaining := len(failures) - len(shown); remaining > 0 {
		check.Detail += fmt.Sprintf("; and %d more", remaining)
	}
	site := *firstFailed
	check.Remediation = fmt.Sprintf(
		"Run `devstack site php %s %s` to reapply its PHP configuration, then `devstack site restart %s` and rerun `devstack doctor`.",
		site.Name, phpVersionLabel(site), site.Name,
	)
	return check
}

// checkPHPPorts verifies that every running PHP site owns a unique FastCGI port
// in the 43000-43999 range.
func checkPHPPorts(sites []model.Site) Check {
	check := Check{ID: "php-ports", Title: "PHP FastCGI ports"}
	running := runningPHPSites(sites)
	if len(running) == 0 {
		check.Status = StatusSkipped
		check.Detail = "no running PHP site needs a FastCGI port"
		check.Remediation = "No action is required."
		return check
	}

	byPort := make(map[int][]string, len(running))
	var problems []string
	firstBad := ""
	for _, site := range running {
		if site.FastCGIPort < phpPortMin || site.FastCGIPort > phpPortMax {
			problems = append(problems, fmt.Sprintf("%s has FastCGI port %d outside %d-%d", site.Name, site.FastCGIPort, phpPortMin, phpPortMax))
			if firstBad == "" {
				firstBad = site.Name
			}
			continue
		}
		byPort[site.FastCGIPort] = append(byPort[site.FastCGIPort], site.Name)
	}
	var duplicates []string
	for port, names := range byPort {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		duplicates = append(duplicates, fmt.Sprintf("%s share duplicate FastCGI port %d", strings.Join(names, " and "), port))
		if firstBad == "" {
			firstBad = names[0]
		}
	}
	sort.Strings(duplicates)
	problems = append(problems, duplicates...)

	if len(problems) != 0 {
		check.Status = StatusFail
		check.Detail = "PHP FastCGI ports are invalid: " + strings.Join(problems, "; ")
		check.Remediation = fmt.Sprintf(
			"Run `devstack site restart %s` to allocate a fresh FastCGI port, then rerun `devstack doctor`.",
			firstBad,
		)
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("%d running PHP site(s) have unique FastCGI ports in %d-%d", len(running), phpPortMin, phpPortMax)
	return check
}

// checkPHPMasters dials each running site's FastCGI port with a short timeout.
// A successful TCP connection proves a master is listening, not that it speaks
// the FastCGI protocol.
func checkPHPMasters(ctx context.Context, sites []model.Site) Check {
	check := Check{ID: "php-master", Title: "PHP masters"}
	running := runningPHPSites(sites)
	if len(running) == 0 {
		check.Status = StatusSkipped
		check.Detail = "no running PHP site needs a FastCGI master"
		check.Remediation = "No action is required."
		return check
	}

	var failures []string
	firstBad := ""
	for _, site := range running {
		port := site.FastCGIPort
		if port < phpPortMin || port > phpPortMax {
			failures = append(failures, fmt.Sprintf("%s: no valid FastCGI port", site.Name))
			if firstBad == "" {
				firstBad = site.Name
			}
			continue
		}
		dialer := net.Dialer{Timeout: phpMasterTimeout}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s (port %d): %s", site.Name, port, errorDetail(err)))
			if firstBad == "" {
				firstBad = site.Name
			}
			continue
		}
		_ = conn.Close()
	}

	if len(failures) != 0 {
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("%d of %d PHP masters did not accept a FastCGI connection: %s",
			len(failures), len(running), strings.Join(failures, "; "))
		check.Remediation = fmt.Sprintf(
			"Run `devstack site restart %s` to start its PHP master, then rerun `devstack doctor`.",
			firstBad,
		)
		return check
	}

	check.Status = StatusOK
	check.Detail = fmt.Sprintf("all %d running PHP masters accept FastCGI connections on 127.0.0.1", len(running))
	return check
}

func runningPHPSites(sites []model.Site) []model.Site {
	running := make([]model.Site, 0, len(sites))
	for _, site := range sites {
		if site.Kind == model.SitePHP && site.DesiredState == model.DesiredRunning {
			running = append(running, site)
		}
	}
	return running
}

func phpVersionLabel(site model.Site) string {
	if version := strings.TrimSpace(site.PHPVersion); version != "" {
		return version
	}
	return "<version>"
}
