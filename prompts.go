package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
)

// loadPrompts reads the project's prompt files and prints an error for
// every unknown placeholder. ok is false when a file has one or can't be
// read.
func loadPrompts(root string, stderr io.Writer) (parts map[string]prompt.Part, ok bool) {
	parts, err := prompt.Load(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, false
	}
	ok = true
	for _, step := range prompt.Steps {
		p := parts[step]
		for _, u := range prompt.Unknown(p.Text) {
			_, _ = fmt.Fprintf(stderr, "error: %s: unknown placeholder %s; only {{commands}} and {{standards}} exist\n", p.Path, u)
			ok = false
		}
	}
	return parts, ok
}

// warnAboutPrompts prints a warning for every prompt file whose built-in
// has improved since the version it is based on, and for every one that
// doesn't say which version that is.
func warnAboutPrompts(parts map[string]prompt.Part, stderr io.Writer) {
	for _, step := range prompt.Steps {
		p := parts[step]
		switch {
		case p.Improved():
			_, _ = fmt.Fprintf(stderr, "warning: %s: the %s has improved since v%d, which this file is based on; see `kraftwerk prompts diff %s`\n",
				p.Path, builtinName(step, prompt.Latest(step)), p.Based, step)
		case p.Exists && p.Based == 0:
			_, _ = fmt.Fprintf(stderr, "warning: %s: no marker line names the built-in it is based on, so check can't tell when that improves; see `kraftwerk prompts diff %s`\n",
				p.Path, step)
		}
	}
}

// builtinName names version v of step's built-in part.
func builtinName(step string, v int) string {
	return fmt.Sprintf("built-in %s prompt v%d", step, v)
}

// runPrompts runs `kraftwerk prompts diff <step>`: how the built-in part
// changed since the version the project's file is based on.
func runPrompts(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "diff" {
		_, _ = fmt.Fprintln(stderr, "Usage: kraftwerk prompts diff <step>")
		return exitConfigError
	}
	step := args[1]
	if !slices.Contains(prompt.Steps, step) {
		_, _ = fmt.Fprintf(stderr, "kraftwerk prompts diff: unknown Step %q; it is one of %s\n", step, strings.Join(prompt.Steps, ", "))
		return exitConfigError
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk prompts diff: can't find the current directory: %v\n", err)
		return exitConfigError
	}
	parts, err := prompt.Load(projectRoot(cwd))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk prompts diff: %v\n", err)
		return exitConfigError
	}
	p, latest := parts[step], prompt.Latest(step)
	builtin := builtinName(step, latest)
	switch {
	case !p.Exists:
		_, _ = fmt.Fprintf(stdout, "There is no %s, so the built-in %s prompt (v%d) is used as it is.\n", p.Path, step, latest)
	case p.Based == 0:
		_, _ = fmt.Fprintf(stdout, "%s has no marker line naming the built-in it is based on, so here it is against the %s:\n\n%s",
			p.Path, builtin, prompt.UnifiedDiff(p.Path, p.Text, builtin, prompt.Builtin(step, latest)))
		_, _ = fmt.Fprintf(stdout, "\nMake this its first line, so `kraftwerk check` tells you when the built-in improves:\n\n%s\n", prompt.Marker(step))
	case p.Based >= latest:
		_, _ = fmt.Fprintf(stdout, "%s is based on the latest built-in %s prompt (v%d).\n", p.Path, step, latest)
	default:
		_, _ = fmt.Fprintf(stdout, "The built-in %s prompt changed like this since v%d, which %s is based on:\n\n%s", step, p.Based, p.Path,
			prompt.UnifiedDiff(builtinName(step, p.Based), prompt.Builtin(step, p.Based), builtin, prompt.Builtin(step, latest)))
		_, _ = fmt.Fprintf(stdout, "\nAdopt what you want into %s, then make its first line:\n\n%s\n", p.Path, prompt.Marker(step))
	}
	return exitOK
}
