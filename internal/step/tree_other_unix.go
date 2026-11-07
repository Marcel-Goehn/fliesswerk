//go:build unix && !linux

package step

import "syscall"

// startTree starts the Harness as the leader of a new process group. A
// process that leaves the group (setsid) escapes the kill: an accepted
// residual risk.
func startTree(spawn func(*syscall.SysProcAttr) (*process, error)) (*process, *tree, error) {
	return startGroup(spawn, &syscall.SysProcAttr{})
}
