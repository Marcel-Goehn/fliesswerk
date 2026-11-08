// Package sandbox is what the Factory's core knows of a Sandbox: a place
// that runs commands and can be cleared and destroyed. A nil Sandbox means
// no Sandbox (`kind = "none"`): commands run directly on the host.
package sandbox

import (
	"context"
	"os/exec"

	"github.com/Marcel-Goehn/kraftwerk/internal/shell"
)

// Sandbox is one Sandbox, holding one worktree.
type Sandbox interface {
	// Exec turns a command to run inside the Sandbox, with env in its
	// environment, into the host command that does it. A value of env never
	// goes into hostArgv.
	Exec(argv, env []string) (hostArgv, hostEnv []string)
	// KillAll kills every process left in the Sandbox: ending the host
	// command doesn't end what it started inside.
	KillAll(ctx context.Context) error
	// Destroy removes the Sandbox and everything created for it.
	Destroy(ctx context.Context) error
}

// Request says what a new Sandbox is for.
type Request struct {
	// Name tells it from the Run's other Sandboxes.
	Name string
	// Worktree is the git worktree it holds, and Rel the project root
	// relative to the top of it.
	Worktree, Rel string
}

// Shell is the host command that runs line with the Sandbox's shell, or
// with the host's in dir when s is nil. It is stopped when ctx ends, though
// what it started inside a Sandbox takes a KillAll.
func Shell(ctx context.Context, s Sandbox, dir, line string, env []string) *exec.Cmd {
	if s == nil {
		cmd := shell.Command(ctx, line)
		cmd.Dir, cmd.Env = dir, env
		return cmd
	}
	argv, hostEnv := s.Exec([]string{"/bin/sh", "-c", line}, env)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the project's own command, for the Sandbox
	shell.Isolate(cmd)
	cmd.Env = hostEnv
	return cmd
}
