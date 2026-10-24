// Package supervisor manages the lifecycle of site backend processes.
//
// Each process runs in its own process group so the whole tree can be stopped
// with one signal. The manager probes TCP readiness, writes a pid file that
// records the kernel start time for safe orphan cleanup, and restarts
// processes that exit unexpectedly according to a bounded backoff policy.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const (
	// defaultReadyTimeout is the readiness deadline when the caller does not
	// set one. Callers that need longer (for example Node) pass their own.
	defaultReadyTimeout = 30 * time.Second

	// defaultStopGrace is how long Stop waits after SIGTERM before sending
	// SIGKILL.
	defaultStopGrace = 10 * time.Second

	// readyPollInterval is how often a TCP readiness probe is retried.
	readyPollInterval = 100 * time.Millisecond

	// groupPollInterval is how often process-group disappearance is checked.
	groupPollInterval = 10 * time.Millisecond

	// killWait bounds the wait for a process to disappear after SIGKILL.
	killWait = 5 * time.Second

	// ioDrainTimeout bounds how long Wait waits for inherited stdout/stderr
	// pipes to close after the direct child exits. Without it, a daemonizing
	// child that leaves a descendant holding the pipes would hide the exit.
	ioDrainTimeout = 500 * time.Millisecond

	// cleanupTermWait and cleanupKillWait bound orphan cleanup escalation.
	cleanupTermWait = 500 * time.Millisecond
	cleanupKillWait = 2 * time.Second
)

// ErrReadyTimeout is returned by Start when a process does not accept TCP
// connections on its readiness address before the deadline.
var ErrReadyTimeout = errors.New("supervisor: readiness timeout")

var (
	errAlreadyRunning     = errors.New("supervisor: process already running")
	errStoppedDuringStart = errors.New("supervisor: stopped during startup")
)

// ReadyCheck describes how to tell that a started process is ready. An empty
// TCPAddr means the process is considered ready immediately after it starts;
// otherwise the supervisor dials TCPAddr until it connects or Timeout expires.
type ReadyCheck struct {
	TCPAddr string
	Timeout time.Duration
}

// RestartPolicy bounds automatic restarts after unexpected exits. MaxRestarts
// is the failure-count threshold: a process is marked crashed when this many
// failures occur within Window. For example, MaxRestarts=5 allows four
// restarts and crashes on the fifth failure. A zero value crashes on the first
// failure.
type RestartPolicy struct {
	MaxRestarts int
	Window      time.Duration
	Backoff     []time.Duration
}

// ProcessSpec describes one supervised process.
type ProcessSpec struct {
	ID      string
	Argv    []string
	Env     map[string]string
	Dir     string
	Ready   ReadyCheck
	Restart RestartPolicy
	Stdout  io.Writer
	Stderr  io.Writer
}

// Status is a snapshot of a supervised process.
type Status struct {
	State     model.SiteStatus
	PID       int
	StartedAt time.Time
	Restarts  int
	LastError string
}

// Manager owns the processes started through it. It is safe for concurrent
// use. StopGrace may be set before starting processes to shorten the SIGTERM
// grace period; NewManager defaults it to 10 seconds.
type Manager struct {
	// StopGrace is how long Stop waits after SIGTERM before sending SIGKILL.
	StopGrace time.Duration

	runDir  string
	logger  *slog.Logger
	initErr error

	mu    sync.Mutex
	procs map[string]*proc
}

// proc is the mutable state for one managed process. Fields are guarded by mu;
// lifecycle transitions are serialized by the single supervise goroutine.
type proc struct {
	id   string
	spec ProcessSpec

	stopCh   chan struct{}
	stopOnce sync.Once
	doneCh   chan struct{}
	resultCh chan startOutcome

	mu         sync.Mutex
	status     Status
	child      *child
	stopping   bool
	restarts   int
	stopSignal syscall.Signal

	// pidFileLeaked keeps the pid file when the process group could not be
	// confirmed gone, so a later CleanupOrphans can retry.
	pidFileLeaked bool
}

// startOutcome is delivered once to the caller waiting in Start.
type startOutcome struct {
	status Status
	err    error
}

