//go:build !windows

package platform

import (
	"context"
	"testing"
)

func TestConfigureProcessSetsProcessGroup(t *testing.T) {
	cmd := NewCommand(context.Background(), "true")
	ConfigureProcess(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("process attr = %+v", cmd.SysProcAttr)
	}
}
