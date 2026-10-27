//go:build !windows

package cli

import "context"

func stopDaemonPlatform(ctx context.Context, home string) (bool, error) {
	return daemonStop(ctx, home)
}
