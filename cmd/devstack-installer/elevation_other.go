//go:build !windows

package main

import (
	"errors"
	"os"
)

func isElevated() bool {
	return os.Geteuid() == 0
}

func relaunchElevated(args []string) error {
	return errors.New("devstack-installer: elevation is supported on Windows only")
}
