// Package runlog is the record of a Run: the live lines on the terminal
// and, in the Run's own directory under .kraftwerk/logs/, the verbose
// run.log, each Step attempt's raw output and exact prompt, and the
// summary. Everything it writes goes through the redactor first.
package runlog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/redact"
)

// IDFormat makes a Run's id from its start time.
const IDFormat = "2006-01-02T15-04-05"

// Kind is what a Line reports, which decides how it is shown.
type Kind int

const (
	// Event is something the Factory did.
	Event Kind = iota
	// Tool is a tool the agent called.
	Tool
	// AgentText is the agent's own text, shown on the terminal only when
	// verbose.
	AgentText
	// Warning is something a person should look at.
	Warning
	// Failure is something that went wrong.
	Failure
	// Success is something that was finished.
	Success
)

// Line is one line of a Run's log. Ticket and Step are empty when it isn't
// about one.
type Line struct {
	Ticket, Step, Text string
	Kind               Kind
}

// String is the line without its time, free of terminal control sequences.
func (l Line) String() string {
	return l.prefix() + l.body()
}

func (l Line) prefix() string {
	var b strings.Builder
	for _, column := range []string{l.Ticket, l.Step} {
		if column != "" {
			b.WriteString(column + "  ")
		}
	}
	return b.String()
}

func (l Line) body() string {
	text := plain(l.Text)
	if l.Kind == Tool {
		return "▸ " + text
	}
	return text
}

// escapes are the terminal sequences a program may print: CSI (colours,
// cursor movement) and OSC (titles, links).
var escapes = regexp.MustCompile("\x1b\\[[0-9;:?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)?")

// plain is s without terminal sequences and control characters, so that
// what an agent prints can't drive the user's terminal or colour the log.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && (r < 0x20 || r == 0x7f) {
			return -1
		}
		return r
	}, escapes.ReplaceAllString(s, ""))
}

// colours are the ANSI codes of the Kinds that have one.
var colours = map[Kind]string{Warning: "33", Failure: "31", Success: "32"}

// UseColour reports whether output may be coloured: only on a terminal,
// and not when NO_COLOR is set to anything.
func UseColour(terminal bool, noColor string) bool {
	return terminal && noColor == ""
}

// Options shape a Log.
type Options struct {
	// Root is the project root, which holds .kraftwerk/.
	Root string
	// Start is when the Run started; it names the Run.
	Start time.Time
	// Terminal gets the live lines and the summary's text.
	Terminal io.Writer
	// Verbose shows the agent's text on the terminal too.
	Verbose bool
	// Colour colours the terminal's lines.
	Colour bool
	// Redactor knows the secrets to mask; nil knows none.
	Redactor *redact.Redactor
}

// Log is the record of one Run. It is safe for concurrent use.
type Log struct {
	opts Options
	id   string
	dir  string

	mu       sync.Mutex
	runLog   *os.File
	attempts map[string]int
	warned   bool
	finished bool
}

// logsDir is where the Run directories are, relative to the project root.
var logsDir = filepath.Join(".kraftwerk", "logs")

// Open creates the Run's directory and its run.log.
func Open(o Options) (*Log, error) {
	if o.Redactor == nil {
		o.Redactor = redact.New()
	}
	parent := filepath.Join(o.Root, logsDir)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return nil, err
	}
	base := o.Start.Format(IDFormat)
	id, dir := base, filepath.Join(parent, base)
	for n := 2; ; n++ {
		err := os.Mkdir(dir, 0o750)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		id = fmt.Sprintf("%s-%d", base, n)
		dir = filepath.Join(parent, id)
	}
	f, err := os.Create(filepath.Join(dir, "run.log")) //nolint:gosec // inside the directory just made
	if err != nil {
		return nil, err
	}
	return &Log{opts: o, id: id, dir: dir, runLog: f, attempts: map[string]int{}}, nil
}

// ID is the Run's id.
func (l *Log) ID() string { return l.id }

// Dir is the Run's directory.
func (l *Log) Dir() string { return l.dir }

