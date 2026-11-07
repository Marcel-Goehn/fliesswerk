//go:build kwkfake

// Command fakeclaude is a scripted fake Harness: it stands in for Claude
// Code in end-to-end tests, so they never spend tokens. It is built only
// with the test-only `kwkfake` build tag, so no release ever ships it.
//
// Like `claude -p --output-format stream-json`, it reads the prompt from
// stdin and writes Claude Code's events to stdout. What it does comes
// from the JSON script at $KWK_FAKE_HARNESS_SCRIPT:
//
//	{"sessions": [{
//	  "match":   "#42",          // a substring of the prompt; "" matches any
//	  "actions": [
//	    {"text": "Reading the cart."},
//	    {"write": "src/cart.go", "content": "package cart\n"},
//	    {"run": ["git", "commit", "-am", "Add cart"]},
//	    {"background": "beat"},  // start a grandchild that keeps stdout open
//	    {"background": "beat", "detach": true},  // … in its own session (setsid)
//	    {"sleep": "1m"},         // go silent
//	    {"chatter": "1m"}        // write a text event every 50ms
//	  ],
//	  "report":  {"outcome": "done", "summary": "…"},  // left out: no report
//	  "error":   "auth",         // auth | transient | other: fail instead
//	  "exit":    0,              // exit code; default 1 after an error, else 0
//	  "linger":  "1m",           // keep running after the result line
//	  "expand":  true            // fill in $VAR from the env everywhere
//	}]}
//
// The first session whose match is in the prompt runs. Every invocation is
// appended to $KWK_FAKE_HARNESS_LOG, if set, as a JSON line
// {"argv": […], "prompt": "…", "env": {…}} with the CLAUDE_CODE_*,
// ANTHROPIC_* and GIT_* env, and the variables named in the
// comma-separated $KWK_FAKE_LOG_ENV.
//
// A `background` grandchild inherits stdout and stderr and appends a byte
// to the named file, relative to the work dir, every 20ms until it is
// killed: a test sees it is dead once the file stops growing.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type script struct {
	Sessions []session `json:"sessions"`
}

type session struct {
	Match   string          `json:"match"`
	Actions []action        `json:"actions"`
	Report  json.RawMessage `json:"report"`
	Error   string          `json:"error"`
	Exit    *int            `json:"exit"`
	Linger  string          `json:"linger"`
	// Expand fills $VAR in texts, commands, written content and the
	// report from the env, as an agent that leaks a secret would.
	Expand bool `json:"expand"`
}

type action struct {
	Text    string   `json:"text"`
	Write   string   `json:"write"`
	Content string   `json:"content"`
	Run     []string `json:"run"`
	// Background, Sleep and Chatter are a file name and durations.
	Background string `json:"background"`
	Sleep      string `json:"sleep"`
	Chatter    string `json:"chatter"`
	Detach     bool   `json:"detach"`
}

// envBeat makes the fake a background grandchild beating into this file.
const envBeat = "KWK_FAKE_BEAT"

const sessionID = "00000000-0000-4000-8000-00000000fa4e"

func main() {
	if path := os.Getenv(envBeat); path != "" {
		beat(path)
	}
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("2.1.319 (Claude Code)")
		return
	}
	if !slices.Contains(args, "-p") || !hasFlag(args, "--output-format", "stream-json") {
		fail("fakeclaude: only `-p --output-format stream-json` is supported, got %q", args)
	}
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail("fakeclaude: %v", err)
	}
	prompt := string(in)
	if strings.TrimSpace(prompt) == "" {
		fail("Error: Input must be provided either through stdin or as a prompt argument when using --print")
	}
	logInvocation(args, prompt)

	s := pick(prompt)
	w := writer{}
	w.emit(map[string]any{
		"type": "system", "subtype": "init", "cwd": cwd(), "session_id": sessionID,
		"tools": []string{"Bash", "Read", "Write", "StructuredOutput"}, "mcp_servers": []any{},
		"model": flagValue(args, "--model"), "permissionMode": flagValue(args, "--permission-mode"),
		"apiKeySource": "none", "claude_code_version": "2.1.319",
	})
	if s.Expand {
		s.expand()
	}
	for _, a := range s.Actions {
		w.act(a)
	}
	switch {
	case s.Error != "":
		w.fail(s.Error)
	case len(s.Report) > 0:
		w.toolCall("StructuredOutput", s.Report, "Structured output provided successfully", false)
		w.result(s.Report)
	default:
		w.result(nil)
	}

	if s.Linger != "" {
		time.Sleep(duration(s.Linger))
	}
	code := 0
	if s.Error != "" {
		code = 1
	}
	if s.Exit != nil {
		code = *s.Exit
	}
	os.Exit(code)
}

