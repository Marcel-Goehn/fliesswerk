package claudecode_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness/claudecode"
)

const prompt = "Implement #42.\nIt's a long prompt with \"quotes\" & newlines."

func step() harness.Step {
	return harness.Step{
		Prompt:           prompt,
		Model:            "claude-sonnet-5-5",
		ReportSchema:     harness.ReportSchema,
		ScrubCredentials: true,
	}
}

// flag returns the value after name in argv, and whether name is there.
func flag(argv []string, name string) (string, bool) {
	i := slices.Index(argv, name)
	if i < 0 {
		return "", false
	}
	if i+1 < len(argv) {
		return argv[i+1], true
	}
	return "", true
}

func TestCommandPutsThePromptOnStdinNeverInArgv(t *testing.T) {
	inv := claudecode.ClaudeCode{}.Command(step())

	if inv.Stdin != prompt {
		t.Errorf("stdin = %q, want the prompt", inv.Stdin)
	}
	for _, a := range inv.Argv {
		if strings.Contains(a, "Implement #42") {
			t.Errorf("argv holds the prompt: %q", inv.Argv)
		}
	}
	if inv.Argv[0] != "claude" {
		t.Errorf("argv[0] = %q, want claude", inv.Argv[0])
	}
}

func TestCommandRunsHeadlessWithStreamJSON(t *testing.T) {
	argv := claudecode.ClaudeCode{}.Command(step()).Argv

	if _, ok := flag(argv, "-p"); !ok {
		t.Errorf("no -p in %q", argv)
	}
	if v, _ := flag(argv, "--output-format"); v != "stream-json" {
		t.Errorf("--output-format = %q, want stream-json", v)
	}
	if _, ok := flag(argv, "--verbose"); !ok {
		t.Errorf("stream-json with -p needs --verbose: %q", argv)
	}
	if _, ok := flag(argv, "--no-session-persistence"); !ok {
		t.Errorf("every Step is a fresh session, want --no-session-persistence: %q", argv)
	}
}

func TestCommandPinsTheAFKFlags(t *testing.T) {
	argv := claudecode.ClaudeCode{}.Command(step()).Argv

	if v, _ := flag(argv, "--permission-mode"); v != "bypassPermissions" {
		t.Errorf("--permission-mode = %q, want bypassPermissions", v)
	}
	if v, _ := flag(argv, "--permission-prompts"); v != "none" {
		t.Errorf("--permission-prompts = %q, want none", v)
	}
	if v, _ := flag(argv, "--settings"); v != `{"sandbox":{"enabled":false}}` {
		t.Errorf("--settings = %q, want the built-in sandbox off", v)
	}
	if v, _ := flag(argv, "--json-schema"); v != harness.ReportSchema {
		t.Errorf("--json-schema = %q, want the report schema", v)
	}
	if _, ok := flag(argv, "--bare"); ok {
		t.Errorf("never --bare: %q", argv)
	}
}

func TestCommandPassesTheModelAsWritten(t *testing.T) {
	s := step()
	s.Model = "us.anthropic.claude-sonnet-5-5-v1:0"
	argv := claudecode.ClaudeCode{}.Command(s).Argv
	if v, _ := flag(argv, "--model"); v != "us.anthropic.claude-sonnet-5-5-v1:0" {
		t.Errorf("--model = %q", v)
	}

	s.Model = ""
	if _, ok := flag(claudecode.ClaudeCode{}.Command(s).Argv, "--model"); ok {
		t.Errorf("--model set without a model")
	}
}

func TestCommandSetsEffortOnlyWhenSet(t *testing.T) {
	s := step()
	if _, ok := flag(claudecode.ClaudeCode{}.Command(s).Argv, "--effort"); ok {
		t.Errorf("--effort set without an effort")
	}

	s.Effort = "high"
	if v, _ := flag(claudecode.ClaudeCode{}.Command(s).Argv, "--effort"); v != "high" {
		t.Errorf("--effort = %q, want high", v)
	}
}

func TestCommandEnv(t *testing.T) {
	s := step()
	env := claudecode.ClaudeCode{}.Command(s).Env
	want := map[string]string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_SUBPROCESS_ENV_SCRUB":         "1",
	}
	if !maps.Equal(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}

	s.ScrubCredentials = false
	env = claudecode.ClaudeCode{}.Command(s).Env
	want = map[string]string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
	if !maps.Equal(env, want) {
		t.Errorf("scrub off: env = %v, want %v", env, want)
	}
}

func TestCommandPointsClaudeCodeAtTheCredentialFile(t *testing.T) {
	s := step()
	s.CredentialCommand = `cat "$KRAFTWERK_CREDENTIAL_FILE"`

	got, _ := flag(claudecode.ClaudeCode{}.Command(s).Argv, "--settings")

	var settings map[string]any
	if err := json.Unmarshal([]byte(got), &settings); err != nil {
		t.Fatalf("--settings %q is not JSON: %v", got, err)
	}
	if settings["awsCredentialExport"] != s.CredentialCommand {
		t.Errorf("awsCredentialExport = %v, want %q", settings["awsCredentialExport"], s.CredentialCommand)
	}
	if sandbox, _ := settings["sandbox"].(map[string]any); sandbox["enabled"] != false {
		t.Errorf("settings = %v, want Claude Code's own sandbox still off", settings)
	}
}

func TestCommandSetsNoCredentialExportWithoutAFile(t *testing.T) {
	got, _ := flag(claudecode.ClaudeCode{}.Command(step()).Argv, "--settings")

	if strings.Contains(got, "awsCredentialExport") {
		t.Errorf("--settings = %q, want no awsCredentialExport", got)
	}
}
