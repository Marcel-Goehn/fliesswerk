package runlog_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/redact"
	"github.com/Marcel-Goehn/fliesswerk/internal/runlog"
)

var start = time.Date(2026, 11, 7, 14, 3, 12, 0, time.Local)

func open(t *testing.T, o runlog.Options) (*runlog.Log, string) {
	t.Helper()
	o.Root, o.Start = t.TempDir(), start
	l, err := runlog.Open(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Finish(runlog.Summary{}) })
	return l, l.Dir()
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestARunDirectoryIsNamedAfterTheStartTime(t *testing.T) {
	var term strings.Builder
	root := t.TempDir()
	first, err := runlog.Open(runlog.Options{Root: root, Start: start, Terminal: &term})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Finish(runlog.Summary{})
	second, err := runlog.Open(runlog.Options{Root: root, Start: start, Terminal: &term})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Finish(runlog.Summary{})

	if first.ID() != "2026-11-07T14-03-12" {
		t.Errorf("ID = %q", first.ID())
	}
	if want := filepath.Join(root, ".fliesswerk", "logs", first.ID()); first.Dir() != want {
		t.Errorf("Dir = %q, want %q", first.Dir(), want)
	}
	if first.Dir() == second.Dir() {
		t.Errorf("two Runs started in the same second share %s", first.Dir())
	}
}

func TestLinesArePrefixedWithTimeTicketAndStep(t *testing.T) {
	var term strings.Builder
	l, _ := open(t, runlog.Options{Terminal: &term})

	l.Write(runlog.Line{Text: "Iteration 1: taking 1 Ticket"})
	l.Write(runlog.Line{Ticket: "#42", Text: "closed"})
	l.Write(runlog.Line{Ticket: "#42", Step: "implement", Kind: runlog.Tool, Text: "Read src/cart.go"})

	want := regexp.MustCompile(`^\d\d:\d\d:\d\d  Iteration 1: taking 1 Ticket
\d\d:\d\d:\d\d  #42  closed
\d\d:\d\d:\d\d  #42  implement  ▸ Read src/cart\.go
$`)
	if !want.MatchString(term.String()) {
		t.Errorf("terminal:\n%s", term.String())
	}
}

func TestAgentTextReachesTheTerminalOnlyWhenVerboseButAlwaysTheRunLog(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprint(verbose), func(t *testing.T) {
			var term strings.Builder
			l, dir := open(t, runlog.Options{Terminal: &term, Verbose: verbose})

			l.Write(runlog.Line{Ticket: "#2", Step: "review", Kind: runlog.AgentText, Text: "thinking aloud"})

			if got := strings.Contains(term.String(), "thinking aloud"); got != verbose {
				t.Errorf("terminal shows the text = %v, want %v:\n%s", got, verbose, term.String())
			}
			if !strings.Contains(read(t, filepath.Join(dir, "run.log")), "#2  review  thinking aloud") {
				t.Errorf("run.log lacks the text")
			}
		})
	}
}

func TestColourReachesOnlyAColourTerminalNeverTheRunLog(t *testing.T) {
	var term strings.Builder
	l, dir := open(t, runlog.Options{Terminal: &term, Colour: true})

	l.Write(runlog.Line{Kind: runlog.Warning, Text: "careful"})

	if !strings.Contains(term.String(), "\x1b[33mcareful\x1b[0m") {
		t.Errorf("terminal = %q, want the warning in yellow", term.String())
	}
	if log := read(t, filepath.Join(dir, "run.log")); strings.Contains(log, "\x1b") {
		t.Errorf("run.log has colour: %q", log)
	}
}

func TestNoColourWithoutOne(t *testing.T) {
	var term strings.Builder
	l, _ := open(t, runlog.Options{Terminal: &term})

	l.Write(runlog.Line{Kind: runlog.Failure, Text: "broke"})

	if strings.Contains(term.String(), "\x1b") {
		t.Errorf("terminal = %q", term.String())
	}
}

