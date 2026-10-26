//go:build windows

package client

import (
	"testing"
)

// TestNewAcceptsNamedPipeWithoutFilesystemCheck proves the CLI does not try to
// os.Stat a named pipe. The first request, not client construction, reports a
// missing daemon on Windows.
func TestNewAcceptsNamedPipeWithoutFilesystemCheck(t *testing.T) {
	t.Setenv("DEVSTACK_HOME", t.TempDir())
	c, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = c.Close()
}
