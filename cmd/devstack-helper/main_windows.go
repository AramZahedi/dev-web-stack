//go:build windows

// Command devstack-helper on Windows is implemented by Lane A. This stub keeps
// the frozen cross-build matrix green and fails closed if it is run before the
// Windows service exists.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "devstack-helper: Windows helper support is not implemented yet")
	os.Exit(1)
}
