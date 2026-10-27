//go:build windows

package performance

// defaultNofileLimit returns the file handle quotas on Windows.
func defaultNofileLimit() (soft, hard uint64, err error) {
	return defaultWindowsHandleLimit, defaultWindowsHandleLimit, nil
}
