// Package trust defines the trust-store results shared by the helper and the
// daemon.
package trust

// Target is the result of installing, removing, or checking one trust store or
// browser profile. Detail carries a human-readable status or error.
type Target struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