// NewManager returns a manager whose pid files live in runDir. The directory
// is created when missing. A nil logger uses slog.Default.
func NewManager(runDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		runDir:    runDir,
		logger:    logger,
		StopGrace: defaultStopGrace,
		procs:     make(map[string]*proc),
	}
	if runDir == "" {
		m.initErr = errors.New("supervisor: empty run directory")
		return m
	}
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		m.initErr = fmt.Errorf("supervisor: create run directory %s: %w", runDir, err)
	}
	return m
}

// Start launches spec and waits until it is ready. The process runs in its own
// process group and is restarted according to spec.Restart when it exits
// unexpectedly. A process that is already starting, running, or stopping under
// the same ID is not started twice.
func (m *Manager) Start(ctx context.Context, spec ProcessSpec) (Status, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.initErr != nil {
		return Status{State: model.StatusError, LastError: m.initErr.Error()}, m.initErr
	}
	if err := validateSpec(spec); err != nil {
		return Status{State: model.StatusError, LastError: err.Error()}, err
	}
	spec = cloneSpec(spec)

	var p *proc
	m.mu.Lock()
	for {
		old, ok := m.procs[spec.ID]
		if !ok {
			p = newProc(spec)
			m.procs[spec.ID] = p
			m.mu.Unlock()
			break
		}
		st := old.snapshot()
		switch st.State {
		case model.StatusStarting, model.StatusRunning, model.StatusStopping:
			m.mu.Unlock()
			return st, fmt.Errorf("%w: %q", errAlreadyRunning, spec.ID)
		}
		// Let the previous supervisor finish removing its pid file before the
		// replacement writes a new one with the same name. Do not hold the
		// manager mutex while waiting: that would freeze unrelated sites.
		done := old.doneCh
		m.mu.Unlock()
		<-done
		m.mu.Lock()
		if current, ok := m.procs[spec.ID]; ok && current == old {
			delete(m.procs, spec.ID)
		}
	}

	go m.supervise(p)

	select {
	case out := <-p.resultCh:
		return out.status, out.err
	case <-ctx.Done():
		// The supervisor owns cleanup; ask it to stop and return without
		// waiting for the grace period.
		p.requestStop()
		return p.snapshot(), ctx.Err()
	}
}

// Stop terminates the process group for id. It sends SIGTERM, waits StopGrace,
// then sends SIGKILL. Stopping an unknown or already stopped id succeeds so
// callers can reconcile idempotently.
func (m *Manager) Stop(ctx context.Context, id string) error {
	return m.stop(ctx, id, syscall.SIGTERM)
}

// StopGraceful stops id with SIGQUIT first, then escalates to SIGKILL after
// StopGrace. PHP-FPM uses SIGQUIT to drain in-flight requests.
func (m *Manager) StopGraceful(ctx context.Context, id string) error {
	return m.stop(ctx, id, syscall.SIGQUIT)
}

func (m *Manager) stop(ctx context.Context, id string, signal syscall.Signal) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p := m.lookup(id)
	if p == nil {
		return nil
	}

	p.mu.Lock()
	if p.status.State == model.StatusStopped {
		p.mu.Unlock()
		return nil
	}
	p.stopping = true
	p.status.State = model.StatusStopping
	p.mu.Unlock()

	// The supervisor owns every signal. It observes stopCh while starting,
	// waiting for readiness, waiting for exit, and backing off, then terminates
	// the process group and polls until it is gone. Delegating here avoids any
	// chance of signalling a recycled pid after a crash.
	p.requestStopWithSignal(signal)

	wait := m.stopGrace() + killWait + cleanupKillWait + time.Second
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-p.doneCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("supervisor: stop %q timed out after %s", id, wait)
	}

	// The supervisor marks a stopped process before closing doneCh. A crashed
	// or errored process that is being retired here still needs the transition.
	if p.snapshot().State != model.StatusStopped {
		m.finishStopped(p)
	}
	return nil
}

