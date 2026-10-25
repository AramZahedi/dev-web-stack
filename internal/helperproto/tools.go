package helperproto

import "errors"

// Tool routes exposed by the helper.
const (
	PathToolsStatus  = "/v1/tools/status"
	PathToolsInstall = "/v1/tools/install"
	PathToolsRemove  = "/v1/tools/remove"
)

// Managed tool ids. Requests carry an id from this fixed set only; they never
// carry host paths or commands.
const (
	ToolPHPDefault = "php-default"
	ToolComposer   = "composer"
)

// CodeToolConflict reports an existing foreign file that would be replaced or
// removed by a tool operation.
const CodeToolConflict = "TOOL_CONFLICT"

// ErrToolConflict is returned when a tool operation would overwrite or remove
// an unmanaged file.
var ErrToolConflict = errors.New("helper: tool conflict")

// ToolStatus describes one managed native tool.
type ToolStatus struct {
	ID          string `json:"id"`
	Installed   bool   `json:"installed"`
	Managed     bool   `json:"managed"`
	WrapperPath string `json:"wrapper_path,omitempty"`
	Target      string `json:"target,omitempty"`
	Version     string `json:"version,omitempty"`
	Conflict    string `json:"conflict,omitempty"`
	Backup      string `json:"backup,omitempty"`
}

// ToolStatusResponse lists both managed tools.
type ToolStatusResponse struct {
	Tools []ToolStatus `json:"tools"`
}

// ToolInstallRequest asks to link a managed tool into the system bin
// directory. Force permits replacing and backing up a foreign file.
type ToolInstallRequest struct {
	ID    string `json:"id"`
	Force bool   `json:"force,omitempty"`
}

// ToolRemoveRequest asks to remove one managed tool's system link and files.
type ToolRemoveRequest struct {
	ID string `json:"id"`
}

// ToolResponse returns the resulting status of one tool operation.
type ToolResponse struct {
	Tool ToolStatus `json:"tool"`
}
