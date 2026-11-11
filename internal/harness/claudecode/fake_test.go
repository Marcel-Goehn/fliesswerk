package claudecode_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness/claudecode"
)

var (
	fakeOnce sync.Once
	fakeDir  string
	fakeErr  error
)

// fakeHarnessDir builds the scripted fake Harness as "claude" once per test
// binary and returns its directory, for PATH.
func fakeHarnessDir(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeclaude")
		if err != nil {
			fakeErr = err
			return
		}
		name := "claude"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cmd := exec.Command("go", "build", "-tags", "fwkfake", "-o", filepath.Join(dir, name), "../fakeclaude")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = errors.New("go build fakeclaude: " + err.Error() + "\n" + string(out))
			return
		}
		fakeDir = dir
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeDir
}

type fakeRun struct {
	events  []harness.Event
	outcome harness.Outcome
	work    string
	log     map[string]any
}

// runFake runs the fake Harness the way the core will: from Command's
// Invocation, in a work dir, then parses its stdout with Parse.
func runFake(t *testing.T, script string, s harness.Step) fakeRun {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o750); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script.json")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "log.jsonl")

	inv := claudecode.ClaudeCode{}.Command(s)
	bin, err := exec.LookPath(filepath.Join(fakeHarnessDir(t), inv.Argv[0]))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, inv.Argv[1:]...)
	cmd.Dir = work
	cmd.Stdin = strings.NewReader(inv.Stdin)
	cmd.Env = append(os.Environ(), "FWK_FAKE_HARNESS_SCRIPT="+scriptPath, "FWK_FAKE_HARNESS_LOG="+logPath)
	for k, v := range inv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		exit = ee.ExitCode()
	}

	run := fakeRun{work: work}
	sc := bufio.NewScanner(&stdout)
	for sc.Scan() {
		run.events = append(run.events, claudecode.ClaudeCode{}.Parse(sc.Bytes())...)
	}
	run.outcome = harness.Resolve(run.events, exit)
	if data, err := os.ReadFile(logPath); err == nil { //nolint:gosec // the test's own temp file
		if err := json.Unmarshal(data, &run.log); err != nil {
			t.Fatalf("log: %v", err)
		}
	} else {
		t.Fatalf("no invocation logged (exit %d): %s", exit, stderr.String())
	}
	return run
}

func TestFakeHarnessEditsFilesAndReports(t *testing.T) {
	script := `{"sessions": [
		{"match": "#7", "report": {"outcome": "cannot_do", "reason": "wrong one", "summary": "-"}},
		{"match": "#42", "actions": [
			{"text": "Adding the cart."},
			{"write": "src/cart.go", "content": "package cart\n"}
		], "report": {"outcome": "done", "summary": "Added the cart."}}
	]}`
	run := runFake(t, script, step())

	want := harness.Report{Outcome: "done", Summary: "Added the cart."}
	if run.outcome.Kind != harness.Reported || run.outcome.Report != want {
		t.Fatalf("outcome = %+v, want report %+v", run.outcome, want)
	}
	if run.outcome.Usage.Input == 0 || run.outcome.Usage.CostUnit != harness.USD {
		t.Errorf("usage = %+v, want tokens and a USD cost", run.outcome.Usage)
	}
	got, err := os.ReadFile(filepath.Join(run.work, "src", "cart.go"))
	if err != nil || string(got) != "package cart\n" {
		t.Errorf("src/cart.go = %q, %v", got, err)
	}
	var kinds []string
	for _, e := range run.events {
		if e.Kind == harness.Text || e.Kind == harness.ToolCall {
			kinds = append(kinds, e.Kind.String()+":"+e.Text+e.Tool)
		}
	}
	if want := []string{"text:Adding the cart.", "tool_call:Write", "tool_call:StructuredOutput"}; strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("events = %q, want %q", kinds, want)
	}
}

func TestFakeHarnessGetsThePromptOnStdin(t *testing.T) {
	run := runFake(t, `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}}]}`, step())

	if run.log["prompt"] != prompt {
		t.Errorf("prompt = %q, want %q", run.log["prompt"], prompt)
	}
}

func TestFakeHarnessRunsCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	run := runFake(t, `{"sessions": [{"actions": [{"run": ["git", "init", "-q", "."]}], "report": {"outcome": "done", "summary": "ok"}}]}`, step())

	if _, err := os.Stat(filepath.Join(run.work, ".git")); err != nil {
		t.Errorf("git init didn't run in the work dir: %v", err)
	}
}

func TestFakeHarnessEndsInEachOutcome(t *testing.T) {
	tests := []struct {
		session string
		kind    harness.OutcomeKind
		class   harness.ErrorClass
	}{
		{`{}`, harness.NoReport, ""},
		{`{"error": "auth"}`, harness.Failed, harness.Auth},
		{`{"error": "transient"}`, harness.Failed, harness.Transient},
		{`{"error": "other", "exit": 0}`, harness.Failed, harness.Other},
	}
	for _, tt := range tests {
		t.Run(tt.session, func(t *testing.T) {
			run := runFake(t, `{"sessions": [`+tt.session+`]}`, step())
			if run.outcome.Kind != tt.kind || run.outcome.Class != tt.class {
				t.Errorf("outcome = %+v, want %v %q", run.outcome, tt.kind, tt.class)
			}
		})
	}
}

// TestFakeHarnessNeverShips checks that without the test-only build tag
// there is nothing to build, so no release can contain it.
func TestFakeHarnessNeverShips(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.GoFiles}}", "../fakeclaude").CombinedOutput()
	if err == nil && strings.TrimSpace(string(out)) != "[]" {
		t.Errorf("fakeclaude builds without the fwkfake tag: %s", out)
	}
}
