package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// child is one OS process started for a proc. waitErr is written by the waiter
// goroutine before exitedCh is closed; readers only inspect it after receiving
// from exitedCh.
type child struct {
	pid       int
	startedAt time.Time
	exitedCh  chan struct{}
	waitErr   error
}

// supervise owns one process's lifecycle: it starts the process, waits for
// readiness, waits for exit, and applies the restart policy. It is the only
// goroutine that advances a proc's lifecycle, so transitions are serialized.
func (m *Manager) supervise(p *proc) {
	defer close(p.doneCh)

	first := true
	var failures []time.Time

	for {
		if p.isStopping() {
			m.finishStopped(p)
			if first {
				p.deliver(startOutcome{status: p.snapshot(), err: errStoppedDuringStart})
			}
			return
		}

		c, err := m.startChild(p)
		if err != nil {
			if p.isStopping() || errors.Is(err, errStoppedDuringStart) {
				m.finishStopped(p)
				if first {
					p.deliver(startOutcome{status: p.snapshot(), err: errStoppedDuringStart})
				}
				return
			}
			m.recordFailure(p, err)
			if first {
				m.finishError(p, err)
				p.deliver(startOutcome{status: p.snapshot(), err: err})
				return
			}
			if !m.retryOrCrash(p, &failures, err) {
				return
			}
			continue
		}

		if err := m.awaitReady(p, c); err != nil {
			m.terminateChild(p, c)
			if p.isStopping() || errors.Is(err, errStoppedDuringStart) {
				m.finishStopped(p)
				if first {
					p.deliver(startOutcome{status: p.snapshot(), err: errStoppedDuringStart})
				}
				return
			}
			m.recordFailure(p, err)
			if first {
				m.finishError(p, err)
				p.deliver(startOutcome{status: p.snapshot(), err: err})
				return
			}
			if !m.retryOrCrash(p, &failures, err) {
				return
			}
			continue
		}

		m.markRunning(p, c)
		if first {
			first = false
			p.deliver(startOutcome{status: p.snapshot()})
		}

		select {
		case <-c.exitedCh:
		case <-p.stopCh:
			// Stop or context cancellation arrived while the process was
			// running. Terminate it here so requestStop alone is sufficient.
			m.terminateChild(p, c)
		}
		if p.isStopping() {
			m.finishStopped(p)
			if err := m.ensureGroupGone(c.pid, cleanupKillWait); err != nil {
				m.logger.Warn("supervisor: process group did not exit", "id", p.id, "pid", c.pid, "error", err)
			}
			return
		}

		exitErr := c.waitErr
		if exitErr == nil {
			exitErr = fmt.Errorf("supervisor: %q exited unexpectedly", p.id)
		}
		m.recordFailure(p, exitErr)
		m.logger.Warn("supervisor: process exited", "id", p.id, "pid", c.pid, "error", exitErr)
		if !m.retryOrCrash(p, &failures, exitErr) {
			return
		}
	}
}

// startChild launches one process in a new process group, records its start
// time in a pid file, and starts the goroutine that reaps it.
func (m *Manager) startChild(p *proc) (*child, error) {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return nil, errStoppedDuringStart
	}
	spec := p.spec
	p.mu.Unlock()

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = mergeEnv(os.Environ(), spec.Env)
	cmd.Stdout = nonNilWriter(spec.Stdout)
	cmd.Stderr = nonNilWriter(spec.Stderr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("supervisor: start %q: %w", spec.ID, err)
	}
	pid := cmd.Process.Pid

	ticks, err := procStartTicks(pid)
	if err != nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
		return nil, fmt.Errorf("supervisor: record start time for %q: %w", spec.ID, err)
	}

	c := &child{
		pid:       pid,
		startedAt: time.Now(),
		exitedCh:  make(chan struct{}),
	}
	go func() {
		c.waitErr = cmd.Wait()
		close(c.exitedCh)
	}()

	if err := m.writePIDFile(spec.ID, pid, ticks); err != nil {
		m.terminateChild(p, c)
		return nil, err
	}

	p.mu.Lock()
	p.child = c
	if !p.stopping {
		p.status.State = model.StatusStarting
		p.status.PID = pid
		p.status.StartedAt = c.startedAt
	}
	stopping := p.stopping
	p.mu.Unlock()

	// Stop may have raced with the spawn and seen no child yet. It is waiting
	// on doneCh, so terminate here rather than leaving the process behind.
	if stopping {
		m.terminateChild(p, c)
		return nil, errStoppedDuringStart
	}
	return c, nil
}

