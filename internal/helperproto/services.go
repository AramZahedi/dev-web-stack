package helperproto

// Service endpoints exposed by the helper.
const (
	PathServicesStatus     = "/v1/services/status"
	PathServicesInstall    = "/v1/services/install"
	PathServicesActivate   = "/v1/services/activate"
	PathServicesDeactivate = "/v1/services/deactivate"
	PathServicesUninstall  = "/v1/services/uninstall"
	PathServicesLogs       = "/v1/services/logs"
	PathServicesRestart    = "/v1/services/restart"
	PathServicesRotate     = "/v1/services/credentials/rotate"
	PathServicesValidate   = "/v1/services/validate"
	PathServicesReload     = "/v1/services/reload"
)

// ServicePortStatus describes the port mapping of an exposed service container.
type ServicePortStatus struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"`
	Loopback  bool   `json:"loopback"`
}

// ServiceStatus reports the operational and container state of a service.
type ServiceStatus struct {
	ID          string              `json:"id"`
	State       string              `json:"state"`
	Active      bool                `json:"active"`
	Installed   bool                `json:"installed"`
	Image       string              `json:"image"`
	ConfigPath  string              `json:"config_path"`
	DataPath    string              `json:"data_path"`
	ContainerID string              `json:"container_id,omitempty"`
	Health      string              `json:"health,omitempty"`
	LastError   string              `json:"last_error,omitempty"`
	Ports       []ServicePortStatus `json:"ports,omitempty"`
}

// ServiceActionRequest is the request body for install, activate, deactivate, and uninstall.
type ServiceActionRequest struct {
	ID         string `json:"id"`
	DeleteData bool   `json:"delete_data,omitempty"`
}

// ServiceLogsRequest specifies the service and log limit.
type ServiceLogsRequest struct {
	ID   string `json:"id"`
	Tail int    `json:"tail,omitempty"`
}

// ServiceLogsResponse wraps the retrieved container log output.
type ServiceLogsResponse struct {
	Logs string `json:"logs"`
}
