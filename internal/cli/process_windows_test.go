//go:build windows

package cli

import (
	"os"
	"testing"
)

func TestProcessRunningCurrentAndMissing(t *testing.T) {
	if !processRunning(os.Getpid()) {
		t.Fatal("current process reported as stopped")
	}
	if processRunning(0) {
		t.Fatal("pid 0 reported as running")
	}
}
