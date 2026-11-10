// Package step is the process runner: it starts one Step from a Harness's
// Invocation, pipes the prompt to it, turns its output into events and
// decides the Outcome. A Step never stalls a Run: it is killed, with every
// process it started, when it goes silent for too long, runs for too long,
// lingers after its terminal event or has its context end, and whatever it
// leaves behind is killed when it exits.
package step

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// Limits of a Step.
const (
	// DefaultIdleTimeout is how long a Step may go without output.
	DefaultIdleTimeout = 10 * time.Minute
	// DefaultTimeout is how long a Step may run.
	DefaultTimeout = 60 * time.Minute
	// DefaultGrace is how long a Harness may run on after its terminal
	// event. It is fixed; only tests change it.
	DefaultGrace = 30 * time.Second
)

// drainDelay bounds how long Run waits for the end of the output once the
// process tree is killed, in case something escaped the kill (a process
// that left the process group on macOS) and still holds the pipes. Its
// readers then stay behind until it exits.
const drainDelay = 5 * time.Second

// Runner runs Steps with one Harness. Zero limits take their defaults.
type Runner struct {
	Harness     harness.Harness
	IdleTimeout time.Duration
	Timeout     time.Duration
	// Grace overrides DefaultGrace, for tests.
	Grace time.Duration
	// OnEvent, if set, sees every event as it is parsed.
	OnEvent func(harness.Event)
	// OnRaw, if set, sees every line of the Harness's stdout, as written.
	OnRaw func(line []byte)
	// OnIdleWarning, if set, is called when a Step has been silent for half
	// its idle timeout, with how long that is.
	OnIdleWarning func(silent time.Duration)
	// Env is added to each Step's env when it starts: the credentials and
	// project secrets the Step gets. They never go into argv.
	Env map[string]string
	// Unset are variables left out of the Factory's own env, so that a
	// credential of the user's own can't win over the configured one.
	Unset []string
	// Sandbox, if set, is where each Step runs. Nothing of the Factory's own
	// env goes in: only the non-interactive git env, Env and the Harness's
	// flags.
	Sandbox sandbox.Sandbox
}

// Run runs one Step in dir and returns its Outcome. The Step's env is the
// Factory's own without Unset, plus the non-interactive git env, Env and
// the Harness's flags; its stdin is the prompt, then closed. With a Sandbox
// it runs there instead, in the Sandbox's own project directory, and dir is
// unused.
func (r Runner) Run(ctx context.Context, s harness.Step, dir string) harness.Outcome {
	inv := r.Harness.Command(s)
	stepEnv := slices.Clone(git.NonInteractiveEnv)
	for k, v := range r.Env {
		stepEnv = append(stepEnv, k+"="+v)
	}
	for k, v := range inv.Env {
		stepEnv = append(stepEnv, k+"="+v)
	}
	argv, env, start := inv.Argv, []string(nil), startTree
	if r.Sandbox != nil {
		argv, env = r.Sandbox.Exec(inv.Argv, stepEnv)
		dir, start = "", startClient
	} else {
		env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
			name, _, _ := strings.Cut(kv, "=")
			return slices.ContainsFunc(r.Unset, func(u string) bool { return strings.EqualFold(u, name) })
		})
		env = append(env, stepEnv...)
	}
	argv, err := resolve(argv)
	if err != nil {
		return failed(fmt.Sprintf("can't start %s: %v", inv.Argv[0], err))
	}
	p, t, err := start(func(attr *syscall.SysProcAttr) (*process, error) {
		return spawn(argv, dir, env, attr)
	})
	if err != nil {
		return failed(fmt.Sprintf("can't start %s: %v", inv.Argv[0], err))
	}
	defer t.close()

	// The prompt goes in only now that the tree is set up, so the Harness
	// can't start anything outside it.
	go func() {
		_, _ = io.WriteString(p.stdin, inv.Stdin)
		_ = p.stdin.Close()
	}()

	o := &output{harness: r.Harness, onEvent: r.OnEvent, onRaw: r.OnRaw, activity: make(chan struct{}, 1), terminal: make(chan struct{})}
	outDone, errDone := make(chan struct{}), make(chan struct{})
	go o.readStdout(p.stdout, outDone)
	go o.readStderr(p.stderr, errDone)
	exited := make(chan error, 1)
	go func() { exited <- p.cmd.Wait() }()

	reason, waitErr, hasExited := r.watch(ctx, o, exited)
	t.kill()
	if !hasExited {
		waitErr = <-exited
	}
	r.clearSandbox()
	drained := make(chan struct{})
	drain := time.AfterFunc(drainDelay, func() { close(drained) })
	defer drain.Stop()
	for _, done := range []chan struct{}{outDone, errDone} {
		select {
		case <-done:
		case <-drained:
		}
	}

	events, terminal, stderr := o.result()
	exit := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(waitErr, &exitErr):
		exit = exitCode(exitErr)
	case waitErr != nil:
		exit = -1
	}
	out := harness.Resolve(events, exit)
	if reason != "" && !terminal {
		out = harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: reason, Usage: out.Usage, ContextWindow: out.ContextWindow}
	}
	if out.Kind == harness.Failed && out.Message != "" && stderr != "" {
		out.Message += ": " + lastLine(stderr)
	}
	return out
}

// exitCode is how the process ended, a SIGKILL as a shell would report it.
func exitCode(e *exec.ExitError) int {
	if status, ok := e.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
		return harness.ExitKilled
	}
	return e.ExitCode()
}

