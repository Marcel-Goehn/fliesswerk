package verify

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// shell runs line with cmd.exe, which is killed with every process it
// started when ctx ends. The command line is passed as written: Go's
// argument quoting (\") is not what cmd.exe understands.
func shell(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe") //nolint:gosec // the project's own Feedback command
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + line + `"`}
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)) //nolint:gosec // a pid
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}
