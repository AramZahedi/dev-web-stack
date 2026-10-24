package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
	"github.com/AramZahedi/dev-web-stack/internal/supervisor"
)

const (
	// defaultPHPGeneration keeps the bare site id as its supervisor id so
	// Plan 1's Stop(siteID) and persisted process records keep working.
	defaultPHPGeneration = "default"

	phpPortMin = 43000
	phpPortMax = 43999

	phpHealthLogTail = 200
	phpHealthBody    = 64 << 10
)

// Readiness timings are variables so tests can shorten the health deadline
// without waiting the full 30 seconds.
var (
	// phpReadyTimeout bounds the TCP readiness probe and phpHealthTimeout the
	// FastCGI health poll for one FPM master.
	phpReadyTimeout  = 30 * time.Second
	phpHealthTimeout = 30 * time.Second
	phpHealthPoll    = 100 * time.Millisecond
	// phpHealthAttemptTimeout caps one FastCGI health request so a master that
	// accepts TCP but never answers cannot consume the whole readiness budget
	// in a single attempt.
	phpHealthAttemptTimeout = 2 * time.Second
)

var _ PHPStager = (*Local)(nil)

// generationPattern keeps a generation usable as part of a supervisor id and
// prevents path or separator characters from reaching pid-file names.
var generationPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// PreparePHP validates one candidate PHP generation and records it without
// starting a master. Starting it makes the generation active.
func (l *Local) PreparePHP(ctx context.Context, siteID, generation string, spec SiteSpec) (PreparedSite, error) {
	return l.preparePHP(ctx, siteID, generation, spec)
}

func (l *Local) preparePHP(ctx context.Context, siteID, generation string, spec SiteSpec) (PreparedSite, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PreparedSite{}, err
	}
	if strings.TrimSpace(siteID) == "" {
		return PreparedSite{}, errors.New("backend: php site id is required")
	}
	if err := validateGeneration(generation); err != nil {
		return PreparedSite{}, fmt.Errorf("backend: php site %q: %w", siteID, err)
	}
	if spec.ID != "" && spec.ID != siteID {
		return PreparedSite{}, fmt.Errorf("backend: php site %q does not match spec id %q", siteID, spec.ID)
	}
	if spec.Kind != "" && spec.Kind != string(model.SitePHP) {
		return PreparedSite{}, fmt.Errorf("backend: php site %q has kind %q", siteID, spec.Kind)
	}
	spec.ID = siteID
	spec.Kind = string(model.SitePHP)

	if err := validatePHPSpec(spec); err != nil {
		return PreparedSite{}, err
	}
	documentRoot, err := resolvePHPDocumentRoot(spec)
	if err != nil {
		return PreparedSite{}, err
	}

	p := preparedSite{
		spec:         cloneSiteSpec(spec),
		documentRoot: documentRoot,
		workingDir:   documentRoot,
	}
	l.mu.Lock()
	site := l.staged[siteID]
	if site == nil {
		site = &stagedSite{generations: make(map[string]preparedSite)}
		l.staged[siteID] = site
	}
	site.generations[generation] = p
	l.mu.Unlock()

	return PreparedSite{
		BackendHost: "127.0.0.1",
		FastCGIPort: spec.FastCGIPort,
	}, nil
}

// StartPHP launches one prepared generation and makes it the active one. It
// waits for TCP readiness and then polls the FastCGI health script.
func (l *Local) StartPHP(ctx context.Context, siteID, generation string) (ProcessInfo, error) {
	return l.startPHP(ctx, siteID, generation)
}

