//go:build !windows

package performance

import "errors"

func collectWindows() (Resources, error) {
	return Resources{}, errors.New("collectWindows is only available on Windows")
}
