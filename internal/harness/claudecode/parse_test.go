package claudecode_test

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness/claudecode"
)

// parseFile feeds a golden transcript into Parse, line by line.
func parseFile(t *testing.T, path string) []harness.Event {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // a golden transcript named by the test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var events []harness.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		events = append(events, claudecode.ClaudeCode{}.Parse(sc.Bytes())...)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func text(s string) harness.Event { return harness.Event{Kind: harness.Text, Text: s} }

func tool(name, summary string) harness.Event {
	return harness.Event{Kind: harness.ToolCall, Tool: name, Summary: summary}
}

func call(id string, in, out, read, write int64) harness.Event {
	return harness.Event{Kind: harness.UsageReport, CallID: id, Usage: harness.Usage{Input: in, Output: out, CacheRead: read, CacheWrite: write}}
}

func total(u harness.Usage) harness.Event {
	return harness.Event{Kind: harness.UsageReport, Cumulative: true, Usage: u}
}

func usd(in, out, read, write int64, cost float64) harness.Usage {
	return harness.Usage{Input: in, Output: out, CacheRead: read, CacheWrite: write, Cost: cost, CostUnit: harness.USD}
}

func failed(class harness.ErrorClass, msg string, terminal bool) harness.Event {
	return harness.Event{Kind: harness.Error, Class: class, Text: msg, Terminal: terminal}
}

func final(r *harness.Report) harness.Event {
	return harness.Event{Kind: harness.Final, Report: r, Terminal: true}
}

func reported(r harness.Report, u harness.Usage) harness.Outcome {
	return harness.Outcome{Kind: harness.Reported, Report: r, Usage: u}
}

// TestParseGoldenTranscripts is the contract test: golden Claude Code
// `stream-json` transcripts (see testdata/README.md) go into Parse, and each Step resolves to
// exactly one outcome.
func TestParseGoldenTranscripts(t *testing.T) {
	doneReport := harness.Report{Outcome: "done", Summary: "Added Cart.Total and its tests."}
	cannotDo := harness.Report{Outcome: "cannot_do", Reason: "No payment provider is configured.", Summary: "Nothing changed."}
	const invalidKey = "Invalid API key · Please run /login"
	const limit = "You've hit your limit · resets 3pm (Europe/Berlin)"
	const expired = `API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`
	const overloaded = `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`

	tests := []struct {
		file    string
		exit    int
		events  []harness.Event
		outcome harness.Outcome
	}{
		{
			file: "done.jsonl",
			events: []harness.Event{
				call("msg_01A", 3, 8, 11000, 5120),
				text("I'll start by reading the cart."), call("msg_01A", 3, 8, 11000, 5120),
				tool("Read", "/work/src/cart.go"), call("msg_01A", 3, 96, 11000, 5120),
				tool("Bash", "go test ./... && git commit -am 'Add cart total'"), call("msg_01B", 6, 120, 16120, 300),
				tool("StructuredOutput", ""), call("msg_01C", 4, 40, 16420, 210),
				total(usd(13, 256, 43540, 5630, 0.0912345)), final(&doneReport),
			},
			outcome: withContext(reported(doneReport, usd(13, 256, 43540, 5630, 0.0912345)), 16634),
		},
		{
			file: "cannot_do.jsonl",
			events: []harness.Event{
				text("The Ticket asks for a payment provider\nthat the repo doesn't have."), call("msg_02A", 10, 30, 2000, 0),
				total(usd(10, 30, 2000, 0, 0.004)), final(&cannotDo),
			},
			outcome: withContext(reported(cannotDo, usd(10, 30, 2000, 0, 0.004)), 2010),
		},
		{
			file: "no_report.jsonl",
			events: []harness.Event{
				text("All done!"), call("msg_03A", 5, 4, 0, 100),
				total(usd(5, 4, 0, 100, 0.001)), final(nil),
			},
			outcome: harness.Outcome{Kind: harness.NoReport, Usage: usd(5, 4, 0, 100, 0.001), ContextWindow: 105},
		},
		{
			// A report that doesn't match the schema is no report.
			file:    "bad_report.jsonl",
			events:  []harness.Event{total(usd(5, 4, 0, 0, 0.001)), final(nil)},
			outcome: harness.Outcome{Kind: harness.NoReport, Usage: usd(5, 4, 0, 0, 0.001)},
		},
		{
			file:    "structured_output_retries.jsonl",
			events:  []harness.Event{total(usd(50, 300, 8000, 0, 0.02)), final(nil)},
			outcome: harness.Outcome{Kind: harness.NoReport, Usage: usd(50, 300, 8000, 0, 0.02)},
		},
		{
			// Exit 1, but the class is read from the stream.
			file: "auth.jsonl",
			exit: 1,
			events: []harness.Event{
				failed(harness.Auth, invalidKey, false),
				total(usd(0, 0, 0, 0, 0)), failed(harness.Auth, invalidKey, true),
			},
			outcome: harness.Outcome{Kind: harness.Failed, Class: harness.Auth, Message: invalidKey, Usage: usd(0, 0, 0, 0, 0)},
		},
		{
			file:    "oauth_expired.jsonl",
			exit:    1,
			events:  []harness.Event{total(usd(0, 0, 0, 0, 0)), failed(harness.Auth, expired, true)},
			outcome: harness.Outcome{Kind: harness.Failed, Class: harness.Auth, Message: expired, Usage: usd(0, 0, 0, 0, 0)},
		},
		{
			file: "rate_limit.jsonl",
			exit: 1,
			events: []harness.Event{
				failed(harness.Transient, limit, false),
				total(usd(0, 0, 0, 0, 0)), failed(harness.Transient, limit, true),
			},
			outcome: harness.Outcome{Kind: harness.Failed, Class: harness.Transient, Message: limit, Usage: usd(0, 0, 0, 0, 0)},
		},
		{
			file:    "overloaded.jsonl",
			exit:    1,
			events:  []harness.Event{total(usd(20, 10, 1000, 0, 0.012)), failed(harness.Transient, overloaded, true)},
			outcome: harness.Outcome{Kind: harness.Failed, Class: harness.Transient, Message: overloaded, Usage: usd(20, 10, 1000, 0, 0.012)},
		},
		{
			// Failures are read from the stream even when the exit code is 0.
			file:    "execution_error.jsonl",
			exit:    0,
			events:  []harness.Event{total(usd(30, 60, 500, 10, 0.03)), failed(harness.Other, "Error: spawn /bin/sh ENOENT", true)},
			outcome: harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "Error: spawn /bin/sh ENOENT", Usage: usd(30, 60, 500, 10, 0.03)},
		},
		{
			// Killed before the result line: per-call usage is counted
			// once per call, and no cost is made up.
			file: "killed.jsonl",
			exit: -1,
			events: []harness.Event{
				tool("Bash", "npm run dev"), call("msg_06A", 7, 20, 900, 40),
				tool("Grep", "func Total"), call("msg_06A", 7, 35, 900, 40),
				text("Waiting for the server."), call("msg_06B", 2, 6, 960, 0),
			},
			outcome: harness.Outcome{
				Kind: harness.Failed, Class: harness.Other, Message: "exited with code -1 before the end of the session",
				Usage: harness.Usage{Input: 9, Output: 41, CacheRead: 1860, CacheWrite: 40}, ContextWindow: 962,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			events := parseFile(t, filepath.Join("testdata", tt.file))
			if !reflect.DeepEqual(events, tt.events) {
				t.Errorf("events:\n got %+v\nwant %+v", events, tt.events)
			}
			if got := harness.Resolve(events, tt.exit); !reflect.DeepEqual(got, tt.outcome) {
				t.Errorf("outcome:\n got %+v\nwant %+v", got, tt.outcome)
			}
		})
	}
}

