// Package step is the process runner: it starts one Step from a Harness's
// Invocation, pipes the prompt to it, turns its output into events and
// decides the Outcome. A Step that lingers after its terminal event, or
// whose context ends, is killed.
package step

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

// DefaultGrace is how long a Harness may run on after its terminal event.
const DefaultGrace = 30 * time.Second

// waitDelay bounds how long Run waits for output after the process has
// exited, in case something it started still holds its pipes.
const waitDelay = 5 * time.Second

// Runner runs Steps with one Harness.
type Runner struct {
	Harness harness.Harness
	// Grace overrides DefaultGrace, for tests.
	Grace time.Duration
	// OnEvent, if set, sees every event as it is parsed.
	OnEvent func(harness.Event)
}

// Run runs one Step in dir and returns its Outcome. The Step's env is the
// Factory's own, plus the Harness's flags and the non-interactive git env.
func (r Runner) Run(ctx context.Context, s harness.Step, dir string) harness.Outcome {
	inv := r.Harness.Command(s)
	bin, err := exec.LookPath(inv.Argv[0])
	if err != nil {
		return failed(fmt.Sprintf("can't start %s: %v", inv.Argv[0], err))
	}
	ctx, kill := context.WithCancel(ctx)
	defer kill()

	cmd := exec.CommandContext(ctx, bin, inv.Argv[1:]...) //nolint:gosec // the Harness adapter builds the argv; never user input
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), git.NonInteractiveEnv...)
	for k, v := range inv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(inv.Stdin)
	cmd.WaitDelay = waitDelay

	grace := r.Grace
	if grace == 0 {
		grace = DefaultGrace
	}
	var events []harness.Event
	var timer *time.Timer
	stdout := &lineWriter{onLine: func(line []byte) {
		for _, e := range r.Harness.Parse(line) {
			events = append(events, e)
			if r.OnEvent != nil {
				r.OnEvent(e)
			}
			if e.Terminal && timer == nil {
				timer = time.AfterFunc(grace, kill)
			}
		}
	}}
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if timer != nil {
		timer.Stop()
	}
	stdout.flush()
	exit := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	case err != nil && ctx.Err() != nil:
		exit = -1 // killed after the grace, or the Step was cancelled
	case err != nil:
		return failed(fmt.Sprintf("%s: %v", inv.Argv[0], err))
	}
	out := harness.Resolve(events, exit)
	if out.Kind == harness.Failed && out.Message != "" && stderr.Len() > 0 {
		out.Message += ": " + lastLine(stderr.String())
	}
	return out
}

func failed(msg string) harness.Outcome {
	return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: msg}
}

// lastLine is the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// lineWriter calls onLine with every complete line written to it. Only one
// goroutine writes to it.
type lineWriter struct {
	buf    []byte
	onLine func([]byte)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.onLine(bytes.TrimRight(w.buf[:i], "\r"))
		w.buf = w.buf[i+1:]
	}
}

// flush hands over a last line that has no newline.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.onLine(w.buf)
		w.buf = nil
	}
}