func (l *Local) startPHP(ctx context.Context, siteID, generation string) (ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ProcessInfo{}, err
	}

	l.mu.Lock()
	site := l.staged[siteID]
	var p preparedSite
	var ok bool
	if site != nil {
		p, ok = site.generations[generation]
	}
	l.mu.Unlock()
	if !ok {
		return ProcessInfo{}, fmt.Errorf("backend: php site %q generation %q is not prepared", siteID, generation)
	}

	processID := generationProcessID(siteID, generation)
	if st, known := l.supervisor.Status(processID); known {
		switch st.State {
		case model.StatusRunning:
			l.markPHPStarted(siteID, generation)
			return ProcessInfo{PID: st.PID, StartedAt: st.StartedAt}, nil
		case model.StatusStarting:
			return ProcessInfo{}, fmt.Errorf("backend: php site %q generation %q is already starting", siteID, generation)
		case model.StatusStopping:
			return ProcessInfo{}, fmt.Errorf("backend: php site %q generation %q is stopping", siteID, generation)
		}
	}

	procSpec, err := l.phpProcessSpec(siteID, generation, p)
	if err != nil {
		return ProcessInfo{}, err
	}
	st, err := l.supervisor.Start(ctx, procSpec)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("backend: start php site %q generation %q: %w%s",
			siteID, generation, err, l.phpLogTail(siteID))
	}
	if err := l.awaitPHPHealth(ctx, siteID, processID, p.spec); err != nil {
		return ProcessInfo{}, err
	}

	l.markPHPStarted(siteID, generation)
	return ProcessInfo{PID: st.PID, StartedAt: st.StartedAt}, nil
}

// StopPHP stops one prepared generation. Stopping the active generation
// promotes the most recently started remaining generation so a failed swap
// still reports the old master.
func (l *Local) StopPHP(ctx context.Context, siteID, generation string) error {
	return l.stopPHP(ctx, siteID, generation, false)
}

// StopPHPGraceful drains a generation with SIGQUIT before escalating.
func (l *Local) StopPHPGraceful(ctx context.Context, siteID, generation string) error {
	return l.stopPHP(ctx, siteID, generation, true)
}

func (l *Local) stopPHP(ctx context.Context, siteID, generation string, graceful bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	site := l.staged[siteID]
	_, known := preparedGeneration(site, generation)
	l.mu.Unlock()
	if !known {
		return nil
	}

	processID := generationProcessID(siteID, generation)
	var stopErr error
	if graceful {
		stopErr = l.supervisor.StopGraceful(ctx, processID)
	} else {
		stopErr = l.supervisor.Stop(ctx, processID)
	}
	if stopErr != nil {
		return fmt.Errorf("backend: stop php site %q generation %q: %w", siteID, generation, stopErr)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	site = l.staged[siteID]
	if site == nil {
		return nil
	}
	if p, ok := site.generations[generation]; ok {
		p.started = 0
		site.generations[generation] = p
	}
	if site.active == generation {
		site.active = mostRecentGeneration(site.generations)
	}
	return nil
}

// hasStagedSite reports whether id uses the PHP generation path.
func (l *Local) hasStagedSite(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.staged[id]
	return ok
}

// stagedGenerations returns the prepared generation names for id, sorted for
// deterministic shutdown, and whether id is a staged PHP site.
func (l *Local) stagedGenerations(id string) ([]string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	site := l.staged[id]
	if site == nil {
		return nil, false
	}
	generations := make([]string, 0, len(site.generations))
	for generation := range site.generations {
		generations = append(generations, generation)
	}
	sort.Strings(generations)
	return generations, true
}

// stagedStatus reports the active generation's supervisor state. The boolean
// is false when id is not a staged PHP site.
func (l *Local) stagedStatus(id string) (Status, bool) {
	l.mu.Lock()
	site := l.staged[id]
	active := ""
	if site != nil {
		active = site.active
	}
	l.mu.Unlock()
	if site == nil {
		return Status{}, false
	}
	if active == "" {
		return Status{State: model.StatusStopped}, true
	}
	st, ok := l.supervisor.Status(generationProcessID(id, active))
	if !ok {
		return Status{State: model.StatusStopped}, true
	}
	return Status{State: st.State, PID: st.PID, Message: st.LastError}, true
}

// markPHPStarted records the generation as the most recently started one and
// makes it active.
func (l *Local) markPHPStarted(siteID, generation string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	site := l.staged[siteID]
	if site == nil {
		return
	}
	p, ok := site.generations[generation]
	if !ok {
		return
	}
	l.startSeq++
	p.started = l.startSeq
	site.generations[generation] = p
	site.active = generation
}

// awaitPHPHealth polls the generated FastCGI health script. Any failure stops
// the master and returns a readiness error with the recent site log tail.
func (l *Local) awaitPHPHealth(ctx context.Context, siteID, processID string, spec SiteSpec) error {
	if err := waitPHPHealth(ctx, spec.FastCGIPort, spec.PHPHealthScript); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = l.supervisor.Stop(stopCtx, processID)
		cancel()
		return fmt.Errorf("backend: php site %q did not become healthy on port %d: %w%s",
			siteID, spec.FastCGIPort, err, l.phpLogTail(siteID))
	}
	return nil
}

