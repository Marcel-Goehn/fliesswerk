// Package harness is the boundary between the Factory's core and the
// Harness that carries out a Step (Claude Code first). Adapters are
// compiled in and implement Harness. They do no I/O: the core does all
// spawning, timeouts, tree-kill, redaction and logging, and only sees the
// types here.
package harness

// Harness is the boundary every Harness adapter implements.
type Harness interface {
	// Command says how to start one Step: argv, env and stdin as data.
	// Nothing runs yet. The prompt goes on stdin, never in argv.
	Command(step Step) Invocation
	// Parse turns one line of the Harness's output into common events.
	// A line it doesn't recognise yields none.
	Parse(line []byte) []Event
	// Describe tells the rest of the Factory what this Harness needs.
	Describe() Description
}

// Step is what the core hands an adapter to start one Step.
type Step struct {
	// Prompt is the whole prompt, delivered on stdin.
	Prompt string
	// Model is passed to the Harness exactly as written; empty leaves the
	// Harness's default.
	Model string
	// Effort is the effort level; empty leaves the flag out.
	Effort string
	// ReportSchema is the JSON Schema of the report, owned by the core.
	ReportSchema string
	// ScrubCredentials keeps credentials out of the env of the commands
	// the agent runs. On unless `scrub_credentials = false`.
	ScrubCredentials bool
}

// Invocation is a Step's process as data.
type Invocation struct {
	// Argv is the command line, the binary first. It never holds the
	// prompt.
	Argv []string
	// Env is what the adapter adds to the Step's env. Credentials are the
	// core's to add.
	Env map[string]string
	// Stdin is written to the process's standard input, then closed.
	Stdin string
}