// withContext is out with the context window worked out from its fixture's
// last assistant line.
func withContext(out harness.Outcome, window int64) harness.Outcome {
	out.ContextWindow = window
	return out
}

func TestParseIgnoresLinesItDoesNotKnow(t *testing.T) {
	for _, line := range []string{
		"",
		"not json",
		`{"type":"system","subtype":"init","session_id":"x"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
	} {
		if events := (claudecode.ClaudeCode{}).Parse([]byte(line)); len(events) != 0 {
			t.Errorf("Parse(%q) = %+v, want none", line, events)
		}
	}
}

func TestParseSummarisesToolCallsOnOneLine(t *testing.T) {
	long := "echo " + strings.Repeat("a", 200)
	tests := []struct {
		input string
		want  string
	}{
		{`{"file_path":"/work/a.go","content":"x"}`, "/work/a.go"},
		{`{"pattern":"**/*.go"}`, "**/*.go"},
		{`{"url":"https://go.dev","prompt":"read it"}`, "https://go.dev"},
		{`{"query":"go generics"}`, "go generics"},
		{`{"description":"Find callers","prompt":"…","subagent_type":"Explore"}`, "Find callers"},
		{`{"command":"` + long + `"}`, long[:99] + "…"},
		{`{"todos":[]}`, ""},
	}
	for _, tt := range tests {
		line := `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"X","input":` + tt.input + `}],"usage":{"input_tokens":1,"output_tokens":1}}}`
		events := claudecode.ClaudeCode{}.Parse([]byte(line))
		if len(events) == 0 || events[0].Kind != harness.ToolCall {
			t.Fatalf("Parse(%s) = %+v, want a tool_call first", tt.input, events)
		}
		if events[0].Summary != tt.want {
			t.Errorf("summary of %s = %q, want %q", tt.input, events[0].Summary, tt.want)
		}
	}
}

func TestAHarnessKilledBeforeItsResultLineHasRunOutOfMemory(t *testing.T) {
	events := parseFile(t, filepath.Join("testdata", "killed.jsonl"))

	got := harness.Resolve(events, 137)

	if got.Kind != harness.Failed || got.Class != harness.OutOfMemory {
		t.Errorf("outcome = %+v, want a failure of class %q", got, harness.OutOfMemory)
	}
}

func TestExit137AfterTheResultLineIsNotOutOfMemory(t *testing.T) {
	events := parseFile(t, filepath.Join("testdata", "done.jsonl"))

	if got := harness.Resolve(events, 137); got.Kind != harness.Reported {
		t.Errorf("outcome = %+v, want the report: the core kills a Harness that lingers", got)
	}
}
