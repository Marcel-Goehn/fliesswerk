package github

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ghLabel is a label as gh prints it.
type ghLabel struct {
	Name string `json:"name"`
}

// MissingLabels reads the repository's labels in one gh call and returns
// those of names it lacks. GitHub compares label names without regard to
// case, and so does this.
func (t *Tracker) MissingLabels(ctx context.Context, names []string) ([]string, error) {
	args := t.args("label", "list", "")
	args = append(args, "--limit", strconv.Itoa(listLimit), "--json", "name")
	var labels []ghLabel
	if err := t.runJSON(ctx, &labels, args...); err != nil {
		return nil, fmt.Errorf("reading the repository's labels: %w", err)
	}
	if len(labels) >= listLimit {
		return nil, fmt.Errorf("gh returned %d labels, its limit; this fliesswerk can't read more", len(labels))
	}
	var missing []string
	for _, name := range names {
		if !slices.ContainsFunc(labels, func(l ghLabel) bool { return strings.EqualFold(l.Name, name) }) {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// CreateLabel creates a label with a description, and gh's choice of
// color. It never changes a label that already exists.
func (t *Tracker) CreateLabel(ctx context.Context, name, description string) error {
	if _, err := t.opts.Runner.Run(ctx, nil, t.createLabelArgs(name, description)...); err != nil {
		return fmt.Errorf("creating the label %s: %w", name, err)
	}
	return nil
}

func (t *Tracker) createLabelArgs(name, description string) []string {
	args := []string{"label", "create", name}
	if t.opts.Repo != "" {
		args = append(args, "--repo", t.opts.Repo)
	}
	return append(args, "--description", description)
}

// createLabelFix is the gh line that creates the label, for a person to run.
func (t *Tracker) createLabelFix(name, description string) string {
	args := t.createLabelArgs(name, description)
	for i, a := range args {
		args[i] = shellWord(a)
	}
	return "gh " + strings.Join(args, " ")
}

// shellWord is s as one word on a command line: as it is when it needs no
// quotes, else in double quotes.
func shellWord(s string) string {
	if s != "" && strings.IndexFunc(s, needsQuotes) < 0 {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

func needsQuotes(r rune) bool {
	plain := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:", r)
	return !plain
}
