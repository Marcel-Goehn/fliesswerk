//go:build unix

package step

import "syscall"

// tree is a Step's process tree on Unix: a process group led by the
// Harness, which on Linux is also the init of its own PID namespace.
type tree struct{ pid int }

// startGroup starts the Harness as the leader of a new process group.
func startGroup(spawn func(*syscall.SysProcAttr) (*process, error), attr *syscall.SysProcAttr) (*process, *tree, error) {
	attr.Setpgid = true
	p, err := spawn(attr)
	if err != nil {
		return nil, nil, err
	}
	return p, &tree{pid: p.cmd.Process.Pid}, nil
}

// kill kills every process in the tree's group; in a PID namespace,
// killing its init kills all the others too. Only the group is signalled,
// never the bare pid, which may be reused once the Harness is reaped; the
// group's id can't be while any of its processes lives.
func (t *tree) kill() { _ = syscall.Kill(-t.pid, syscall.SIGKILL) }

// close has nothing to free on Unix.
func (t *tree) close() {}
