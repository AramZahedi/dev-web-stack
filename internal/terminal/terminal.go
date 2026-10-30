// Package terminal implements site-scoped PTY sessions for the dashboard. A
// browser exchanges a short-lived, one-use ticket for an authenticated
// WebSocket connection; the daemon then runs the installing user's shell in the
// site's project directory with the same PHP/Node shims as `devstack shell`.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

const (
	// DefaultTicketTTL is the lifetime of a terminal WebSocket ticket. The
	// issued TTL is capped at MaxTicketTTL even when the caller asks for more.
	DefaultTicketTTL = 30 * time.Second
	MaxTicketTTL     = 30 * time.Second

	// DefaultMaxSessionsTotal and DefaultMaxSessionsPerSite bound concurrent
	// shells. The per-site limit also applies per requesting UID.
	DefaultMaxSessionsTotal   = 4
	DefaultMaxSessionsPerSite = 2

	// DefaultIdleTimeout closes a shell with no input or output. The absolute
	// DefaultMaxDuration caps a single session even if it stays active.
	DefaultIdleTimeout  = 30 * time.Minute
	DefaultMaxDuration  = 4 * time.Hour
	DefaultMaxOutput    = 4 << 20
	initialPTYCols      = 80
	initialPTYRows      = 24
	maxPTYDimension     = 1000
	outputReadBuffer    = 32 << 10
	processShutdownWait = 3 * time.Second
	websocketWriteWait  = 10 * time.Second
)

// Stable errors returned before a WebSocket upgrade. The API maps them onto
// JSON error envelopes; after an upgrade only frames are used.
var (
	ErrTicketInvalid = errors.New("terminal: ticket is invalid or already used")
	ErrTicketExpired = errors.New("terminal: ticket has expired")
	ErrSiteInvalid   = errors.New("terminal: site is not available for a terminal")
	ErrEnvironment   = errors.New("terminal: site environment is unavailable")
	ErrLimit         = errors.New("terminal: terminal session limit reached")
	ErrRoot          = errors.New("terminal: refusing to run a terminal as root")
	ErrUpgrade       = errors.New("terminal: WebSocket upgrade failed")
	ErrClosed        = errors.New("terminal: session is closed")
)

// Ticket is the public POST response. SiteID and UID are bound server-side and
// never serialized.
type Ticket struct {
	Token     string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
	SiteID    string    `json:"-"`
	UID       int       `json:"-"`
}

// RuntimeLister lists installed runtime builds. The site service provides the
// same view to `devstack shell`.
type RuntimeLister interface {
	List(ctx context.Context) ([]model.RuntimeBuild, error)
}

// PHPInfoProvider resolves the site's generated PHP configuration.
type PHPInfoProvider interface {
	PHPInfo(ctx context.Context, ref string) (sites.PHPInfo, error)
}

// Deps configures a Manager. UID is the installing user; zero means root and
// is always refused. A zero Shell falls back to $SHELL, then /bin/bash.
type Deps struct {
	Paths    config.Paths
	Runtimes RuntimeLister
	PHP      PHPInfoProvider
	// NodeDefault is the resolved machine-wide Node default. The site's
	// node_toolchain summary normally supplies it; this field is a fallback for
	// embedders that already hold the resolved build.
	NodeDefault *model.RuntimeBuild
	Logger      *slog.Logger
	UID         int
	Shell       string
	BasePATH    string
	Now         func() time.Time

	TicketTTL   time.Duration
	MaxTotal    int
	MaxPerSite  int
	IdleTimeout time.Duration
	MaxDuration time.Duration
	MaxOutput   int64
}

// Manager issues tickets and owns the live session table.
type Manager struct {
	paths       config.Paths
	runtimes    RuntimeLister
	php         PHPInfoProvider
	nodeDefault *model.RuntimeBuild
	logger      *slog.Logger
	uid         int
	shell       string
	basePATH    string

	tickets     *TicketStore
	ticketTTL   time.Duration
	maxTotal    int
	maxPerSite  int
	idleTimeout time.Duration
	maxDuration time.Duration
	maxOutput   int64

	mu       sync.Mutex
	sessions map[*Session]struct{}
	bySite   map[siteUID]int

	closed    chan struct{}
	closeOnce sync.Once
}

type siteUID struct {
	siteID string
	uid    int
}

