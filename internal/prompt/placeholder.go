package prompt

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

// Names of the Steps that have a prompt.
const (
	implementStep = "implement"
	reviewStep    = "review"
	mergeStep     = "merge"
)

// Steps are the Steps with a prompt, in the order a Ticket meets them.
var Steps = []string{implementStep, reviewStep, mergeStep}

// The placeholders of the guidance. Each becomes a whole section, or
// nothing.
const (
	commandsPlaceholder  = "{{commands}}"
	standardsPlaceholder = "{{standards}}"
)

// commandsIntro says, per Step, when to run the Feedback commands.
var commandsIntro = map[string]string{
	implementStep: "Run these before every commit and make them pass:",
	reviewStep:    "After your changes, run these and make them pass:",
	mergeStep:     "After resolving, run these and make them pass:",
}

// fill replaces the placeholders in step's guidance. A placeholder that
// renders nothing and stands on its own line takes its line, and one
// blank line after it, with it.
func fill(step string, c Context) string {
	part := guidance[step]
	part = replace(part, commandsPlaceholder, commandsSection(step, c.Commands))
	standards := ""
	if step == reviewStep {
		standards = standardsSection(c.Standards)
	}
	return replace(part, standardsPlaceholder, standards)
}

// replace puts section in place of every placeholder in part.
func replace(part, placeholder, section string) string {
	if section == "" {
		line := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(placeholder) + `[ \t]*(\r?\n([ \t]*\r?\n)?|$)`)
		part = line.ReplaceAllString(part, "")
	}
	return strings.ReplaceAll(part, placeholder, strings.TrimRight(section, "\n"))
}

// commandsSection is what `{{commands}}` becomes in step's prompt: the
// Feedback commands under a heading, or nothing if there are none.
func commandsSection(step string, cmds []verify.Command) string {
	if len(cmds) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Feedback commands\n\n%s\n\n", commandsIntro[step])
	for _, c := range cmds {
		fmt.Fprintf(&b, "- %s: `%s`\n", c.Name, c.Line)
	}
	return b.String()
}

// standardsSection is what `{{standards}}` becomes in review's prompt:
// the Coding standards file to follow, or nothing if there is none.
func standardsSection(path string) string {
	if path == "" {
		return ""
	}
	return fmt.Sprintf("Follow the coding standards in `%s`. Read that file first. Where they disagree with the smells below, the standards win.\n", path)
}