func TestColourNeedsATerminalAndAnEmptyNoColor(t *testing.T) {
	cases := []struct {
		tty     bool
		noColor string
		want    bool
	}{
		{true, "", true},
		{false, "", false},
		{true, "1", false},
		{false, "1", false},
	}
	for _, c := range cases {
		if got := runlog.UseColour(c.tty, c.noColor); got != c.want {
			t.Errorf("UseColour(%v, %q) = %v, want %v", c.tty, c.noColor, got, c.want)
		}
	}
}

func TestEscapeSequencesFromAnAgentNeverReachTheTerminalOrTheLog(t *testing.T) {
	var term strings.Builder
	l, dir := open(t, runlog.Options{Terminal: &term, Verbose: true})

	l.Write(runlog.Line{Kind: runlog.AgentText, Text: "\x1b[31mred\x1b[0m \x1b]0;title\x07tail\x07"})

	for name, got := range map[string]string{"terminal": term.String(), "run.log": read(t, filepath.Join(dir, "run.log"))} {
		if strings.Contains(got, "\x1b") || strings.Contains(got, "\x07") || !strings.Contains(got, "red tail") {
			t.Errorf("%s = %q, want plain text", name, got)
		}
	}
}

func TestEverythingWrittenIsRedacted(t *testing.T) {
	var term strings.Builder
	l, dir := open(t, runlog.Options{Terminal: &term, Redactor: redact.New("hunter2-hunter2")})

	l.Write(runlog.Line{Text: "token hunter2-hunter2"})
	a := l.Attempt("2", "implement", "the prompt has hunter2-hunter2")
	a.Raw([]byte(`{"text":"hunter2-hunter2"}`))
	a.Close()
	out := l.Finish(runlog.Summary{Failed: []runlog.FailedTicket{{ID: "#2", Reason: "saw hunter2-hunter2"}}})

	files := map[string]string{
		"terminal":     term.String(),
		"stdout":       string(out),
		"run.log":      read(t, filepath.Join(dir, "run.log")),
		"summary.json": read(t, filepath.Join(dir, "summary.json")),
		"prompt":       read(t, filepath.Join(dir, "2", "implement-1.prompt.md")),
		"jsonl":        read(t, filepath.Join(dir, "2", "implement-1.jsonl")),
	}
	for name, got := range files {
		if strings.Contains(got, "hunter2") || !strings.Contains(got, "***") {
			t.Errorf("%s isn't redacted: %q", name, got)
		}
	}
}

func TestEachStepAttemptGetsItsOwnRawOutputAndPrompt(t *testing.T) {
	var term strings.Builder
	l, dir := open(t, runlog.Options{Terminal: &term})

	for i := 1; i <= 2; i++ {
		a := l.Attempt("42", "implement", fmt.Sprintf("prompt %d", i))
		a.Raw([]byte(fmt.Sprintf(`{"attempt":%d}`, i)))
		a.Close()
	}
	r := l.Attempt("42", "review", "review prompt")
	r.Close()

	for path, want := range map[string]string{
		"implement-1.prompt.md": "prompt 1",
		"implement-2.prompt.md": "prompt 2",
		"implement-1.jsonl":     "{\"attempt\":1}\n",
		"implement-2.jsonl":     "{\"attempt\":2}\n",
		"review-1.prompt.md":    "review prompt",
		"review-1.jsonl":        "",
	} {
		if got := read(t, filepath.Join(dir, "42", path)); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestFinishWritesTheVersionedSummary(t *testing.T) {
	var term strings.Builder
	root := t.TempDir()
	l, err := runlog.Open(runlog.Options{Root: root, Start: start, Terminal: &term})
	if err != nil {
		t.Fatal(err)
	}

	out := l.Finish(runlog.Summary{ExitCode: 1})

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got["schema"] != float64(1) || got["exit_code"] != float64(1) {
		t.Errorf("summary = %v", got)
	}
	for _, list := range []string{"closed", "failed", "specs_finished", "queued_writes"} {
		if _, ok := got[list].([]any); !ok {
			t.Errorf("%s = %v, want a list even when empty", list, got[list])
		}
	}
	if read(t, filepath.Join(l.Dir(), "summary.json")) != string(out) {
		t.Error("summary.json differs from what was returned")
	}
}