// NewManager returns a Manager with production defaults for unset limits.
func NewManager(deps Deps) *Manager {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.TicketTTL <= 0 || deps.TicketTTL > MaxTicketTTL {
		deps.TicketTTL = DefaultTicketTTL
	}
	if deps.MaxTotal <= 0 {
		deps.MaxTotal = DefaultMaxSessionsTotal
	}
	if deps.MaxPerSite <= 0 {
		deps.MaxPerSite = DefaultMaxSessionsPerSite
	}
	if deps.IdleTimeout <= 0 {
		deps.IdleTimeout = DefaultIdleTimeout
	}
	if deps.MaxDuration <= 0 {
		deps.MaxDuration = DefaultMaxDuration
	}
	if deps.MaxOutput <= 0 {
		deps.MaxOutput = DefaultMaxOutput
	}
	return &Manager{
		paths:       deps.Paths,
		runtimes:    deps.Runtimes,
		php:         deps.PHP,
		nodeDefault: deps.NodeDefault,
		logger:      deps.Logger,
		uid:         deps.UID,
		shell:       deps.Shell,
		basePATH:    deps.BasePATH,
		tickets:     NewTicketStore(deps.TicketTTL, deps.Now),
		ticketTTL:   deps.TicketTTL,
		maxTotal:    deps.MaxTotal,
		maxPerSite:  deps.MaxPerSite,
		idleTimeout: deps.IdleTimeout,
		maxDuration: deps.MaxDuration,
		maxOutput:   deps.MaxOutput,
		sessions:    make(map[*Session]struct{}),
		bySite:      make(map[siteUID]int),
		closed:      make(chan struct{}),
	}
}

// Session is one prepared PTY shell. Run upgrades the connection before it is
// called; prepare/reserve happen inside Serve.
type Session struct {
	manager  *Manager
	id       string
	site     model.Site
	uid      int
	Dir      string
	Env      []string
	Shell    string
	reserved bool

	mu      sync.Mutex
	ptyFile *os.File
	process *os.Process
}

// IssueTicket validates the site and returns a short-lived one-use ticket
// bound to its ID and the manager's installing UID.
func (m *Manager) IssueTicket(ctx context.Context, site model.Site) (Ticket, error) {
	if err := m.checkUser(); err != nil {
		return Ticket{}, err
	}
	if _, err := validateSitePaths(site); err != nil {
		return Ticket{}, err
	}
	select {
	case <-m.closed:
		return Ticket{}, ErrClosed
	default:
	}
	return m.tickets.Issue(site.ID, m.uid)
}

// Serve consumes the ticket, prepares the shell, upgrades the connection, and
// runs the PTY until the shell exits or the client disconnects. Errors returned
// before websocket.Accept are safe to render as JSON; ErrUpgrade means the
// handshake failed after headers may have been written.
func (m *Manager) Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, site model.Site, token string) error {
	if err := m.checkUser(); err != nil {
		return err
	}
	if _, err := m.tickets.Consume(strings.TrimSpace(token), site.ID, m.uid); err != nil {
		return err
	}
	prepared, err := m.prepare(ctx, site)
	if err != nil {
		return err
	}
	if err := m.reserve(prepared); err != nil {
		return err
	}
	return prepared.serve(ctx, w, r)
}

// Close terminates every live session and waits briefly for their process
// groups to be reaped. Sessions observe the closure and clean up before
// returning, so daemon shutdown cannot leave orphan shells behind.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		close(m.closed)
		deadline := time.Now().Add(processShutdownWait * 2)
		for time.Now().Before(deadline) {
			if m.ActiveSessions() == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// ActiveSessions reports the number of reserved or running sessions.
func (m *Manager) ActiveSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) checkUser() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if m.uid <= 0 {
		return ErrRoot
	}
	if current := os.Getuid(); current == 0 {
		return ErrRoot
	}
	return nil
}

func (m *Manager) prepare(ctx context.Context, site model.Site) (*Session, error) {
	if err := m.checkUser(); err != nil {
		return nil, err
	}
	dir, err := validateSitePaths(site)
	if err != nil {
		return nil, err
	}
	env, err := m.environment(ctx, site)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEnvironment, err)
	}
	shell, err := m.resolveShell()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEnvironment, err)
	}
	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("terminal: generate session id: %w", err)
	}
	return &Session{
		manager: m,
		id:      id,
		site:    site,
		uid:     m.uid,
		Dir:     dir,
		Env:     env,
		Shell:   shell,
	}, nil
}

