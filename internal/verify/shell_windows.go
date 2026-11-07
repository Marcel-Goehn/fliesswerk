package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// shell runs line with the system's cmd.exe, which is killed with every
// process it started when ctx ends. The command line is passed as
// written: Go's argument quoting (\") is not what cmd.exe understands.
func shell(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, cmdExe()) //nolint:gosec // the project's own Feedback command
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + line + `"`}
	cmd.Cancel = func() error {
		kill := exec.Command(filepath.Join(systemRoot(), "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)) //nolint:gosec // a pid
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}

// cmdExe is the system's cmd.exe: %ComSpec%, else the one in System32,
// whatever PATH says.
func cmdExe() string {
	if c := os.Getenv("ComSpec"); c != "" {
		return c
	}
	return filepath.Join(systemRoot(), "System32", "cmd.exe")
}

// systemRoot is the Windows directory.
func systemRoot() string {
	if r := os.Getenv("SystemRoot"); r != "" {
		return r
	}
	return `C:\Windows`
}
