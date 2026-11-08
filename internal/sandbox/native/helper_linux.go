//go:build linux

package native

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// helperFailed is the exit code of a helper that couldn't run the command.
const helperFailed = 125

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

// runExec becomes the command's bwrap. Options and env go through a memfd:
// on bwrap's argv anyone on the machine could read a secret, and bwrap
// closes it before the command starts.
func runExec(args []string) error {
	dash := slices.Index(args, "--")
	if dash != 1 || len(args) < 3 {
		return errors.New("usage: " + helperExec + " <plan> -- <command>")
	}
	data, err := os.ReadFile(args[0]) //nolint:gosec // the Factory's own file, from its own argv
	if err != nil {
		return err
	}
	var s bwrapPlan
	if err := json.Unmarshal(data, &s); err != nil {
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
	argv := append([]string{s.Bwrap, "--args", strconv.Itoa(fd), "--", insideSelf, helperInit, "--"}, args[dash+1:]...)
	return syscall.Exec(s.Bwrap, argv, []string{}) //nolint:gosec // bwrap, as Check found it
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
// port on the Sandbox's loopback over to the socket, and runs the command.
func runInit(args []string) (int, error) {
	if len(args) < 2 || args[0] != "--" {
		return 0, errors.New("usage: " + helperInit + " -- <command>")
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+proxyPort)
	if err != nil {
		return 0, fmt.Errorf("the forwarder to the egress proxy can't listen: %w", err)
	}
	go forward(l)

	cmd := exec.Command(args[1], args[2:]...) //nolint:gosec // the command the Sandbox is for
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 0, err
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return exit.ExitCode(), nil
}

// forward relays every connection to the egress proxy's socket.
func forward(l net.Listener) {
	for {
		client, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = client.Close() }()
			proxy, err := net.Dial("unix", insideSocket)
			if err != nil {
				return
			}
			defer func() { _ = proxy.Close() }()
			done := make(chan struct{})
			go func() {
				_, _ = io.Copy(proxy, client)
				_ = proxy.(*net.UnixConn).CloseWrite()
				close(done)
			}()
			_, _ = io.Copy(client, proxy)
			_ = client.(*net.TCPConn).CloseWrite()
			<-done
		}()
	}
}