// awaitReady probes readiness. An empty TCPAddr is ready immediately; callers
// that need to accept connections set one and a timeout.
func (m *Manager) awaitReady(p *proc, c *child) error {
	addr := strings.TrimSpace(p.spec.Ready.TCPAddr)
	if addr == "" {
		if p.isStopping() {
			return errStoppedDuringStart
		}
		return nil
	}
	timeout := p.spec.Ready.Timeout
	if timeout <= 0 {
		timeout = defaultReadyTimeout
	}
	deadline := time.Now().Add(timeout)

	var lastErr error
	for {
		if p.isStopping() {
			return errStoppedDuringStart
		}
		select {
		case <-c.exitedCh:
			return fmt.Errorf("supervisor: %q exited before becoming ready: %s", p.id, exitMessage(c.waitErr))
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr == nil {
				lastErr = errors.New("no connection")
			}
			return fmt.Errorf("%w: %s: %v", ErrReadyTimeout, addr, lastErr)
		}
		dialTimeout := readyPollInterval
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err

		remaining = time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%w: %s: %v", ErrReadyTimeout, addr, lastErr)
		}
		wait := readyPollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-c.exitedCh:
			timer.Stop()
			return fmt.Errorf("supervisor: %q exited before becoming ready: %s", p.id, exitMessage(c.waitErr))
		case <-p.stopCh:
			timer.Stop()
			return errStoppedDuringStart
		}
		timer.Stop()
	}
}

// terminateChild stops one process group, escalating from SIGTERM to SIGKILL
// after StopGrace. It always waits for the direct child to be reaped.
func (m *Manager) terminateChild(p *proc, c *child) {
	if c == nil {
		return
	}
	if !channelClosed(c.exitedCh) {
		_ = syscall.Kill(-c.pid, syscall.SIGTERM)
		if !waitChannel(nil, c.exitedCh, m.stopGrace()) {
			_ = syscall.Kill(-c.pid, syscall.SIGKILL)
			if !waitChannel(nil, c.exitedCh, killWait) {
				m.logger.Error("supervisor: process did not exit after SIGKILL", "id", p.id, "pid", c.pid)
			}
		}
	}
	if err := m.ensureGroupGone(c.pid, cleanupKillWait); err != nil {
		m.logger.Warn("supervisor: process group did not exit", "id", p.id, "pid", c.pid, "error", err)
	}
}

// retryOrCrash records a failure and either sleeps through the next backoff
// (returning true to keep supervising) or marks the process crashed/stopped.
func (m *Manager) retryOrCrash(p *proc, failures *[]time.Time, cause error) bool {
	now := time.Now()
	*failures = append(*failures, now)
	if window := p.spec.Restart.Window; window > 0 {
		cutoff := now.Add(-window)
		i := 0
		for i < len(*failures) && (*failures)[i].Before(cutoff) {
			i++
		}
		if i > 0 {
			*failures = append([]time.Time(nil), (*failures)[i:]...)
		}
	}

	maxRestarts := p.spec.Restart.MaxRestarts
	if maxRestarts <= 0 || len(*failures) > maxRestarts {
		m.finishCrashed(p, cause)
		return false
	}

	p.mu.Lock()
	p.restarts++
	p.status.Restarts = p.restarts
	p.status.State = model.StatusStarting
	p.status.PID = 0
	p.status.StartedAt = time.Time{}
	restarts := p.restarts
	p.mu.Unlock()

	delay := backoffDelay(p.spec.Restart.Backoff, len(*failures)-1)
	m.logger.Warn("supervisor: restarting process", "id", p.id, "restart", restarts, "delay", delay.String())
	if !sleepOrStop(delay, p.stopCh) {
		m.finishStopped(p)
		return false
	}
	return true
}

