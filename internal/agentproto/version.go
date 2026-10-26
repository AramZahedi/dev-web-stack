package agentproto

// SupportedProtocol reports whether the wire version is understood by this
// build. There is exactly one supported version by design.
func SupportedProtocol(version string) bool {
	return version == ProtocolVersion
}
