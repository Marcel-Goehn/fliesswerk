// Package verify runs the project's Feedback commands the way the Factory
// checks a change itself, so that success never rests on the agent's word.
package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// Command is one Feedback command from `[commands]`.
type Command struct {
	Name string
	// Line is the shell command line.
	Line string
}

// Failure is the Feedback command that failed, and what it printed.
type Failure struct {
	Command Command
	// Output is its stdout and stderr, interleaved as written.
	Output string
	Err    error
}

// Error names the command and why it failed.
func (f *Failure) Error() string {
	return fmt.Sprintf("`%s` (%s) failed: %v", f.Command.Name, f.Command.Line, f.Err)
}

// Tail is the last n lines of the output.
func (f *Failure) Tail(n int) string {
	lines := strings.Split(strings.TrimRight(f.Output, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// waitDelay bounds how long Run waits for a command's output once it has
// exited or been stopped, in case something escaped the kill and holds
// the pipes.
const waitDelay = 5 * time.Second

// Run runs cmds one after another in dir with env, stopping at the first
// that fails. A command still running after timeout is stopped, with
// every process it started. No commands means no checks. With a Sandbox,
// the commands run in it, in its own project directory, and dir is unused.
func Run(ctx context.Context, sb sandbox.Sandbox, dir string, env []string, timeout time.Duration, cmds []Command) *Failure {
	for _, c := range cmds {
		if f := run(ctx, sb, dir, env, timeout, c); f != nil {
			if sb != nil {
				// Ending the engine's client left the command running inside.
				_ = sb.KillAll(context.WithoutCancel(ctx))
			}
			return f
		}
	}
	return nil
}

func run(ctx context.Context, sb sandbox.Sandbox, dir string, env []string, timeout time.Duration, c Command) *Failure {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sh := sandbox.Shell(ctx, sb, dir, c.Line, env)
	sh.WaitDelay = waitDelay
	var out bytes.Buffer
	sh.Stdout, sh.Stderr = &out, &out
	err := sh.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("still running after %s (timeout)", timeout)
	}
	if err != nil {
		return &Failure{Command: c, Output: out.String(), Err: err}
	}
	return nil
}
