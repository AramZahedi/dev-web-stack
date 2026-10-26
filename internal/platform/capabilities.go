package platform

// CapabilityProbe supplies host facts that cannot be derived from GOOS alone.
type CapabilityProbe struct {
	Systemd      bool
	DockerSocket bool
	WSL          bool
	ConPTY       bool
}

// Capabilities describes the platform features the product may use.
type Capabilities struct {
	HasSystemd         bool
	HasDockerSocket    bool
	HasNamedPipe       bool
	HasWSL             bool
	SupportsHostTuning bool
	SupportsConPTY     bool
	RequiresWSLAgent   bool
}

// CapabilitiesFor maps a GOOS and host probe into the capability contract.
func CapabilitiesFor(goos string, probe CapabilityProbe) Capabilities {
	if goos == "windows" {
		return Capabilities{
			HasDockerSocket:    false,
			HasNamedPipe:       true,
			HasWSL:             probe.WSL,
			SupportsHostTuning: false,
			SupportsConPTY:     true,
			RequiresWSLAgent:   true,
		}
	}
	return Capabilities{
		HasSystemd:         probe.Systemd,
		HasDockerSocket:    probe.DockerSocket,
		HasNamedPipe:       false,
		HasWSL:             false,
		SupportsHostTuning: true,
		SupportsConPTY:     false,
		RequiresWSLAgent:   false,
	}
}