// watch waits until the Harness exits or must be killed. It returns why it
// must be killed ("" when it exited or lingered past the grace after its
// terminal event), and the Harness's exit if it exited.
func (r Runner) watch(ctx context.Context, o *output, exited <-chan error) (reason string, waitErr error, hasExited bool) {
	idleTimeout, timeout, grace := orDefault(r.IdleTimeout, DefaultIdleTimeout), orDefault(r.Timeout, DefaultTimeout), orDefault(r.Grace, DefaultGrace)
	idle, warn, step := time.NewTimer(idleTimeout), time.NewTimer(idleTimeout/2), time.NewTimer(timeout)
	defer idle.Stop()
	defer warn.Stop()
	defer step.Stop()
	idleC, warnC, terminal := idle.C, warn.C, o.terminal
	var graceC <-chan time.Time
	for {
		select {
		case waitErr = <-exited:
			return "", waitErr, true
		case <-o.activity:
			idle.Reset(idleTimeout)
			warn.Reset(idleTimeout / 2)
		case <-warnC:
			if r.OnIdleWarning != nil {
				r.OnIdleWarning(idleTimeout / 2)
			}
		case <-terminal:
			// Silence after the end of the session is expected; only the
			// grace counts now.
			idle.Stop()
			warn.Stop()
			idleC, warnC, terminal = nil, nil, nil
			graceC = time.After(grace)
		case <-graceC:
			return "", nil, false
		case <-idleC:
			return fmt.Sprintf("no output for %s (idle timeout)", idleTimeout), nil, false
		case <-step.C:
			return fmt.Sprintf("still running after %s (Step timeout)", timeout), nil, false
		case <-ctx.Done():
			return fmt.Sprintf("stopped: %v", ctx.Err()), nil, false
		}
	}
}

// clearSandbox kills what the Step left in its Sandbox: the kill above only
// reached the engine's client. If it fails, what is left dies with the
// Sandbox at the latest.
func (r Runner) clearSandbox() {
	if r.Sandbox == nil {
		return
	}
	_ = r.Sandbox.KillAll(context.Background())
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// process is a started Step process and the parent's ends of its pipes.
type process struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout, stderr *os.File
}

// spawn starts argv with attr. The pipes are the process's own files, not
// copied by os/exec, so that Wait returns when the Harness exits even if
// something it started still holds them. On failure it closes everything
// it opened.
func spawn(argv []string, dir string, env []string, attr *syscall.SysProcAttr) (*process, error) {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the Harness adapter builds the argv; never user input
	cmd.Dir, cmd.Env, cmd.SysProcAttr = dir, env, attr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	var parent, child []*os.File
	closeAll := func(files []*os.File) {
		for _, f := range files {
			_ = f.Close()
		}
	}
	for range 2 {
		r, w, err := os.Pipe()
		if err != nil {
			_ = stdin.Close()
			closeAll(parent)
			closeAll(child)
			return nil, err
		}
		parent, child = append(parent, r), append(child, w)
	}
	cmd.Stdout, cmd.Stderr = child[0], child[1]
	err = cmd.Start()
	closeAll(child)
	if err != nil {
		_ = stdin.Close()
		closeAll(parent)
		return nil, err
	}
	return &process{cmd: cmd, stdin: stdin, stdout: parent[0], stderr: parent[1]}, nil
}

// stderrTail is how much of the end of stderr is kept.
const stderrTail = 64 << 10

// output collects a Step's output from the reading goroutines.
type output struct {
	harness harness.Harness
	onEvent func(harness.Event)
	onRaw   func([]byte)
	// activity gets a value whenever output arrives; terminal is closed
	// at the first terminal event.
	activity chan struct{}
	terminal chan struct{}

	mu         sync.Mutex
	events     []harness.Event
	isTerminal bool
	stderr     []byte
}

func (o *output) noteActivity() {
	select {
	case o.activity <- struct{}{}:
	default:
	}
}

func (o *output) readStdout(f *os.File, done chan<- struct{}) {
	defer close(done)
	defer func() { _ = f.Close() }()
	lw := lineWriter{onLine: func(line []byte) {
		if o.onRaw != nil {
			o.onRaw(line)
		}
		for _, e := range o.harness.Parse(line) {
			o.mu.Lock()
			o.events = append(o.events, e)
			first := e.Terminal && !o.isTerminal
			o.isTerminal = o.isTerminal || e.Terminal
			o.mu.Unlock()
			if o.onEvent != nil {
				o.onEvent(e)
			}
			if first {
				close(o.terminal)
			}
		}
	}}
	read(f, func(p []byte) {
		o.noteActivity()
		lw.write(p)
	})
	lw.flush()
}

func (o *output) readStderr(f *os.File, done chan<- struct{}) {
	defer close(done)
	defer func() { _ = f.Close() }()
	read(f, func(p []byte) {
		o.noteActivity()
		o.mu.Lock()
		o.stderr = append(o.stderr, p...)
		if len(o.stderr) > stderrTail {
			o.stderr = o.stderr[len(o.stderr)-stderrTail:]
		}
		o.mu.Unlock()
	})
}

// result is what the Step wrote so far.
func (o *output) result() (events []harness.Event, terminal bool, stderr string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]harness.Event(nil), o.events...), o.isTerminal, string(o.stderr)
}

// read hands every chunk read from r to fn, until r ends.
func read(r io.Reader, fn func([]byte)) {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			fn(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func failed(msg string) harness.Outcome {
	return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: msg}
}

// lastLine is the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// lineWriter calls onLine with every complete line written to it. Only one
// goroutine writes to it.
type lineWriter struct {
	buf    []byte
	onLine func([]byte)
}

func (w *lineWriter) write(p []byte) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return
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
