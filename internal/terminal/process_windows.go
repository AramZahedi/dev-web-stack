//go:build windows

package terminal

// signalGroup is a no-op on Windows. The Windows terminal is an agent-proxy
// stub until the WSL terminal transport lands; there is no ConPTY process
// group to signal here.
func signalGroup(int, bool) {}