func (m *Manager) recordFailure(p *proc, err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.status.LastError = err.Error()
	p.mu.Unlock()
}

func (m *Manager) markRunning(p *proc, c *child) {
	p.mu.Lock()
	p.status.State = model.StatusRunning
	p.status.PID = c.pid
	p.status.StartedAt = c.startedAt
	p.status.LastError = ""
	st := p.status
	p.mu.Unlock()
	m.logger.Info("supervisor: process is ready", "id", p.id, "pid", c.pid, "restarts", st.Restarts)
}

func (m *Manager) finishStopped(p *proc) {
	p.mu.Lock()
	p.status.State = model.StatusStopped
	p.status.PID = 0
	p.status.StartedAt = time.Time{}
	p.mu.Unlock()
	m.removePIDFile(p.id)
}

func (m *Manager) finishCrashed(p *proc, cause error) {
	p.mu.Lock()
	p.status.State = model.StatusCrashed
	p.status.PID = 0
	p.status.StartedAt = time.Time{}
	if cause != nil {
		p.status.LastError = cause.Error()
	}
	st := p.status
	p.mu.Unlock()
	m.removePIDFile(p.id)
	m.logger.Error("supervisor: process crashed", "id", p.id, "restarts", st.Restarts, "error", st.LastError)
}

func (m *Manager) finishError(p *proc, err error) {
	p.mu.Lock()
	p.status.State = model.StatusError
	p.status.PID = 0
	p.status.StartedAt = time.Time{}
	if err != nil {
		p.status.LastError = err.Error()
	}
	p.mu.Unlock()
	m.removePIDFile(p.id)
	m.logger.Error("supervisor: process failed to start", "id", p.id, "error", err)
}

func (m *Manager) pidPath(id string) string {
	return filepath.Join(m.runDir, id+".json")
}

// writePIDFile atomically records the process ID and kernel start time so a
// later daemon can distinguish our process from a recycled PID.
func (m *Manager) writePIDFile(id string, pid int, ticks uint64) error {
	rec := pidRecord{ID: id, PID: pid, StartTicks: ticks}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("supervisor: encode pid file for %q: %w", id, err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(m.runDir, "."+id+"-*.tmp")
	if err != nil {
		return fmt.Errorf("supervisor: create pid file for %q: %w", id, err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("supervisor: write pid file for %q: %w", id, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("supervisor: write pid file for %q: %w", id, err)
	}
	if err := os.Rename(tmpName, m.pidPath(id)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("supervisor: install pid file for %q: %w", id, err)
	}
	return nil
}

func (m *Manager) removePIDFile(id string) {
	if err := os.Remove(m.pidPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logger.Warn("supervisor: remove pid file", "id", id, "error", err)
	}
}

// mergeEnv appends overrides to the daemon environment, replacing matching
// keys. A nil or empty override map inherits the environment unchanged.
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	env := make([]string, 0, len(base)+len(overrides))
	replaced := make(map[string]bool, len(overrides))
	for _, entry := range base {
		key := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key = entry[:i]
		}
		value, ok := overrides[key]
		if !ok {
			env = append(env, entry)
			continue
		}
		if replaced[key] {
			continue
		}
		env = append(env, key+"="+value)
		replaced[key] = true
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		if !replaced[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func nonNilWriter(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func backoffDelay(backoff []time.Duration, attempt int) time.Duration {
	if len(backoff) == 0 || attempt < 0 {
		return 0
	}
	if attempt >= len(backoff) {
		attempt = len(backoff) - 1
	}
	if backoff[attempt] < 0 {
		return 0
	}
	return backoff[attempt]
}

func sleepOrStop(delay time.Duration, stop <-chan struct{}) bool {
	if delay <= 0 {
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	}
}

// waitChannel waits for ch to close or timeout. A nil context is allowed.
func waitChannel(ctx context.Context, ch <-chan struct{}, timeout time.Duration) bool {
	if channelClosed(ch) {
		return true
	}
	if timeout < 0 {
		timeout = 0
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	case <-done:
		return false
	}
}

func channelClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func exitMessage(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
