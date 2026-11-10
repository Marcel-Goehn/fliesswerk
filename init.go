package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
	"github.com/Marcel-Goehn/fliesswerk/internal/credentials"
	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/setup"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/github"
)

// runInit sets up the project around the current directory: it looks at the
// repo, asks the team's questions, writes .fliesswerk/, creates the labels
// the Tracker lacks and ends with a full `check`. Nothing is written until
// the last question has been answered.
func runInit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "fliesswerk init: unexpected argument %q; init takes none, and only ever creates what's missing\n", args[0])
		return exitConfigError
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "fliesswerk init: %v\n", err)
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
	store, err := credentials.StorePath()
	if err != nil {
		return fail(err)
	}
	hasConfig := exists(filepath.Join(root, filepath.FromSlash(config.ProjectFile)))
	hasLocal := exists(filepath.Join(root, filepath.FromSlash(config.LocalFile)))
	facts, err := setup.Detect(ctx, root)
	if err != nil {
		return fail(err)
	}
	desc := theHarness.Describe()
	h := setup.Harness{Name: desc.Name, Install: desc.ContainerInstall}
	trackerInfo := github.New(github.Options{Runner: github.ExecRunner{}}).Describe()
	ask := setup.NewAsker(stdin, stdout)

	var files []setup.File
	var unsandboxed bool
	var repo string
	var values map[string]any
	if hasConfig {
		// An unreadable config has no kind, so only .gitignore can come back.
		values = config.Load(root, nil).Values
		files = setup.Recreated(facts, sandboxKindOf(values), h)
		repo = stringAt(values, facts.Repo, "tracker", "repo")
	} else {
		answers, err := setup.Ask(root, ask, facts, trackerInfo.Questions)
		if err != nil {
			return fail(err)
		}
		files, unsandboxed, repo = setup.Files(facts, answers, h), answers.Unsandboxed, answers.Values["tracker.repo"]
	}
	var personal setup.Personal
	if !hasLocal {
		home, _ := os.UserHomeDir() // without one, there's just nothing to import
		personal, err = setup.AskPersonal(ctx, ask, setup.PersonalInput{Harness: desc, Tracker: trackerInfo.Access, Repo: repo, Store: store, Root: root, Home: home,
			FindInterception: findInterception(values, root), TeamCACerts: stringsOf(valueAt(values, "network", "ca_certs"))})
		if err != nil {
			return fail(err)
		}
		if err := personal.Save(); err != nil {
			return fail(err)
		}
	}
	if !hasLocal || unsandboxed {
		files = append(files, setup.LocalFile(personal, unsandboxed))
	}

	written, kept, err := setup.Write(root, files)
	if err != nil {
		return fail(err)
	}
	for _, f := range written {
		_, _ = fmt.Fprintf(stdout, "  + %s\n", f)
	}
	if !hasConfig {
		for _, f := range kept {
			_, _ = fmt.Fprintf(stdout, "  = %s (already there, left as it is)\n", f)
		}
	}
	if err := personal.FillDotenv(ctx, ask, root); err != nil {
		return fail(err)
	}
	labels := createMissingLabels(ctx, root, stdout, stderr)
	if hasConfig && hasLocal {
		if len(written) == 0 && labels == 0 {
			_, _ = fmt.Fprintln(stdout, "Already set up: init never overwrites a file or a label, and nothing is missing.")
		}
		return runCheck(nil, stdout, stderr)
	}

	_, _ = fmt.Fprintln(stdout, "\nChecking the setup, including one try of your commands in a Sandbox (no agent, no tokens):")
	if code := runCheck([]string{"--commands"}, stdout, stderr); code != exitOK {
		_, _ = fmt.Fprintln(stderr, "Not ready yet: fix the lines above, then run `fliesswerk check --commands`.")
		return code
	}
	if hasConfig {
		_, _ = fmt.Fprintln(stdout, "Ready: label a Ticket, run `fliesswerk run`")
		return exitOK
	}
	_, _ = fmt.Fprintln(stdout, "Ready: commit `.fliesswerk/`, label a Ticket, run `fliesswerk run`")
	return exitOK
}

// sandboxKindOf is the Sandbox kind this person runs, "" for a config that
// can't be read.
func sandboxKindOf(values map[string]any) string {
	if values == nil {
		return ""
	}
	return stringAt(values, defaultSandbox, "sandbox", "kind")
}
