//go:build !windows

package agent

import (
	"context"
	"net"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestKillFPMProcessSkipsReapedChild(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep fixture: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	done := make(chan struct{})
	close(done)
	killFPMProcess(cmd, pid, done)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("reaped child was signaled (pid reuse guard failed): %v", err)
	}
}

func TestSimulatedFPMStopDoesNotSignalAgent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	proc := &fpmProcess{listener: listener, cancel: cancel, done: done, pid: 0}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
		close(done)
	}()
	proc.stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("simulated FPM did not stop")
	}
}
