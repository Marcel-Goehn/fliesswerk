package step_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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

func TestAGrandchildHoldingStdoutIsKilledWithTheHarnessAfterTheGrace(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"background": "beat"}],
		"report": {"outcome": "done", "summary": "ok"}, "linger": "1m"}]}`)
	dir := t.TempDir()
	r := step.Runner{Harness: claudecode.ClaudeCode{}, Grace: 100 * time.Millisecond}

	start := time.Now()
	out := r.Run(context.Background(), newStep("go"), dir)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the Step took %s; the tree wasn't killed after the grace", elapsed)
	}
	if out.Kind != harness.Reported {
		t.Errorf("outcome = %+v, want the report given before the kill", out)
	}
	assertStopped(t, filepath.Join(dir, "beat"))
}

func TestWhatAHarnessLeavesBehindIsKilledWhenItExits(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"background": "beat"}, {"sleep": "300ms"}],
		"report": {"outcome": "done", "summary": "ok"}}]}`)
	dir := t.TempDir()

	start := time.Now()
	out := step.Runner{Harness: claudecode.ClaudeCode{}}.Run(context.Background(), newStep("go"), dir)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the Step took %s; it waited for the leftover process", elapsed)
	}
	if out.Kind != harness.Reported {
		t.Errorf("outcome = %+v, want the report", out)
	}
	assertStopped(t, filepath.Join(dir, "beat"))
}

func TestOnLinuxAGrandchildThatLeavesTheProcessGroupIsKilledToo(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only the PID namespace on Linux catches a process that calls setsid")
	}
	withFakeHarness(t, `{"sessions": [{"actions": [{"background": "beat", "detach": true}],
		"report": {"outcome": "done", "summary": "ok"}, "linger": "1m"}]}`)
	dir := t.TempDir()
	r := step.Runner{Harness: claudecode.ClaudeCode{}, Grace: 100 * time.Millisecond}

	start := time.Now()
	r.Run(context.Background(), newStep("go"), dir)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the Step took %s; the detached grandchild kept the output open", elapsed)
	}
	assertStopped(t, filepath.Join(dir, "beat"))
}

// assertStopped fails unless the fake's background grandchild beating into
// path has started and is dead now.
func assertStopped(t *testing.T, path string) {
	t.Helper()
	size := func() int64 {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("the grandchild never ran: %v", err)
		}
		return fi.Size()
	}
	before := size()
	time.Sleep(300 * time.Millisecond)
	if after := size(); after != before {
		t.Errorf("the grandchild is still running (%s grew from %d to %d bytes)", path, before, after)
	}
}

func TestASilentStepFailsAtTheIdleTimeoutWithItsTree(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"background": "beat"}, {"sleep": "1m"}],
		"report": {"outcome": "done", "summary": "ok"}}]}`)
	dir := t.TempDir()
	r := step.Runner{Harness: claudecode.ClaudeCode{}, IdleTimeout: 500 * time.Millisecond}

	start := time.Now()
	out := r.Run(context.Background(), newStep("go"), dir)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the Step took %s; the idle timeout didn't fire", elapsed)
	}
	if out.Kind != harness.Failed || out.Class != harness.Other || !strings.Contains(out.Message, "idle timeout") {
		t.Errorf("outcome = %+v, want an idle timeout failure", out)
	}
	assertStopped(t, filepath.Join(dir, "beat"))
}

func TestAStepThatKeepsTalkingFailsAtTheStepTimeout(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"chatter": "1m"}],
		"report": {"outcome": "done", "summary": "ok"}}]}`)
	r := step.Runner{Harness: claudecode.ClaudeCode{}, IdleTimeout: 500 * time.Millisecond, Timeout: 1500 * time.Millisecond}

	start := time.Now()
	out := r.Run(context.Background(), newStep("go"), t.TempDir())

	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 6*time.Second {
		t.Errorf("the Step took %s; want it killed at the Step timeout, not the idle one", elapsed)
	}
	if out.Kind != harness.Failed || out.Class != harness.Other || !strings.Contains(out.Message, "Step timeout") {
		t.Errorf("outcome = %+v, want a Step timeout failure", out)
	}
}

