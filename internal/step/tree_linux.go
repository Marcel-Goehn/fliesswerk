package step

import (
	"errors"
	"os"
	"syscall"
)

// startTree starts the Harness as the init of a new PID namespace (inside
// a user namespace that maps the user to themselves, so no privilege is
// needed). When the init dies, the kernel kills every process in the
// namespace, so nothing the Harness starts can outlive it, not even a
// process that calls setsid. Where namespaces are refused (seccomp in a
// container, user namespaces switched off), the Harness leads a process
// group instead, as on macOS.
//
// Accepted side effects: inside the user namespace, files of other users
// show as owned by nobody and sudo doesn't work; as PID 1, the Harness
// inherits orphans it may not reap, which the kernel clears when the
// namespace ends.
func startTree(spawn func(*syscall.SysProcAttr) (*process, error)) (*process, *tree, error) {
	p, t, err := startGroup(spawn, &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: os.Getuid(), HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: os.Getgid(), HostID: os.Getgid(), Size: 1}},
	})
	if err != nil && namespacesRefused(err) {
		return startGroup(spawn, &syscall.SysProcAttr{})
	}
	return p, t, err
}

func namespacesRefused(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.ENOSYS)
}
