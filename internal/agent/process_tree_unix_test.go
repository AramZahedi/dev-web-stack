//go:build !windows

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// A timed-out build/exec must kill the whole process group, not only the
// /bin/sh that was started. npm/pnpm/vite children otherwise keep running and
// holding the project directory open.
func TestExecSiteKillsGrandchildrenOnTimeout(t *testing.T) {
	root := t.TempDir()
	agentRuntime, err := NewRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{ID: "exec-tree", Kind: "static", BackendPath: project, DocumentRoot: "."}
	if _, err := agentRuntime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	response, err := agentRuntime.ExecSite(context.Background(), spec.ID, agentproto.SiteExecRequest{
		Argv:      []string{"/bin/sh", "-c", "sleep 30 & echo $! > child.pid; wait"},
		TimeoutMS: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ExitCode == 0 {
		t.Fatal("timed-out exec reported success")
	}
	data, err := os.ReadFile(filepath.Join(project, "child.pid"))
	if err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("grandchild pid = %q", data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the command timeout", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