func (s *session) expand() {
	for i := range s.Actions {
		a := &s.Actions[i]
		a.Text, a.Content = os.ExpandEnv(a.Text), os.ExpandEnv(a.Content)
		for j := range a.Run {
			a.Run[j] = os.ExpandEnv(a.Run[j])
		}
	}
	if len(s.Report) > 0 {
		s.Report = json.RawMessage(os.ExpandEnv(string(s.Report)))
	}
}

func pick(prompt string) session {
	path := os.Getenv("KWK_FAKE_HARNESS_SCRIPT")
	data, err := os.ReadFile(path) //nolint:gosec // the test that runs the fake names its script
	if err != nil {
		fail("fakeclaude: KWK_FAKE_HARNESS_SCRIPT: %v", err)
	}
	var sc script
	if err := json.Unmarshal(data, &sc); err != nil {
		fail("fakeclaude: %s: %v", path, err)
	}
	for _, s := range sc.Sessions {
		if strings.Contains(prompt, s.Match) {
			return s
		}
	}
	fail("fakeclaude: no session in %s matches the prompt", path)
	return session{}
}

// writer writes Claude Code `stream-json` lines and keeps the usage
// totals for the result line.
type writer struct {
	calls  int
	input  int64
	output int64
}

// Every model call reports the same small usage.
const (
	callInput   = 100
	callOutput  = 20
	costPerCall = 0.001
)

func (w *writer) emit(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		fail("fakeclaude: %v", err)
	}
	_, _ = os.Stdout.Write(append(line, '\n'))
}

// assistant writes one model call with one content block.
func (w *writer) assistant(block map[string]any) {
	w.calls++
	w.input += callInput
	w.output += callOutput
	w.emit(map[string]any{
		"type": "assistant", "session_id": sessionID, "parent_tool_use_id": nil,
		"message": map[string]any{
			"id": fmt.Sprintf("msg_fake_%d", w.calls), "type": "message", "role": "assistant", "model": "fake",
			"content": []any{block},
			"usage": map[string]any{
				"input_tokens": callInput, "output_tokens": callOutput,
				"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0,
			},
		},
	})
}

func (w *writer) toolCall(name string, input any, result string, isError bool) {
	id := fmt.Sprintf("toolu_fake_%d", w.calls+1)
	w.assistant(map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	w.emit(map[string]any{
		"type": "user", "session_id": sessionID, "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": isError},
		}},
	})
}

