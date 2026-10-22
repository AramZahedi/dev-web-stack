package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
			if st.Restarts != 2 {
				t.Fatalf("restarts = %d, want 2", st.Restarts)
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
	rec := pidRecord{ID: "stale", PID: unrelated.Process.Pid, StartTicks: ticks + 1}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "stale.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir, slog.Default())
	if err := m.CleanupOrphans(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-unrelated.Process.Pid, 0); err != nil {
		t.Fatalf("recycled pid process was killed: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale pid file still exists: err = %v", err)
	}
}
