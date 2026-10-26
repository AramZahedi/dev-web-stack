//go:build windows

package performance

import "errors"

// errNoNofileLimit is returned when a collector asks for the Unix open-file
// limit on Windows. Windows has per-handle quotas instead of RLIMIT_NOFILE.
var errNoNofileLimit = errors.New("RLIMIT_NOFILE is not available on Windows")

func defaultNofileLimit() (soft, hard uint64, err error) {
	return 0, 0, errNoNofileLimit
}
