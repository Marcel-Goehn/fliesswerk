//go:build unix

package verify

import (
	"context"
	"os/exec"
	"syscall"
)

// shell runs line with sh as the leader of a new process group, which is
// killed whole when ctx ends.
func shell(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", line) //nolint:gosec // the project's own Feedback command
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd
}
