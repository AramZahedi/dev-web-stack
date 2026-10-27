//go:build windows

package forwarder

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
)

func mapListenErr(err error) error {
	if errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, syscall.Errno(10048)) {
		return fmt.Errorf("%w: %w", err, syscall.EADDRINUSE)
	}
	return err
}