// StopAll terminates every process currently owned by the manager. Each
// process gets its own stop budget, so one stubborn process cannot consume the
// time available to the others. It is safe to call with a manager that owns no
// processes.
func (m *Manager) StopAll(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m == nil {
		return nil
	}

	m.mu.Lock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Strings(ids)

	var errs []error
	for _, id := range ids {
		stopCtx, cancel := context.WithTimeout(ctx, m.stopBudget())
		err := m.Stop(stopCtx, id)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("supervisor: stop all %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// stopBudget bounds one Stop call, including SIGTERM grace and SIGKILL.
func (m *Manager) stopBudget() time.Duration {
	return m.stopGrace() + killWait + cleanupKillWait + 2*time.Second
}

// Status returns a snapshot for id. The boolean is false when id is not known
// to this manager. Crashed and stopped processes remain queryable until a new
// Start replaces them.
func (m *Manager) Status(id string) (Status, bool) {
	p := m.lookup(id)
	if p == nil {
		return Status{}, false
	}
	return p.snapshot(), true
}

func newProc(spec ProcessSpec) *proc {
	return &proc{
		id:         spec.ID,
		spec:       spec,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		resultCh:   make(chan startOutcome, 1),
		status:     Status{State: model.StatusStarting},
		stopSignal: syscall.SIGTERM,
	}
}

func (m *Manager) lookup(id string) *proc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.procs[id]
}

func (m *Manager) stopGrace() time.Duration {
	if m.StopGrace < 0 {
		return 0
	}
	return m.StopGrace
}

func (p *proc) snapshot() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *proc) isStopping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopping
}

// requestStop closes stopCh exactly once. The supervisor observes the channel
// while waiting for readiness, between restarts, and while backing off.
func (p *proc) requestStop() {
	p.requestStopWithSignal(syscall.SIGTERM)
}

func (p *proc) requestStopWithSignal(signal syscall.Signal) {
	p.mu.Lock()
	p.stopping = true
	if signal != 0 {
		p.stopSignal = signal
	}
	p.mu.Unlock()
	p.stopOnce.Do(func() { close(p.stopCh) })
}

func (p *proc) terminationSignal() syscall.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopSignal == 0 {
		return syscall.SIGTERM
	}
	return p.stopSignal
}

func (p *proc) deliver(out startOutcome) {
	select {
	case p.resultCh <- out:
	default:
	}
}

// keepPIDFile records that the process group could not be confirmed gone, so
// terminal transitions must leave the pid file for a later cleanup pass.
func (p *proc) keepPIDFile() {
	p.mu.Lock()
	p.pidFileLeaked = true
	p.mu.Unlock()
}

// clearChild drops c once its group has been cleaned up. It only clears the
// matching pointer so a newer spawn is never lost.
func (p *proc) clearChild(c *child) {
	p.mu.Lock()
	if p.child == c {
		p.child = nil
	}
	p.mu.Unlock()
}

func validateSpec(spec ProcessSpec) error {
	if err := validateID(spec.ID); err != nil {
		return err
	}
	if len(spec.Argv) == 0 || strings.TrimSpace(spec.Argv[0]) == "" {
		return fmt.Errorf("supervisor: %q has an empty argv", spec.ID)
	}
	return nil
}

// validateID keeps ids usable as pid-file names and prevents path traversal.
func validateID(id string) error {
	switch {
	case id == "":
		return errors.New("supervisor: empty process id")
	case id == "." || id == "..":
		return fmt.Errorf("supervisor: invalid process id %q", id)
	case strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, 0):
		return fmt.Errorf("supervisor: invalid process id %q", id)
	case filepath.Base(id) != id:
		return fmt.Errorf("supervisor: invalid process id %q", id)
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return fmt.Errorf("supervisor: invalid process id %q", id)
		}
	}
	return nil
}

// cloneSpec detaches the caller's slices and maps from the long-lived spec.
func cloneSpec(spec ProcessSpec) ProcessSpec {
	spec.Argv = append([]string(nil), spec.Argv...)
	if spec.Env != nil {
		env := make(map[string]string, len(spec.Env))
		for k, v := range spec.Env {
			env[k] = v
		}
		spec.Env = env
	}
	if spec.Restart.Backoff != nil {
		spec.Restart.Backoff = append([]time.Duration(nil), spec.Restart.Backoff...)
	}
	return spec
}
