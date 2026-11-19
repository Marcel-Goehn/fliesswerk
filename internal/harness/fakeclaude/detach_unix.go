//go:build fwkfake && unix

package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// detach makes cmd leave the fake's process group and session (setsid).
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// killSelf ends the fake by SIGKILL. As the first process of a PID namespace
// it can't receive that from itself, so it then exits as the engine's client
// reports a command killed that way.
func killSelf() {
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	os.Exit(137)
}
