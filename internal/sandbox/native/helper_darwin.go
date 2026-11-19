//go:build darwin

package native

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// RunHelper runs a hidden subcommand if args name one, and returns its exit
// code.
func RunHelper(args []string) (code int, ok bool) {
	if len(args) == 0 || args[0] != helperExec {
		return 0, false
	}
	err := runExec(args[1:]) // only returns if it failed
	_, _ = fmt.Fprintf(os.Stderr, "fliesswerk: the native Sandbox: %v\n", err)
	return helperFailed, true
}

// runExec becomes the command, under a profile made for the tree as it is
// now. Env values stay in the environment and off every argv.
func runExec(args []string) error {
	s, command, err := loadPlan(args)
	if err != nil {
		return err
	}
	profile, err := s.seatbeltProfile()
	if err != nil {
		return err
	}
	if err := recordGroup(filepath.Join(filepath.Dir(args[0]), groupsDir)); err != nil {
		return err
	}
	if err := os.Chdir(s.Workdir); err != nil {
		return err
	}
	argv := append([]string{s.Launcher, "-p", profile}, command...)
	return syscall.Exec(s.Launcher, argv, os.Environ()) //nolint:gosec // sandbox-exec, as Check found it
}

// recordGroup notes this command's process group for KillAll, leading a new
// group if need be: the one it was born into may be the Factory's own.
func recordGroup(dir string) error {
	pid := os.Getpid()
	if syscall.Getpgrp() != pid {
		if err := syscall.Setpgid(0, 0); err != nil {
			return fmt.Errorf("leading a process group: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // the Sandbox's own state, from the Factory's argv
		return err
	}
	forgetEndedGroups(dir)
	return os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)), nil, 0o600) //nolint:gosec // the Sandbox's own state, from the Factory's argv
}

// forgetEndedGroups drops the groups with no process left: their ids are free
// to be reused by something that isn't the Sandbox's.
func forgetEndedGroups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if pgid, err := strconv.Atoi(e.Name()); err != nil || errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			_ = os.Remove(filepath.Join(dir, e.Name())) //nolint:gosec // the Sandbox's own state, from the Factory's argv
		}
	}
}

// killGroups kills the recorded process groups and forgets them.
func killGroups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if pgid, err := strconv.Atoi(e.Name()); err == nil && pgid > 1 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
