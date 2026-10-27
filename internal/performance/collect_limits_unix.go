//go:build !windows

package performance

import "syscall"

// defaultNofileLimit reports the process open-file limit. Linux and the other
// Unix targets expose RLIMIT_NOFILE through syscall.
func defaultNofileLimit() (soft, hard uint64, err error) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return 0, 0, err
	}
	return limit.Cur, limit.Max, nil
}
