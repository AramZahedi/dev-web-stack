//go:build windows

// Command php-smoke is only built for Linux. PHP artifacts are Linux binaries
// and the smoke test starts a real php-fpm master.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "php-smoke: PHP artifacts are Linux-only; run this command in WSL or on a Linux host")
	os.Exit(1)
}