// RelDir is the directory of a Run, relative to the project root and with
// forward slashes.
func RelDir(runID string) string {
	return filepath.ToSlash(filepath.Join(logsDir, runID))
}

// Write logs one line: always to run.log, and to the terminal unless it is
// the agent's text and the Log isn't verbose.
func (l *Log) Write(line Line) {
	stamp := time.Now().Format(time.TimeOnly)
	l.mu.Lock()
	defer l.mu.Unlock()
	redacted := l.opts.Redactor.String(line.String())
	l.toFile(stamp + "  " + redacted + "\n")
	if line.Kind == AgentText && !l.opts.Verbose {
		return
	}
	shown := stamp + "  " + redacted
	if l.opts.Colour {
		prefix := l.opts.Redactor.String(line.prefix())
		body := strings.TrimPrefix(redacted, prefix)
		if code, ok := colours[line.Kind]; ok {
			body = "\x1b[" + code + "m" + body + "\x1b[0m"
		}
		shown = "\x1b[2m" + stamp + "\x1b[0m  " + prefix + body
	}
	_, _ = io.WriteString(l.opts.Terminal, shown+"\n")
}

// toFile appends to run.log. The caller holds mu.
func (l *Log) toFile(s string) {
	if _, err := io.WriteString(l.runLog, s); err != nil {
		l.warnLocked("run.log", err)
	}
}

// warnLocked says once that the record is incomplete; a Run goes on
// regardless. The caller holds mu.
func (l *Log) warnLocked(what string, err error) {
	if l.warned {
		return
	}
	l.warned = true
	_, _ = fmt.Fprintf(l.opts.Terminal, "warning: the Run's log is incomplete: %s: %s\n", what, l.opts.Redactor.String(err.Error()))
}

// Attempt starts the record of a Step attempt under its Ticket: the exact
// prompt, and a file for its raw output. Attempts of a Step are numbered
// from 1 across the Run.
func (l *Log) Attempt(ticket, step, prompt string) *Attempt {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ticket + "/" + step
	l.attempts[key]++
	dir := filepath.Join(l.dir, ticket)
	name := fmt.Sprintf("%s-%d", step, l.attempts[key])
	a := &Attempt{log: l}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		l.warnLocked(dir, err)
		return a
	}
	if err := os.WriteFile(filepath.Join(dir, name+".prompt.md"), []byte(l.opts.Redactor.String(prompt)), 0o600); err != nil {
		l.warnLocked(name+".prompt.md", err)
	}
	f, err := os.Create(filepath.Join(dir, name+".jsonl")) //nolint:gosec // inside the Run's directory
	if err != nil {
		l.warnLocked(name+".jsonl", err)
		return a
	}
	a.raw = f
	return a
}

// Attempt is the record of one Step attempt.
type Attempt struct {
	log *Log
	raw *os.File
}

// Raw records one line of the Harness's output, redacted.
func (a *Attempt) Raw(line []byte) {
	if a.raw == nil {
		return
	}
	if _, err := io.WriteString(a.raw, a.log.opts.Redactor.String(string(line))+"\n"); err != nil {
		a.log.mu.Lock()
		defer a.log.mu.Unlock()
		a.log.warnLocked(a.raw.Name(), err)
	}
}

// Close ends the attempt's record.
func (a *Attempt) Close() {
	if a.raw != nil {
		_ = a.raw.Close()
	}
}

// Finish shows the summary, saves it as summary.json and closes the Log.
// It returns the JSON, redacted; only its first call does anything.
func (l *Log) Finish(s Summary) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return nil
	}
	l.finished = true
	text := l.opts.Redactor.String(plainLines(s.Text()))
	_, _ = io.WriteString(l.opts.Terminal, text)
	l.toFile(text)
	out := []byte(l.opts.Redactor.String(string(s.json())))
	if err := os.WriteFile(filepath.Join(l.dir, "summary.json"), out, 0o600); err != nil {
		l.warnLocked("summary.json", err)
	}
	_ = l.runLog.Close()
	return out
}

// plainLines is plain for text of many lines.
func plainLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = plain(line)
	}
	return strings.Join(lines, "\n")
}
