// Package claudecode is the Harness adapter for Claude Code. It does no
// I/O: it builds the headless command, parses `stream-json` lines and
// declares what Claude Code needs.
package claudecode

import (
	"encoding/json"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
)

// ClaudeCode is the Claude Code adapter, selected by
// `harness = "claude_code"`.
type ClaudeCode struct{}

var _ harness.Harness = ClaudeCode{}

// Env flags Command sets.
const (
	envNoTraffic = "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"
	envScrub     = "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"
)

// settings are what Claude Code gets with --settings. Its built-in sandbox
// is off, as the Factory's Sandbox isolates the Step, and it reads AWS
// credentials through AWSCredentialExport, if set.
type settings struct {
	Sandbox             sandboxSettings `json:"sandbox"`
	AWSCredentialExport string          `json:"awsCredentialExport,omitempty"`
}

type sandboxSettings struct {
	Enabled bool `json:"enabled"`
}

// settingsJSON is the value of --settings. It holds a command, never a
// credential.
func settingsJSON(credentialCommand string) string {
	data, err := json.Marshal(settings{AWSCredentialExport: credentialCommand})
	if err != nil {
		panic(err) // two plain fields always marshal
	}
	return string(data)
}

// Command builds the headless command. The AFK flags are pinned here, not
// in user config, and it never passes --bare, so an OAuth token,
// CLAUDE.md, skills and settings all work.
func (ClaudeCode) Command(step harness.Step) harness.Invocation {
	argv := []string{
		binary, "-p",
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions",
		"--permission-prompts", "none",
		"--settings", settingsJSON(step.CredentialCommand),
		"--json-schema", step.ReportSchema,
		"--no-session-persistence",
	}
	if step.Model != "" {
		argv = append(argv, "--model", step.Model)
	}
	if step.Effort != "" {
		argv = append(argv, "--effort", step.Effort)
	}
	env := map[string]string{envNoTraffic: "1"}
	if step.ScrubCredentials {
		env[envScrub] = "1"
	}
	return harness.Invocation{Argv: argv, Env: env, Stdin: step.Prompt}
}
