package services

import "time"

// State is the operational lifecycle state of an infrastructure service.
type State string

const (
	StateNotInstalled        State = "not_installed"
	StateInstalled           State = "installed"
	StateInstalledButStopped State = "installed_but_stopped"
	StateStarting            State = "starting"
	StateActive              State = "active"
	StateStopping            State = "stopping"
	StateError               State = "error"
)

// Record is the persistent database row describing a service's recorded state.
type Record struct {
	ID            ID
	State         State
	Image         string
	Active        bool
	LastError     string
	LastOperation string
	UpdatedAt     time.Time
}

// View combines catalog metadata and stored state for API/UI presentation.
type View struct {
	ID          ID     `json:"id"`
	DisplayName string `json:"display_name"`
	Group       Group  `json:"group"`
	State       State  `json:"state"`
	Active      bool   `json:"active"`
	Image       string `json:"image"`
	Ports       []Port `json:"ports,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	ConfigPath  string `json:"config_path,omitempty"`
	DataPath    string `json:"data_path,omitempty"`
	Health      string `json:"health"`
	ContainerID string `json:"container_id"`
	Installed   bool   `json:"installed"`
}

// installedState reports whether a state implies an image and config scaffold exist.
func installedState(state State) bool {
	switch state {
	case StateInstalled, StateInstalledButStopped, StateStarting, StateActive, StateStopping:
		return true
	default:
		return false
	}
}
