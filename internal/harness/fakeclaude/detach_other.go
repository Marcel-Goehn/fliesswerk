//go:build fwkfake && !unix

package main

import (
	"os"
	"os/exec"
)

// detach does nothing where there are no process groups to leave.
func detach(*exec.Cmd) {}

// killSelf exits as a command killed by SIGKILL reports it.
func killSelf() { os.Exit(137) }
