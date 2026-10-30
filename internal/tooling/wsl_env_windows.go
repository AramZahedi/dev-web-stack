//go:build windows

package tooling

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

// secureWSLEnvFile applies an explicit protected DACL so the temp env file is
// readable only by the current user and SYSTEM, not inherited %TEMP% ACLs.
func secureWSLEnvFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("tooling: WSL environment file path is empty")
	}
	sid, err := ipc.CurrentSID()
	if err != nil {
		return fmt.Errorf("tooling: read current user SID: %w", err)
	}
	sddl, err := ipc.PipeSDDL(sid, false)
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("tooling: build WSL environment file DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("tooling: read WSL environment file DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("tooling: protect WSL environment file: %w", err)
	}
	return nil
}