// phpProcessSpec builds the supervised php-fpm master for one generation.
func (l *Local) phpProcessSpec(siteID, generation string, p preparedSite) (supervisor.ProcessSpec, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p.spec.FastCGIPort))
	fpmBin := filepath.Join(p.spec.PHPBuildPath, "sbin", "php-fpm")
	return supervisor.ProcessSpec{
		ID:      generationProcessID(siteID, generation),
		Argv:    phpfpm.Argv(fpmBin, p.spec.FPMConfigPath, p.spec.PHPIniPath),
		Env:     phpEnvironment(p.spec.PHPBuildPath, p.spec.Env),
		Dir:     p.workingDir,
		Ready:   supervisor.ReadyCheck{TCPAddr: addr, Timeout: phpReadyTimeout},
		Restart: defaultRestartPolicy,
		Stdout:  l.logs.Writer(p.spec.ID, "fpm"),
		Stderr:  l.logs.Writer(p.spec.ID, "fpm"),
	}, nil
}

// phpLogTail renders the most recent site log lines for a readiness error.
func (l *Local) phpLogTail(siteID string) string {
	if l.logs == nil {
		return ""
	}
	lines, err := l.logs.Tail(siteID, phpHealthLogTail)
	if err != nil || len(lines) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\nlast logs:\n")
	for _, line := range lines {
		builder.WriteString(formatLogLine(line))
	}
	return strings.TrimRight(builder.String(), "\n")
}

// generationProcessID returns the supervisor id for one generation. The
// default generation deliberately keeps the bare site id.
func generationProcessID(siteID, generation string) string {
	if generation == defaultPHPGeneration {
		return siteID
	}
	return siteID + "#" + generation
}

func validateGeneration(generation string) error {
	if generation == "" {
		return errors.New("generation is required")
	}
	if !generationPattern.MatchString(generation) {
		return fmt.Errorf("generation %q contains invalid characters", generation)
	}
	return nil
}

// validatePHPSpec checks every value Prepare needs before a master may start.
func validatePHPSpec(spec SiteSpec) error {
	id := spec.ID
	if strings.TrimSpace(spec.PHPBuild) == "" {
		return fmt.Errorf("backend: php site %q requires a build id", id)
	}
	if err := validatePHPBuild(spec.PHPBuildPath, id); err != nil {
		return err
	}
	for _, path := range []struct {
		field string
		value string
	}{
		{"php.ini", spec.PHPIniPath},
		{"php-fpm config", spec.FPMConfigPath},
		{"health script", spec.PHPHealthScript},
	} {
		if err := validateAbsoluteCleanPath(path.field, path.value, id); err != nil {
			return err
		}
	}
	if spec.FastCGIPort < phpPortMin || spec.FastCGIPort > phpPortMax {
		return fmt.Errorf("backend: php site %q FastCGI port %d is outside %d-%d",
			id, spec.FastCGIPort, phpPortMin, phpPortMax)
	}
	return nil
}

func validatePHPBuild(buildPath, id string) error {
	if strings.TrimSpace(buildPath) == "" {
		return fmt.Errorf("backend: php site %q requires a build path", id)
	}
	if !filepath.IsAbs(buildPath) || filepath.Clean(buildPath) != buildPath {
		return fmt.Errorf("backend: php site %q build path %q is not an absolute clean path", id, buildPath)
	}
	for _, rel := range []string{"bin/php", "sbin/php-fpm"} {
		path := filepath.Join(buildPath, rel)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("backend: php site %q build %s: %w", id, path, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("backend: php site %q build %s is not executable", id, path)
		}
	}
	return nil
}

func validateAbsoluteCleanPath(field, value, id string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("backend: php site %q %s path is required", id, field)
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("backend: php site %q %s path %q is not absolute and clean", id, field, value)
	}
	return nil
}

