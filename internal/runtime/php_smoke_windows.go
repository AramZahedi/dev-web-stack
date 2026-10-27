//go:build windows

package runtime

import (
	"context"
	"errors"
)

// phpSmoke is unsupported on Windows: PHP runtime registration happens in the
// WSL agent, which runs the Linux implementation.
var phpSmoke = func(context.Context, string) error {
	return errors.New("runtime: PHP smoke tests run in the WSL agent")
}
