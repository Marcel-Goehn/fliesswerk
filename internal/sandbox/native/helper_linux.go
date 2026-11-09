//go:build linux

package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/Marcel-Goehn/kraftwerk/internal/forward"
	"golang.org/x/sys/unix"
)

// RunHelper runs a hidden subcommand if args name one, and returns its exit
// code.
func RunHelper(args []string) (code int, ok bool) {
	if len(args) == 0 || (args[0] != helperExec && args[0] != helperInit) {
		return 0, false
	}
	var err error
	if args[0] == helperExec {
		err = runExec(args[1:]) // only returns if it failed
	} else {
		code, err = runInit(args[1:])
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "kraftwerk: the native Sandbox: %v\n", err)
		return helperFailed, true
	}
	return code, true
}

// killGroups has nothing to do: a command's PID namespace ends with it.
func killGroups(string) {}

// runExec becomes the command's bwrap. Options and env go through a memfd:
// on bwrap's argv anyone on the machine could read a secret, and bwrap
// closes it before the command starts.
func runExec(args []string) error {
	s, command, err := loadPlan(args)
	if err != nil {
		return err
	}
	if err := recordRoot(filepath.Join(filepath.Dir(args[0]), groupsDir)); err != nil {
		return err
	}
	opts, err := s.options(os.Environ())
	if err != nil {
		return err
	}
	fd, err := optionsFile(opts)
	if err != nil {
		return err
	}
	argv := slices.Concat([]string{s.Launcher, "--args", strconv.Itoa(fd), "--", insideSelf, helperInit}, s.initRules(), []string{"--"}, command)
	return syscall.Exec(s.Launcher, argv, []string{}) //nolint:gosec // bwrap, as Check found it
}

// recordRoot notes this process, which becomes the command's bwrap, as the
// root of a tree to measure, and forgets the ones that have ended: their ids
// are free to be reused by something that isn't the Sandbox's.
func recordRoot(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // the Sandbox's own state, from the Factory's argv
		return err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if pid, err := strconv.Atoi(e.Name()); err != nil || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				_ = os.Remove(filepath.Join(dir, e.Name())) //nolint:gosec // the Sandbox's own state, from the Factory's argv
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600) //nolint:gosec // the Sandbox's own state, from the Factory's argv
}

// optionsFile is a file descriptor bwrap inherits, holding opts in the
// NUL-separated form of its --args.
func optionsFile(opts []string) (int, error) {
	fd, err := unix.MemfdCreate("kraftwerk-sandbox", 0)
	if err != nil {
		return 0, fmt.Errorf("memfd_create: %w", err)
	}
	data := []byte(strings.Join(opts, "\x00") + "\x00")
	for len(data) > 0 {
		n, err := unix.Write(fd, data)
		if err != nil {
			return 0, err
		}
		data = data[n:]
	}
	if _, err := unix.Seek(fd, 0, io.SeekStart); err != nil {
		return 0, err
	}
	return fd, nil
}

// runInit is the Sandbox's first process of its own: it carries the proxy
// port and the Services' ports on the Sandbox's loopback over to their
// sockets, and runs the command. Its arguments are forward rules, then "--"
// and the command.
func runInit(args []string) (int, error) {
	dash := slices.Index(args, "--")
	if dash < 0 || len(args) < dash+2 {
		return 0, errors.New("usage: " + helperInit + " [listen=target ...] -- <command>")
	}
	rules := []forward.Rule{{Listen: "127.0.0.1:" + proxyPort, Target: forward.Unix(insideSocket)}}
	for _, arg := range args[:dash] {
		rule, err := forward.ParseRule(arg)
		if err != nil {
			return 0, err
		}
		rules = append(rules, rule)
	}
	if _, err := forward.Start(context.Background(), rules); err != nil {
		return 0, fmt.Errorf("the forwarders can't listen: %w", err)
	}

	cmd := exec.Command(args[dash+1], args[dash+2:]...) //nolint:gosec // the command the Sandbox is for
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 0, err
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return exit.ExitCode(), nil
}
