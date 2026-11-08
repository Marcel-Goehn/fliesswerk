//go:build unix

// Package shell runs a project's own command lines with the system shell.
package shell

import (
	"context"
	"os/exec"
	"syscall"
)

// Command runs line with the system shell, /bin/sh (POSIX puts it there,
// whatever PATH says), as the leader of a new process group, which is
// killed whole when ctx ends.
func Command(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", line) //nolint:gosec // the project's own command
	Isolate(cmd)
	return cmd
}

// Isolate keeps a terminal's Ctrl-C away from cmd, made by
// exec.CommandContext: it leads a new process group, which is killed whole
// when its context ends.
func Isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
