package step_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness/claudecode"
	"github.com/Marcel-Goehn/kraftwerk/internal/step"
)

var (
	fakeOnce sync.Once
	fakeDir  string
	fakeErr  error
)

// withFakeHarness builds the scripted fake Harness as "claude" once per
// test binary, puts it first on PATH and points it at script.
func withFakeHarness(t *testing.T, script string) {
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
		cmd := exec.Command("go", "build", "-tags", "kwkfake", "-o", filepath.Join(dir, name), "../harness/fakeclaude")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = errors.New("go build fakeclaude: " + err.Error() + "\n" + string(out))
			return
		}
		fakeDir = dir
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KWK_FAKE_HARNESS_SCRIPT", path)
}

func newStep(prompt string) harness.Step {
	return harness.Step{Prompt: prompt, ReportSchema: harness.ReportSchema, ScrubCredentials: true}
}

func TestAStepRunsInItsDirWithThePromptOnStdin(t *testing.T) {
	withFakeHarness(t, `{"sessions": [
		{"match": "#9", "report": {"outcome": "cannot_do", "reason": "wrong prompt", "summary": "-"}},
		{"match": "#42", "actions": [{"write": "cart.go", "content": "package cart\n"}],
		 "report": {"outcome": "done", "summary": "Added the cart."}}
	]}`)
	dir := t.TempDir()
	var tools []string
	r := step.Runner{Harness: claudecode.ClaudeCode{}, OnEvent: func(e harness.Event) {
		if e.Kind == harness.ToolCall {
			tools = append(tools, e.Tool)
		}
	}}

	out := r.Run(context.Background(), newStep("Implement Ticket #42."), dir)

	want := harness.Report{Outcome: harness.Done, Summary: "Added the cart."}
	if out.Kind != harness.Reported || out.Report != want {
		t.Fatalf("outcome = %+v, want report %+v", out, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "cart.go")); err != nil {
		t.Errorf("the Step didn't run in its dir: %v", err)
	}
	if strings.Join(tools, ",") != "Write,StructuredOutput" {
		t.Errorf("tool calls seen = %q, want Write,StructuredOutput", tools)
	}
}

func TestAHarnessLingeringAfterItsResultIsKilledAfterTheGrace(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}, "linger": "1m"}]}`)
	r := step.Runner{Harness: claudecode.ClaudeCode{}, Grace: 100 * time.Millisecond}

	start := time.Now()
	out := r.Run(context.Background(), newStep("go"), t.TempDir())

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("the Step took %s; the lingering Harness wasn't killed", elapsed)
	}
	if out.Kind != harness.Reported {
		t.Fatalf("outcome = %+v, want the report given before the kill", out)
	}
}

func TestACancelledStepIsKilled(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"text": "thinking"}], "linger": "1m"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	out := step.Runner{Harness: claudecode.ClaudeCode{}}.Run(ctx, newStep("go"), t.TempDir())

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("the Step took %s; it wasn't killed", elapsed)
	}
	if out.Kind == harness.Reported {
		t.Fatalf("outcome = %+v, want no report", out)
	}
}

func TestAMissingHarnessFailsTheStep(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out := step.Runner{Harness: claudecode.ClaudeCode{}}.Run(context.Background(), newStep("go"), t.TempDir())

	if out.Kind != harness.Failed || !strings.Contains(out.Message, "claude") {
		t.Fatalf("outcome = %+v, want a failure naming the binary", out)
	}
}
