package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/setup"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/github"
)

// runInit sets up the project around the current directory: it looks at the
// repo, asks the team's questions, writes .kraftwerk/ and ends with a full
// `check`. Nothing is written until the last question has been answered.
func runInit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "kraftwerk init: unexpected argument %q; init takes none, and only ever creates what's missing\n", args[0])
		return exitConfigError
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "kraftwerk init: %v\n", err)
		return exitConfigError
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(fmt.Errorf("can't find the current directory: %w", err))
	}
	ctx := context.Background()
	if _, err := (git.Repo{Dir: cwd}).Run(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fail(errors.New("this isn't a git repository; run `git init` first, or run init inside your project's checkout"))
	}
	root := projectRoot(cwd)
	if exists(filepath.Join(root, filepath.FromSlash(config.ProjectFile))) {
		_, _ = fmt.Fprintln(stdout, "Already set up: "+config.ProjectFile+" exists, and init never overwrites it.")
		return runCheck(nil, stdout, stderr)
	}

	facts, err := setup.Detect(ctx, root)
	if err != nil {
		return fail(err)
	}
	questions := github.New(github.Options{}).Describe().Questions
	answers, err := setup.Ask(root, stdin, stdout, facts, questions)
	if err != nil {
		return fail(err)
	}
	desc := theHarness.Describe()
	written, kept, err := setup.Write(root, setup.Files(facts, answers, setup.Harness{Name: desc.Name, Install: desc.ContainerInstall}))
	if err != nil {
		return fail(err)
	}
	for _, f := range written {
		_, _ = fmt.Fprintf(stdout, "  + %s\n", f)
	}
	for _, f := range kept {
		_, _ = fmt.Fprintf(stdout, "  = %s (already there, left as it is)\n", f)
	}

	_, _ = fmt.Fprintln(stdout, "\nChecking the setup, including one try of your commands in a Sandbox (no agent, no tokens):")
	if code := runCheck([]string{"--commands"}, stdout, stderr); code != exitOK {
		_, _ = fmt.Fprintln(stderr, "Not ready yet: fix the lines above, then run `kraftwerk check --commands`.")
		return code
	}
	_, _ = fmt.Fprintln(stdout, "Ready: commit `.kraftwerk/`, label a Ticket, run `kraftwerk run`")
	return exitOK
}