// phpEnvironment builds the master environment: the site's variables, with the
// build's bin directory first on PATH and no host ini scan directory.
func phpEnvironment(buildPath string, siteEnv map[string]string) map[string]string {
	env := make(map[string]string, len(siteEnv)+2)
	pathValue := os.Getenv("PATH")
	if sitePath, ok := siteEnv["PATH"]; ok && sitePath != "" {
		pathValue = sitePath
	}
	for key, value := range siteEnv {
		env[key] = value
	}
	binDir := filepath.Join(buildPath, "bin")
	if pathValue == "" {
		env["PATH"] = binDir
	} else {
		env["PATH"] = binDir + string(os.PathListSeparator) + pathValue
	}
	env["PHP_INI_SCAN_DIR"] = ""
	return env
}

// preparedGeneration returns the prepared generation and whether it exists.
func preparedGeneration(site *stagedSite, generation string) (preparedSite, bool) {
	if site == nil {
		return preparedSite{}, false
	}
	p, ok := site.generations[generation]
	return p, ok
}

// mostRecentGeneration returns the started generation with the highest start
// sequence, or "" when none is running.
func mostRecentGeneration(generations map[string]preparedSite) string {
	best := ""
	var bestSeq uint64
	for generation, p := range generations {
		if p.started == 0 {
			continue
		}
		if p.started > bestSeq || (p.started == bestSeq && generation < best) {
			best = generation
			bestSeq = p.started
		}
	}
	return best
}

// waitPHPHealth polls the FastCGI health script until it returns "ok". The
// overall budget is fixed at phpHealthTimeout and every attempt is bounded by
// both that budget and phpHealthAttemptTimeout, so a master that accepts TCP
// but never answers cannot block Start past the deadline.
func waitPHPHealth(ctx context.Context, port int, script string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	overallCtx, cancel := context.WithTimeout(ctx, phpHealthTimeout)
	defer cancel()

	var last error
	for {
		deadline, _ := overallCtx.Deadline()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		budget := phpHealthAttemptTimeout
		if budget <= 0 || budget > remaining {
			budget = remaining
		}
		attemptCtx, attemptCancel := context.WithTimeout(overallCtx, budget)
		body, err := phpHealthRequest(attemptCtx, port, script)
		attemptCancel()

		switch {
		case err == nil && body == "ok":
			return nil
		case err == nil:
			last = fmt.Errorf("health script returned %q", body)
		case ctx.Err() != nil:
			// The caller's context, not the readiness deadline, ended the
			// attempt; report cancellation rather than a health failure.
			return ctx.Err()
		case overallCtx.Err() != nil:
			// The readiness deadline elapsed during this attempt. The timeout
			// is reported below with the last attempt error.
		default:
			last = err
		}
		if overallCtx.Err() != nil {
			break
		}

		pause := phpHealthPoll
		if remaining := time.Until(deadline); pause > remaining {
			pause = remaining
		}
		if pause <= 0 {
			break
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	if last == nil {
		last = context.DeadlineExceeded
	}
	return fmt.Errorf("health check did not pass within %s: %w", phpHealthTimeout, last)
}

// phpHealthRequest sends one FastCGI GET for the health script.
func phpHealthRequest(ctx context.Context, port int, script string) (string, error) {
	if strings.TrimSpace(script) == "" {
		return "", errors.New("backend: php health script is required")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	client, err := fcgi.Dial(ctx, addr)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Do(ctx, fcgi.Request{
		Params:     phpHealthParams(port, script),
		BodyLength: 0,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, phpHealthBody))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("health script status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func phpHealthParams(port int, script string) map[string]string {
	name := "/" + filepath.Base(script)
	return map[string]string{
		"GATEWAY_INTERFACE": "CGI/1.1",
		"REQUEST_METHOD":    "GET",
		"REQUEST_URI":       name,
		"SCRIPT_FILENAME":   script,
		"SCRIPT_NAME":       name,
		"SERVER_PROTOCOL":   "HTTP/1.1",
		"SERVER_SOFTWARE":   "devstack",
		"REMOTE_ADDR":       "127.0.0.1",
		"SERVER_ADDR":       "127.0.0.1",
		"SERVER_NAME":       "localhost",
		"SERVER_PORT":       strconv.Itoa(port),
		"DOCUMENT_ROOT":     filepath.Dir(script),
		"QUERY_STRING":      "",
	}
}

// ActivePHPGeneration returns the generation currently backing a site, or an
// empty string when no master is active.
func (l *Local) ActivePHPGeneration(siteID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	site := l.staged[siteID]
	if site == nil {
		return ""
	}
	return site.active
}
