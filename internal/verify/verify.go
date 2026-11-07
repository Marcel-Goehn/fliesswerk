// Package verify runs the project's Feedback commands the way the Factory
// checks a change itself, so that success never rests on the agent's word.
package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
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

func (f *Failure) Error() string {
	return fmt.Sprintf("`%s` (%s) failed: %v", f.Command.Name, f.Command.Line, f.Err)
}

// Tail is the last n lines of the output.
func (f *Failure) Tail(n int) string {
	lines := strings.Split(strings.TrimRight(f.Output, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// waitDelay bounds how long Run waits for a command's output once it has
// exited or been stopped, in case something it started holds the pipes.
const waitDelay = 5 * time.Second

// Run runs cmds one after another in dir with env, stopping at the first
// that fails. No commands means no checks.
func Run(ctx context.Context, dir string, env []string, cmds []Command) *Failure {
	for _, c := range cmds {
		var sh *exec.Cmd
		if runtime.GOOS == "windows" {
			sh = exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", c.Line) //nolint:gosec // the project's own Feedback command
		} else {
			sh = exec.CommandContext(ctx, "sh", "-c", c.Line) //nolint:gosec // the project's own Feedback command
		}
		sh.Dir, sh.Env, sh.WaitDelay = dir, env, waitDelay
		var out bytes.Buffer
		sh.Stdout, sh.Stderr = &out, &out
		if err := sh.Run(); err != nil {
			return &Failure{Command: c, Output: out.String(), Err: err}
		}
	}
	return nil
}
