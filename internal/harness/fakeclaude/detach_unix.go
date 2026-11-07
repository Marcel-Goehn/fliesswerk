//go:build kwkfake && unix

package main

import (
	"os/exec"
	"syscall"
)

// detach makes cmd leave the fake's process group and session (setsid).
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
