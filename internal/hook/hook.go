// Package hook runs a project's Hooks: its own shell commands at a named
// moment of a Run, in declared order and with nothing hidden about what
// runs at once or for how long.
package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

// Where a Hook runs.
const (
	Host    = "host"
	Sandbox = "sandbox"
)

// DefaultTimeout is how long an entry that sets no `timeout` may run.
const DefaultTimeout = 5 * time.Minute

// Entry is one entry of a hook point.
type Entry struct {
	// Commands are the entry's shell command lines.
	Commands []string
	// Where is Host or Sandbox.
	Where string
	// Timeout stops the entry when it runs longer; zero is DefaultTimeout.
	Timeout time.Duration
	// Env is the env its commands run with.
	Env []string
}

// Setting is what a hook point's entries run in: the directory for each
// kind of entry and what they are told about the moment.
type Setting struct {
	// HostDir is where a Host entry runs, SandboxDir where a Sandbox entry
	// runs.
	HostDir, SandboxDir string
	// Env is added to every entry's env.
	Env []string
	// Sandbox, if set, is where a Sandbox entry runs, in the Sandbox's own
	// project directory instead of SandboxDir.
	Sandbox sandbox.Sandbox
}

// Output is how one command ended and what it printed.
type Output struct {
	Command string
	// Text is its stdout and stderr, interleaved as written.
	Text string
	// Err is why it failed, nil if it didn't.
	Err error
	// Stopped reports that it was stopped because another command of its
	// group failed first.
	Stopped bool
}

// Failure is the command that failed first.
type Failure struct {
	Command string
	Err     error
}

// Error names the command and why it failed.
func (f *Failure) Error() string {
	return fmt.Sprintf("`%s` failed: %v", f.Command, f.Err)
}

// Run runs entries one after another, stopping at the first that fails. A
// command still running at its entry's timeout is stopped, with every
// process it started.
// report gets every command's Output as soon as its entry has ended.
func Run(ctx context.Context, m Setting, entries []Entry, report func(Output)) *Failure {
	for _, e := range entries {
		if f := runEntry(ctx, m, e, report); f != nil {
			return f
		}
	}
	return nil
}

// runEntry runs the entry's commands at once. The first to fail stops the
// others; every command's Output is reported, in declared order, once all
// have ended.
func runEntry(ctx context.Context, m Setting, e Entry, report func(Output)) *Failure {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	group, stop := context.WithCancel(ctx)
	defer stop()
	outputs := make([]Output, len(e.Commands))
	first := -1
	var once sync.Once
	var wg sync.WaitGroup
	for i, line := range e.Commands {
		wg.Go(func() {
			outputs[i] = runCommand(group, m, e, line, timeout)
			if outputs[i].Err != nil {
				once.Do(func() {
					first = i
					stop()
				})
			}
		})
	}
	wg.Wait()
	if first >= 0 && e.Where == Sandbox && m.Sandbox != nil {
		// Ending the engine's clients left the commands running inside.
		_ = m.Sandbox.KillAll(context.WithoutCancel(ctx))
	}
	for i, o := range outputs {
		o.Stopped = o.Err != nil && i != first && !errors.Is(group.Err(), context.DeadlineExceeded)
		report(o)
	}
	if first < 0 {
		return nil
	}
	return &Failure{Command: outputs[first].Command, Err: outputs[first].Err}
}

// waitDelay bounds how long a command's output is waited for once it has
// exited or been stopped, in case something escaped the kill and holds
// the pipes.
const waitDelay = 5 * time.Second

func runCommand(ctx context.Context, m Setting, e Entry, line string, timeout time.Duration) Output {
	env := append(e.Env[:len(e.Env):len(e.Env)], m.Env...)
	var sb sandbox.Sandbox
	dir := m.HostDir
	if e.Where == Sandbox {
		sb, dir = m.Sandbox, m.SandboxDir
	}
	sh := sandbox.Shell(ctx, sb, dir, line, env)
	sh.WaitDelay = waitDelay
	var out bytes.Buffer
	sh.Stdout, sh.Stderr = &out, &out
	err := sh.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("still running after %s (timeout)", timeout)
	}
	return Output{Command: line, Text: out.String(), Err: err}
}
