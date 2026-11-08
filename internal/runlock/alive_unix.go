//go:build unix

package runlock

import (
	"errors"
	"syscall"
)

// alive reports whether a process with this PID is running. EPERM means
// it is, under another user.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
