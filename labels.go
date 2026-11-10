package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
	"github.com/Marcel-Goehn/fliesswerk/internal/credentials"
	"github.com/Marcel-Goehn/fliesswerk/internal/factory"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// trackerLabel is a label a Run needs in the Tracker, with the description
// `init` gives it.
type trackerLabel struct {
	name, description string
}

// trackerLabels are the labels a Run adds, by their configured names, each
// once: GitHub compares label names without regard to case.
func trackerLabels(values map[string]any) []trackerLabel {
	all := []trackerLabel{
		{readyLabel(values), "Fliesswerk may work on it"},
		{stringAt(values, defaultInProgressLabel, "tracker", "in_progress_label"), "Claimed by a Fliesswerk Run"},
		{stringAt(values, defaultCannotDoLabel, "tracker", "cannot_do_label"), "Fliesswerk handed it back to a person"},
	}
	var out []trackerLabel
	for _, l := range all {
		if !slices.ContainsFunc(out, func(o trackerLabel) bool { return strings.EqualFold(o.name, l.name) }) {
			out = append(out, l)
		}
	}
	return out
}

// missingLabels are those of labels the Tracker lacks.
func missingLabels(ctx context.Context, tr tracker.Tracker, labels []trackerLabel) ([]trackerLabel, error) {
	missing, err := tr.MissingLabels(ctx, labelNames(labels))
	if err != nil {
		return nil, err
	}
	var out []trackerLabel
	for _, l := range labels {
		if slices.Contains(missing, l.name) {
			out = append(out, l)
		}
	}
	return out, nil
}

func labelNames(labels []trackerLabel) []string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.name
	}
	return names
}

// createLabelLines are the lines that create labels by hand.
func createLabelLines(tr tracker.Tracker, labels []trackerLabel) string {
	fix := tr.Describe().CreateLabelFix
	var b strings.Builder
	for _, l := range labels {
		_, _ = fmt.Fprintf(&b, "\n  %s", fix(l.name, l.description))
	}
	return b.String()
}

// checkLabels proves the Tracker has every label a Run adds. A missing one
// is a *factory.SetupError that names each missing label with both fixes.
func checkLabels(ctx context.Context, tr tracker.Tracker, values map[string]any) error {
	missing, err := missingLabels(ctx, tr, trackerLabels(values))
	if err != nil {
		return fmt.Errorf("labels: %w", err)
	}
	if len(missing) == 0 {
		return nil
	}
	names := labelNames(missing)
	them := pluralize(len(missing), "it", "them")
	return &factory.SetupError{Msg: fmt.Sprintf("labels: the Tracker has no %s %s, and a Run can't add a label that doesn't exist. Run `fliesswerk init` to create %s, or create %s yourself:%s",
		pluralize(len(missing), "label", "labels"), strings.Join(names, ", "), them, them, createLabelLines(tr, missing))}
}

// createMissingLabels creates the labels a Run needs that the Tracker
// lacks, leaving those it has exactly as they are, and says which it
// created. Where it can't, it names the lines that create them by hand;
// `check` then reports them as missing. created is how many it created.
func createMissingLabels(ctx context.Context, root string, stdout, stderr io.Writer) (created int) {
	r := config.Load(root, nil)
	if !r.OK() {
		return 0 // check reports the config
	}
	labels := trackerLabels(r.Values)
	token, tokenErr := initTrackerToken(ctx, root, r.Values)
	tr, err := newTracker(r.Values, token)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: can't reach the Tracker to create its labels: %v\n", err)
		return 0
	}
	byHand := func(err error) int {
		// Without knowing which are missing, every line is one to try.
		_, _ = fmt.Fprintf(stderr, "warning: can't create the Tracker's labels: %v\nCreate those that are missing yourself:%s\n", err, createLabelLines(tr, labels))
		return 0
	}
	if tokenErr != nil {
		return byHand(tokenErr)
	}
	missing, err := missingLabels(ctx, tr, labels)
	if err != nil {
		return byHand(err)
	}
	var failed []trackerLabel
	for _, l := range missing {
		if err := tr.CreateLabel(ctx, l.name, l.description); err != nil {
			_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
			failed = append(failed, l)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "  + label %s\n", l.name)
		created++
	}
	if len(failed) > 0 {
		_, _ = fmt.Fprintf(stderr, "Create the %s yourself:%s\n", pluralize(len(failed), "label", "labels"), createLabelLines(tr, failed))
	}
	return created
}

// initTrackerToken is the Tracker token the config names, empty for the
// Tracker's own login.
func initTrackerToken(ctx context.Context, root string, values map[string]any) (string, error) {
	store, err := credentials.StorePath()
	if err != nil {
		return "", err
	}
	return trackerToken(ctx, credentials.Resolver{Root: root, Store: store, Getenv: os.Getenv}, values)
}
