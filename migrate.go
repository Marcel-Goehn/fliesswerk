package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
)

// runMigrate is `kraftwerk migrate`: it rewrites the project's deprecated
// keys and reports what changed.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "kraftwerk migrate: unexpected argument %q\n", args[0])
		return exitConfigError
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk migrate: can't find the current directory: %v\n", err)
		return exitConfigError
	}
	migrations, findings := config.Migrate(projectRoot(cwd))
	changed := false
	for _, m := range migrations {
		if len(m.Changes) == 0 {
			_, _ = fmt.Fprintf(stdout, "kraftwerk migrate: %s: nothing to change\n", m.File)
		}
		for _, c := range m.Changes {
			changed = true
			_, _ = fmt.Fprintf(stdout, "kraftwerk migrate: %s: %s\n", m.File, c)
		}
	}
	if len(findings) > 0 {
		for _, d := range findings {
			_, _ = fmt.Fprintln(stderr, d.String())
		}
		if changed {
			_, _ = fmt.Fprintln(stderr, "kraftwerk migrate: stopped partway, after the changes reported above; fix the errors above and run it again")
		} else {
			_, _ = fmt.Fprintln(stderr, "kraftwerk migrate: nothing was changed; fix the errors above")
		}
		return exitConfigError
	}
	return exitOK
}