func (w *writer) act(a action) {
	switch {
	case a.Text != "":
		w.assistant(map[string]any{"type": "text", "text": a.Text})
	case a.Write != "":
		path := filepath.Join(cwd(), filepath.FromSlash(a.Write))
		err := os.MkdirAll(filepath.Dir(path), 0o750)
		if err == nil {
			err = os.WriteFile(path, []byte(a.Content), 0o600)
		}
		result := "File created successfully at: " + path
		if err != nil {
			result = err.Error()
		}
		w.toolCall("Write", map[string]any{"file_path": path, "content": a.Content}, result, err != nil)
	case a.Background != "":
		self, err := os.Executable()
		if err != nil {
			fail("fakeclaude: %v", err)
		}
		cmd := exec.Command(self) //nolint:gosec // the fake starts itself
		cmd.Env = append(os.Environ(), envBeat+"="+filepath.Join(cwd(), filepath.FromSlash(a.Background)))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if a.Detach {
			detach(cmd)
		}
		if err := cmd.Start(); err != nil {
			fail("fakeclaude: background: %v", err)
		}
	case a.Sleep != "":
		time.Sleep(duration(a.Sleep))
	case a.Chatter != "":
		for end := time.Now().Add(duration(a.Chatter)); time.Now().Before(end); {
			w.assistant(map[string]any{"type": "text", "text": "still working"})
			time.Sleep(50 * time.Millisecond)
		}
	case len(a.Run) > 0:
		cmd := exec.Command(a.Run[0], a.Run[1:]...) //nolint:gosec // a test script names the command
		out, err := cmd.CombinedOutput()
		w.toolCall("Bash", map[string]any{"command": strings.Join(a.Run, " ")}, string(out), err != nil)
	}
}

func (w *writer) usage() map[string]any {
	return map[string]any{
		"input_tokens": w.input, "output_tokens": w.output,
		"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0,
	}
}

func (w *writer) cost() float64 { return float64(w.calls) * costPerCall }

func (w *writer) result(report json.RawMessage) {
	r := map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "num_turns": w.calls,
		"result": "", "session_id": sessionID, "total_cost_usd": w.cost(), "usage": w.usage(),
		"permission_denials": []any{},
	}
	if report != nil {
		r["structured_output"] = report
	}
	w.emit(r)
}

// fail ends the session with an error the way Claude Code reports it.
func (w *writer) fail(class string) {
	var kind, msg string
	switch class {
	case "auth":
		kind, msg = "authentication_failed", "Invalid API key · Please run /login"
	case "transient":
		kind, msg = "server_error", `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	case "other":
		w.emit(map[string]any{
			"type": "result", "subtype": "error_during_execution", "is_error": true, "num_turns": w.calls,
			"session_id": sessionID, "total_cost_usd": w.cost(), "usage": w.usage(),
			"errors": []string{"Error: scripted failure"},
		})
		return
	default:
		fail("fakeclaude: unknown error class %q", class)
	}
	w.emit(map[string]any{
		"type": "assistant", "session_id": sessionID, "parent_tool_use_id": nil, "error": kind,
		"message": map[string]any{
			"id": "synthetic", "type": "message", "role": "assistant", "model": "<synthetic>",
			"content": []any{map[string]any{"type": "text", "text": msg}},
			"usage":   map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	w.emit(map[string]any{
		"type": "result", "subtype": "success", "is_error": true, "num_turns": w.calls + 1,
		"result": msg, "session_id": sessionID, "total_cost_usd": w.cost(), "usage": w.usage(),
	})
}

func logInvocation(args []string, prompt string) {
	path := os.Getenv("KWK_FAKE_HARNESS_LOG")
	if path == "" {
		return
	}
	env := map[string]string{}
	extra := strings.Split(os.Getenv("KWK_FAKE_LOG_ENV"), ",")
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE_CODE_") || strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "GIT_") || slices.Contains(extra, k) {
			env[k] = v
		}
	}
	line, _ := json.Marshal(map[string]any{"argv": args, "prompt": prompt, "env": env})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test that runs the fake names its log
	if err != nil {
		fail("fakeclaude: %v", err)
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

func duration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		fail("fakeclaude: %v", err)
	}
	return d
}

// beat appends a byte to path every 20ms, forever.
func beat(path string) {
	for {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test names the file
		if err == nil {
			_, _ = f.Write([]byte{'.'})
			_ = f.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func hasFlag(args []string, name, value string) bool { return flagValue(args, name) == value }

func flagValue(args []string, name string) string {
	if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		fail("fakeclaude: %v", err)
	}
	return dir
}

func fail(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
