package helperproto

// Proxy target endpoint exposed by the helper. The daemon calls this before
// reconciling privileged forwarders so a changed daemon HTTP/HTTPS port does
// not leave 80/443 pointing at a dead loopback listener.
const PathProxyTargets = "/v1/proxy/targets"

// ProxyTargetsRequest replaces only the unprivileged loopback targets. The
// helper's privileged listen ports remain fixed by its own configuration and
// no target path or command is accepted. Only the same-UID daemon may call the
// helper socket; this RPC is the deliberate exception that lets the daemon's
// listener ports move without editing root-owned configuration by hand.
type ProxyTargetsRequest struct {
	HTTPPort  int `json:"http_port"`
	HTTPSPort int `json:"https_port"`
}

// ProxyTargetsResponse reports the targets accepted by the helper and whether
// they were written back to the helper's YAML configuration.
type ProxyTargetsResponse struct {
	HTTPPort  int  `json:"http_port"`
	HTTPSPort int  `json:"https_port"`
	Persisted bool `json:"persisted"`
	// PersistenceError reports a write failure after the in-memory targets
	// already moved. Forwarding works until the next helper restart; callers
	// should surface this as a warning.
	PersistenceError string `json:"persistence_error,omitempty"`
	ConfigPath       string `json:"config_path,omitempty"`
}