func TestEveryStepGetsTheNonInteractiveGitEnv(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}}]}`)
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	t.Setenv("KWK_FAKE_HARNESS_LOG", logPath)
	t.Setenv("GIT_EDITOR", "vim")

	out := step.Runner{Harness: claudecode.ClaudeCode{}}.Run(context.Background(), newStep("go"), t.TempDir())

	if out.Kind != harness.Reported {
		t.Fatalf("outcome = %+v, want a report", out)
	}
	log := readInvocation(t, logPath)
	for k, v := range map[string]string{"GIT_TERMINAL_PROMPT": "0", "GIT_EDITOR": "true", "GIT_PAGER": "cat"} {
		if log.Env[k] != v {
			t.Errorf("%s = %q, want %q", k, log.Env[k], v)
		}
	}
}

func TestCredentialsGoIntoTheStepEnvNeverItsArgv(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}}]}`)
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	t.Setenv("KWK_FAKE_HARNESS_LOG", logPath)
	t.Setenv("KWK_FAKE_LOG_ENV", "DATABASE_URL")
	// The user's own key would win over the configured OAuth token.
	t.Setenv("ANTHROPIC_API_KEY", "the-users-own-key")

	const dbURL = "postgres://u:pw@db/app" //nolint:gosec // a made-up secret
	r := step.Runner{
		Harness: claudecode.ClaudeCode{},
		Env:     map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "oauth-secret", "DATABASE_URL": dbURL},
		Unset:   []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
	}
	out := r.Run(context.Background(), newStep("go"), t.TempDir())

	if out.Kind != harness.Reported {
		t.Fatalf("outcome = %+v, want a report", out)
	}
	log := readInvocation(t, logPath)
	if log.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "oauth-secret" || log.Env["DATABASE_URL"] != dbURL {
		t.Errorf("env = %v, want the credential and the project secret", log.Env)
	}
	if _, ok := log.Env["ANTHROPIC_API_KEY"]; ok {
		t.Errorf("env = %v, want the user's own ANTHROPIC_API_KEY left out", log.Env)
	}
	if argv := strings.Join(log.Argv, " "); strings.Contains(argv, "oauth-secret") || strings.Contains(argv, "pw@db") {
		t.Errorf("a secret is in argv: %q", log.Argv)
	}
}

// npmShim is the shim npm writes for a package whose bin is an executable.
const npmShim = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n" +
	"\"%dp0%\\node_modules\\fake\\bin\\claude.exe\"   %*\r\n"

func TestOnWindowsACmdShimIsFollowedToTheRealExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd shims are a Windows thing")
	}
	path := os.Getenv("PATH")
	withFakeHarness(t, `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}}]}`)
	shimDir := t.TempDir()
	target := filepath.Join(shimDir, "node_modules", "fake", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	exe, err := os.ReadFile(filepath.Join(fakeDir, "claude.exe")) //nolint:gosec // the fake this test built
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, exe, 0o700); err != nil { //nolint:gosec // an executable must be executable
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "claude.cmd"), []byte(npmShim), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only the shim is on PATH, not the fake itself.
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+path)
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	t.Setenv("KWK_FAKE_HARNESS_LOG", logPath)

	s := newStep("go")
	out := step.Runner{Harness: claudecode.ClaudeCode{}}.Run(context.Background(), s, t.TempDir())

	if out.Kind != harness.Reported {
		t.Fatalf("outcome = %+v, want a report", out)
	}
	log := readInvocation(t, logPath)
	if want := (claudecode.ClaudeCode{}).Command(s).Argv[1:]; strings.Join(log.Argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("the Harness got argv %q, want %q unchanged", log.Argv, want)
	}
}

// invocation is how the fake Harness logged it was started.
type invocation struct {
	Argv []string          `json:"argv"`
	Env  map[string]string `json:"env"`
}

// readInvocation reads the one invocation the fake Harness logged at path.
func readInvocation(t *testing.T, path string) invocation {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // the test names its log
	if err != nil {
		t.Fatal(err)
	}
	var inv invocation
	if err := json.Unmarshal(data, &inv); err != nil {
		t.Fatal(err)
	}
	return inv
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

func TestEveryRawLineOfTheHarnessIsHandedOver(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"text": "working"}], "report": {"outcome": "done", "summary": "ok"}}]}`)
	var mu sync.Mutex
	var raw []string
	r := step.Runner{Harness: claudecode.ClaudeCode{}, OnRaw: func(line []byte) {
		mu.Lock()
		defer mu.Unlock()
		raw = append(raw, string(line))
	}}

	r.Run(context.Background(), newStep("go"), t.TempDir())

	mu.Lock()
	defer mu.Unlock()
	if len(raw) < 3 || !strings.Contains(raw[0], `"subtype":"init"`) || !strings.Contains(raw[len(raw)-1], `"type":"result"`) {
		t.Errorf("raw lines = %q, want the session from its init line to its result line", raw)
	}
}

func TestASilentStepIsWarnedAboutAtHalfTheIdleTimeout(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"sleep": "1m"}], "report": {"outcome": "done", "summary": "ok"}}]}`)
	var warnings atomic.Int32
	var after time.Duration
	r := step.Runner{Harness: claudecode.ClaudeCode{}, IdleTimeout: time.Second, OnIdleWarning: func(silent time.Duration) {
		after = silent
		warnings.Add(1)
	}}

	r.Run(context.Background(), newStep("go"), t.TempDir())

	if n := warnings.Load(); n != 1 {
		t.Fatalf("got %d idle warnings, want one", n)
	}
	if after != 500*time.Millisecond {
		t.Errorf("warned after %s of silence, want half the idle timeout", after)
	}
}

func TestAStepThatKeepsTalkingGetsNoIdleWarning(t *testing.T) {
	withFakeHarness(t, `{"sessions": [{"actions": [{"chatter": "1s"}], "report": {"outcome": "done", "summary": "ok"}}]}`)
	var warnings atomic.Int32
	r := step.Runner{Harness: claudecode.ClaudeCode{}, IdleTimeout: 2 * time.Second, OnIdleWarning: func(time.Duration) { warnings.Add(1) }}

	r.Run(context.Background(), newStep("go"), t.TempDir())

	if n := warnings.Load(); n != 0 {
		t.Errorf("got %d idle warnings, want none", n)
	}
}
