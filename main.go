// Command kraftwerk is the Factory: it runs AFK agents over a Spec's ready
// Tickets in a loop.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// version is injected at build time with -ldflags "-X main.version=...".
// It stays empty in a dev build.
var version string

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `Usage:
  kraftwerk --version   print the version
`

// Exit codes, as fixed by the Spec (#31). A usage error counts as a config
// failure.
const (
	exitOK          = 0
	exitConfigError = 3
)

// runCLI executes the command line and returns its exit code.
func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(stdout, "kraftwerk %s\n", versionString())
		return exitOK
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
