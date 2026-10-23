package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ctx is shared by the tests that only need a non-cancelled context.
var ctx = context.Background()

func TestStartStopsProcessGroup(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	st, err := m.Start(ctx, ProcessSpec{ID: "x", Argv: []string{"sh", "-c", "sleep 30"}, Ready: ReadyCheck{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(-st.PID, 0) == nil {
		t.Fatal("process group still alive")
	}
}

func TestReadinessTimeoutKillsProcess(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	_, err := m.Start(ctx, ProcessSpec{ID: "x", Argv: []string{"sleep", "30"}, Ready: ReadyCheck{TCPAddr: "127.0.0.1:1", Timeout: 50 * time.Millisecond}})
	if !errors.Is(err, ErrReadyTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestCleanupOrphansKillsOnlyOurProcesses(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, slog.Default())
	st, err := m.Start(ctx, ProcessSpec{ID: "orphan", Argv: []string{"sleep", "30"}, Ready: ReadyCheck{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(dir, slog.Default())
	if err := m2.CleanupOrphans(); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(-st.PID, 0) == nil {
		t.Fatal("orphan survived cleanup")
	}
	unrelated := exec.Command("sleep", "30")
	unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-unrelated.Process.Pid, syscall.SIGKILL)
	if err := m2.CleanupOrphans(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-unrelated.Process.Pid, 0); err != nil {
		t.Fatal("unrelated process was killed")
	}
}

func TestStopAllStopsEveryProcessWithIndividualBudgets(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	m.StopGrace = 50 * time.Millisecond
	ids := []string{"slow-a", "slow-b", "slow-c"}
	pids := make(map[string]int, len(ids))
	for _, id := range ids {
		st, err := m.Start(ctx, ProcessSpec{
			ID:    id,
			Argv:  []string{"sh", "-c", "trap '' TERM; while :; do sleep 1; done"},
			Ready: ReadyCheck{Timeout: time.Second},
		})
		if err != nil {
			t.Fatal(err)
		}
		pids[id] = st.PID
	}
	// Let each shell install its TERM trap before shutdown starts.
	time.Sleep(150 * time.Millisecond)

	started := time.Now()
	if err := m.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("StopAll took %s for %d processes", elapsed, len(ids))
	}
	for _, id := range ids {
		status, ok := m.Status(id)
		if !ok || status.State != model.StatusStopped {
			t.Fatalf("%s status = %+v (known=%v), want stopped", id, status, ok)
		}
		if err := syscall.Kill(-pids[id], 0); err == nil {
			t.Fatalf("%s process group is still alive", id)
		}
	}
}

func TestStatusReportsRunningThenStopped(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	st, err := m.Start(ctx, ProcessSpec{ID: "ok", Argv: []string{"sh", "-c", "sleep 30"}, Ready: ReadyCheck{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.Status("ok")
	if !ok {
		t.Fatal("status not found")
	}
	if got.State != model.StatusRunning || got.PID != st.PID || got.PID <= 0 {
		t.Fatalf("status = %+v", got)
	}
	if err := m.Stop(ctx, "ok"); err != nil {
		t.Fatal(err)
	}
	got, ok = m.Status("ok")
	if !ok {
		t.Fatal("status not found after stop")
	}
	if got.State != model.StatusStopped || got.PID != 0 {
		t.Fatalf("status after stop = %+v", got)
	}
}

func TestRestartsThenCrashes(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	_, err := m.Start(ctx, ProcessSpec{
		ID:    "flap",
		Argv:  []string{"sh", "-c", "exit 1"},
		Ready: ReadyCheck{Timeout: time.Second},
		Restart: RestartPolicy{
			MaxRestarts: 2,
			Window:      time.Minute,
			Backoff:     []time.Duration{10 * time.Millisecond, 10 * time.Millisecond},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx, "flap") })

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, ok := m.Status("flap")
		if !ok {
			t.Fatal("status not found")
		}
		if st.State == model.StatusCrashed {
			if st.Restarts != 1 {
				t.Fatalf("restarts = %d, want 1 (crash on the second failure)", st.Restarts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want crashed", st.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestZeroRestartPolicyDoesNotRestart(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	if _, err := m.Start(ctx, ProcessSpec{
		ID:    "once",
		Argv:  []string{"sh", "-c", "sleep 0.05; exit 1"},
		Ready: ReadyCheck{Timeout: time.Second},
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, ok := m.Status("once")
		if !ok {
			t.Fatal("status not found")
		}
		if st.State == model.StatusCrashed {
			if st.Restarts != 0 {
				t.Fatalf("restarts = %d, want 0", st.Restarts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want crashed", st.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStopEscalatesToSIGKILL(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	m.StopGrace = 100 * time.Millisecond
	st, err := m.Start(ctx, ProcessSpec{ID: "stubborn", Argv: []string{"sh", "-c", "trap '' TERM; while :; do :; done"}, Ready: ReadyCheck{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	// Give the shell time to install the TERM trap before stopping it.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := m.Stop(ctx, "stubborn"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("Stop returned after %s, before the grace period elapsed", elapsed)
	}
	if syscall.Kill(-st.PID, 0) == nil {
		t.Fatal("process group still alive after SIGKILL escalation")
	}
}

func TestNilStdioDoesNotPanic(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	st, err := m.Start(ctx, ProcessSpec{ID: "nil-stdio", Argv: []string{"sh", "-c", "echo hello; sleep 0.2"}, Ready: ReadyCheck{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if st.PID <= 0 {
		t.Fatalf("pid = %d", st.PID)
	}
	if err := m.Stop(ctx, "nil-stdio"); err != nil {
		t.Fatal(err)
	}
}

func TestCrashesOnFifthFailureWithinWindow(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	starts := filepath.Join(t.TempDir(), "starts")
	_, err := m.Start(ctx, ProcessSpec{
		ID:    "fifth",
		Argv:  []string{"sh", "-c", `echo x >> "$STARTS"; exit 1`},
		Env:   map[string]string{"STARTS": starts},
		Ready: ReadyCheck{Timeout: time.Second},
		Restart: RestartPolicy{
			MaxRestarts: 5,
			Window:      5 * time.Minute,
			Backoff: []time.Duration{
				time.Millisecond, time.Millisecond, time.Millisecond,
				time.Millisecond, time.Millisecond, time.Millisecond,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx, "fifth") })

	st := waitForStatus(t, m, "fifth", model.StatusCrashed)
	if st.Restarts != 4 {
		t.Fatalf("restarts = %d, want 4 (crash on the fifth failure)", st.Restarts)
	}
	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x\n"); got != 5 {
		t.Fatalf("process starts = %d, want 5", got)
	}
}

func TestUnexpectedExitKillsRemainingProcessGroup(t *testing.T) {
	runDir := t.TempDir()
	m := NewManager(runDir, slog.Default())
	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	st, err := m.Start(ctx, ProcessSpec{
		ID:    "bg",
		Argv:  []string{"sh", "-c", `sleep 30 & echo $! > "$GRANDCHILD_PID"; exit 0`},
		Env:   map[string]string{"GRANDCHILD_PID": pidPath},
		Ready: ReadyCheck{Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	groupPID := st.PID
	defer func() { _ = syscall.Kill(-groupPID, syscall.SIGKILL) }()

	grandchild := waitForPID(t, pidPath)
	pgrp, err := procPgrp(grandchild)
	if err != nil {
		t.Fatalf("grandchild pgrp: %v", err)
	}
	if pgrp != groupPID {
		t.Fatalf("grandchild pgrp = %d, want %d", pgrp, groupPID)
	}

	st = waitForStatus(t, m, "bg", model.StatusCrashed)
	if st.Restarts != 0 {
		t.Fatalf("restarts = %d, want 0", st.Restarts)
	}
	if syscall.Kill(-groupPID, 0) == nil {
		t.Fatal("descendant process group survived the unexpected exit")
	}
	if _, err := os.Stat(filepath.Join(runDir, "bg.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pid file still exists after the group was killed: %v", err)
	}
}

func TestCleanupOrphansKeepsPidFileWhenGroupOutlivesLeader(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	leader := exec.Command("sh", "-c", `sleep 30 & echo $! > "$GRANDCHILD_PID"; exit 0`)
	leader.Env = append(os.Environ(), "GRANDCHILD_PID="+pidPath)
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	leaderPID := leader.Process.Pid
	ticks, err := procStartTicks(leaderPID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-leaderPID, syscall.SIGKILL)
		_ = waitGroupGone(leaderPID, 2*time.Second)
	}()

	path := filepath.Join(dir, "orphan.json")
	writePIDRecordForTest(t, path, pidRecord{ID: "orphan", PID: leaderPID, StartTicks: ticks})
	if err := leader.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = waitForPID(t, pidPath)
	if err := syscall.Kill(leaderPID, 0); err == nil {
		t.Fatal("leader is still alive")
	}
	if err := syscall.Kill(-leaderPID, 0); err != nil {
		t.Fatalf("process group did not outlive its leader: %v", err)
	}

	m := NewManager(dir, slog.Default())
	err = m.CleanupOrphans()
	if err == nil {
		t.Fatal("CleanupOrphans returned nil for a group that outlived its leader")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("pid file was removed while its group is alive: %v", statErr)
	}
	if err := syscall.Kill(-leaderPID, 0); err != nil {
		t.Fatal("surviving process group was killed")
	}
}

func TestCleanupOrphansSkipsNonLeaderRecord(t *testing.T) {
	dir := t.TempDir()
	nonLeader := exec.Command("sleep", "30")
	if err := nonLeader.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = nonLeader.Process.Kill()
		_ = nonLeader.Wait()
	}()
	ticks, err := procStartTicks(nonLeader.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgrp, err := procPgrp(nonLeader.Process.Pid); err != nil || pgrp == nonLeader.Process.Pid {
		t.Fatalf("test process is unexpectedly a group leader (pgrp=%d, err=%v)", pgrp, err)
	}

	path := filepath.Join(dir, "nonleader.json")
	writePIDRecordForTest(t, path, pidRecord{ID: "nonleader", PID: nonLeader.Process.Pid, StartTicks: ticks})

	m := NewManager(dir, slog.Default())
	if err := m.CleanupOrphans(); err == nil {
		t.Fatal("CleanupOrphans returned nil for a non-leader pid")
	}
	if err := syscall.Kill(nonLeader.Process.Pid, 0); err != nil {
		t.Fatalf("non-leader process was killed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pid file was removed for a non-leader record: %v", err)
	}
}

func TestStopDoesNotSignalRecycledPID(t *testing.T) {
	m := NewManager(t.TempDir(), slog.Default())
	m.StopGrace = 10 * time.Millisecond
	if _, err := m.Start(ctx, ProcessSpec{
		ID:    "crash",
		Argv:  []string{"sh", "-c", "exit 1"},
		Ready: ReadyCheck{Timeout: time.Second},
	}); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, m, "crash", model.StatusCrashed)

	p := m.lookup("crash")
	p.mu.Lock()
	cleared := p.child == nil
	p.mu.Unlock()
	if !cleared {
		t.Fatal("crashed process retained its child after exit")
	}

	unrelated := exec.Command("sleep", "30")
	unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-unrelated.Process.Pid, syscall.SIGKILL)
		_ = unrelated.Wait()
	}()

	// Simulate the pre-fix state where a crashed proc still pointed at an old
	// PID that has since been reused as an unrelated group.
	p.mu.Lock()
	p.child = &child{pid: unrelated.Process.Pid, startedAt: time.Now(), exitedCh: make(chan struct{})}
	p.status.State = model.StatusCrashed
	p.mu.Unlock()

	if err := m.Stop(ctx, "crash"); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-unrelated.Process.Pid, 0); err != nil {
		t.Fatalf("Stop signalled a recycled pid group: %v", err)
	}
}

func TestSignalableGroupLeaderGuards(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		if err := signalableGroupLeader(pid); err == nil {
			t.Fatalf("accepted reserved pid %d", pid)
		}
	}

	nonLeader := exec.Command("sleep", "30")
	if err := nonLeader.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = nonLeader.Process.Kill()
		_ = nonLeader.Wait()
	})
	if err := signalableGroupLeader(nonLeader.Process.Pid); err == nil {
		t.Fatalf("accepted non-leader pid %d", nonLeader.Process.Pid)
	}

	leader := exec.Command("sleep", "30")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-leader.Process.Pid, syscall.SIGKILL)
		_ = leader.Wait()
	})
	if err := signalableGroupLeader(leader.Process.Pid); err != nil {
		t.Fatalf("rejected group leader: %v", err)
	}
}

func waitForStatus(t *testing.T, m *Manager, id string, want model.SiteStatus) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, ok := m.Status(id)
		if !ok {
			t.Fatalf("status %q not found", id)
		}
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want %q", st.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
			lastErr = convErr
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("read pid file %s: %v", path, lastErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writePIDRecordForTest(t *testing.T, path string, rec pidRecord) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupOrphansSkipsRecycledPID(t *testing.T) {
	dir := t.TempDir()
	unrelated := exec.Command("sleep", "30")
	unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-unrelated.Process.Pid, syscall.SIGKILL)
		_ = unrelated.Wait()
	}()

	ticks, err := procStartTicks(unrelated.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "stale.json")
	writePIDRecordForTest(t, path, pidRecord{ID: "stale", PID: unrelated.Process.Pid, StartTicks: ticks + 1})

	m := NewManager(dir, slog.Default())
	err = m.CleanupOrphans()
	if err == nil {
		t.Fatal("CleanupOrphans returned nil for a live group whose pid was reused")
	}
	if err := syscall.Kill(-unrelated.Process.Pid, 0); err != nil {
		t.Fatalf("recycled pid process was killed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pid file was removed while its group is alive: %v", err)
	}
}
