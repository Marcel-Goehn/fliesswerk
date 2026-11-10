// Command kraftwerk is the Factory: it runs AFK agents over a Spec's ready
// Tickets in a loop.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox/native"
)

// version is injected at build time with -ldflags "-X main.version=...".
// It stays empty in a dev build.
var version string

func main() {
	if code, ok := native.RunHelper(os.Args[1:]); ok {
		os.Exit(code)
	}
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `Usage:
  kraftwerk init                  set this project up, mostly by confirming what the repo suggests
  kraftwerk check [--commands]    find where a setup is stuck, before any token is spent;
                                  --commands also runs the project's commands on base_branch
  kraftwerk run [<N>]             implement the ready Tickets, Iteration by Iteration (of only Spec or standalone Ticket N)
  kraftwerk run --dry-run [<N>]   show what Iteration 1 would take, and what is skipped and why
  kraftwerk run --verbose [<N>]   show the agent's text as well
  kraftwerk run --json [<N>]      print the Run's summary as JSON to stdout, and the live lines to stderr
  kraftwerk logs                  print the latest Run's log directory
  kraftwerk release <N>           drop the claim on Spec or standalone Ticket N, so someone else's Run may take it
  kraftwerk prompts diff <step>   show how the built-in prompt of a Step changed since your file's version
  kraftwerk auth set <key>        store a secret, read from a hidden prompt, in your user-level credentials file
  kraftwerk migrate               rewrite renamed or removed config keys, and report what changed
  kraftwerk update                install the newest release of this major version
  kraftwerk update --major        move to the newest major version; run kraftwerk migrate after it
  kraftwerk --version             print the version
`

// Exit codes, as fixed by the Spec (#31). A usage error counts as a config
// failure.
const (
	exitOK          = 0
	exitFailures    = 1 // failures, cannot_do or skipped Tickets
	exitStopped     = 2 // the Run was stopped, e.g. the Snapshot failed
	exitConfigError = 3
)

// runCLI executes the command line and returns its exit code.
func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(stdout, "kraftwerk %s\n", versionString())
		return exitOK
	}
	if len(args) > 0 && args[0] == "init" {
		return runInit(args[1:], os.Stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "check" {
		return runCheck(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "run" {
		return runRun(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "logs" {
		return runLogs(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "release" {
		return runRelease(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "prompts" {
		return runPrompts(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "auth" {
		return runAuth(args[1:], os.Stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "migrate" {
		return runMigrate(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "update" {
		return runUpdate(args[1:], stdout, stderr)
	}
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "kraftwerk: unknown argument %q\n", args[0])
	}
	_, _ = fmt.Fprint(stderr, usage)
	return exitConfigError
}

// versionString is the injected version, or "dev" for a build that had none
// injected. A dev build appends the module version Go stamped into it (from
// `go install ...@v1.2.3` or the VCS state of a local checkout), if any.
func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return "dev (" + info.Main.Version + ")"
	}
	return "dev"
}
