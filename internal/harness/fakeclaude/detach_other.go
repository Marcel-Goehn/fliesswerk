//go:build kwkfake && !unix

package main

import "os/exec"

// detach does nothing where there are no process groups to leave.
func detach(*exec.Cmd) {}
