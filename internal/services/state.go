package services

import "time"

// State is the operational lifecycle state of an infrastructure service.
type State string

const (
	StateNotInstalled State = "not_installed"
	StateInstalled    State = "installed"
	StateStarting     State = "starting"
	StateActive       State = "active"
	StateStopping     State = "stopping"
	StateError        State = "error"
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
}
