//go:build windows

package platform

import (
	"context"
	"testing"
)

func TestConfigureProcessHidesWindow(t *testing.T) {
	cmd := NewCommand(context.Background(), "cmd.exe", "/c", "exit 0")
	ConfigureProcess(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("process attr = %+v", cmd.SysProcAttr)
	}
}