func (m *Manager) environment(ctx context.Context, site model.Site) ([]string, error) {
	if m.runtimes == nil {
		return nil, fmt.Errorf("terminal: runtime inventory is not available")
	}
	builds, err := m.runtimes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("terminal: list runtimes: %w", err)
	}
	var info *sites.PHPInfo
	if site.Kind == model.SitePHP {
		if m.php == nil {
			return nil, fmt.Errorf("terminal: PHP information is not available")
		}
		value, err := m.php.PHPInfo(ctx, site.ID)
		if err != nil {
			return nil, fmt.Errorf("terminal: read site PHP configuration: %w", err)
		}
		info = &value
	}
	basePATH := m.basePATH
	if basePATH == "" {
		basePATH = os.Getenv("PATH")
	}
	nodeDefault := tooling.NodeDefaultBuild(site, builds)
	if nodeDefault == nil {
		nodeDefault = m.nodeDefault
	}
	env, err := tooling.BuildSiteEnv(site, tooling.EnvDeps{
		Paths:       m.paths,
		Builds:      builds,
		PHPInfo:     info,
		NodeDefault: nodeDefault,
		BasePATH:    basePATH,
	})
	if err != nil {
		return nil, fmt.Errorf("terminal: build site environment: %w", err)
	}
	// TERM is forced so a PTY client always gets a colour-capable terminal;
	// the browser terminal is xterm.js-compatible.
	env.Values["TERM"] = "xterm-256color"
	env.Values["COLORTERM"] = "truecolor"
	return tooling.Environment(os.Environ(), env.Values), nil
}

func (m *Manager) resolveShell() (string, error) {
	candidate := strings.TrimSpace(m.shell)
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("SHELL"))
	}
	if candidate == "" {
		candidate = "/bin/bash"
	}
	switch strings.ToLower(filepath.Base(candidate)) {
	case "sudo", "doas", "pkexec":
		// A poisoned $SHELL must never escalate privileges. Fall through to
		// the safe system shells below.
		candidate = "/bin/bash"
	}
	if path, ok := executablePath(candidate); ok {
		return path, nil
	}
	if path, ok := executablePath("/bin/bash"); ok {
		return path, nil
	}
	if path, ok := executablePath("/bin/sh"); ok {
		return path, nil
	}
	return "", fmt.Errorf("terminal: no usable shell (tried %q, /bin/bash, /bin/sh)", candidate)
}

func executablePath(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	return path, true
}

func (m *Manager) reserve(session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.closed:
		return ErrClosed
	default:
	}
	if len(m.sessions) >= m.maxTotal {
		return fmt.Errorf("%w: %d total sessions", ErrLimit, m.maxTotal)
	}
	key := siteUID{siteID: session.site.ID, uid: session.uid}
	if m.bySite[key] >= m.maxPerSite {
		return fmt.Errorf("%w: %d sessions for this site", ErrLimit, m.maxPerSite)
	}
	session.reserved = true
	m.sessions[session] = struct{}{}
	m.bySite[key]++
	return nil
}

func (m *Manager) release(session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !session.reserved {
		return
	}
	session.reserved = false
	delete(m.sessions, session)
	key := siteUID{siteID: session.site.ID, uid: session.uid}
	if m.bySite[key] <= 1 {
		delete(m.bySite, key)
	} else {
		m.bySite[key]--
	}
}

func (m *Manager) audit(action string, session *Session, attrs ...any) {
	base := []any{
		"audit", "terminal.session",
		"action", action,
		"session", session.id,
		"site", session.site.Name,
		"uid", session.uid,
	}
	m.logger.Info("site terminal "+action, append(base, attrs...)...)
}

// validateSitePaths resolves the registered project and verifies that neither
// the project root nor the document root escaped through a symlink.
func validateSitePaths(site model.Site) (string, error) {
	raw := strings.TrimSpace(site.HostPath)
	if raw == "" {
		return "", fmt.Errorf("%w: site %q has no project directory", ErrSiteInvalid, site.Name)
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("%w: project path %q is not absolute", ErrSiteInvalid, raw)
	}
	project := filepath.Clean(raw)
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		return "", fmt.Errorf("%w: resolve project path: %v", ErrSiteInvalid, err)
	}
	if resolved != project {
		return "", fmt.Errorf("%w: project path resolves through a symlink", ErrSiteInvalid)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: inspect project path: %v", ErrSiteInvalid, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: project path is not a directory", ErrSiteInvalid)
	}

	documentRoot := strings.TrimSpace(site.DocumentRoot)
	if documentRoot != "" {
		target := documentRoot
		if !filepath.IsAbs(target) {
			target = filepath.Join(resolved, target)
		}
		target = filepath.Clean(target)
		docResolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return "", fmt.Errorf("%w: resolve document root: %v", ErrSiteInvalid, err)
		}
		if !pathWithin(resolved, docResolved) {
			return "", fmt.Errorf("%w: document root escapes the project", ErrSiteInvalid)
		}
	}
	return resolved, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
