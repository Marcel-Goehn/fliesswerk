package github_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const labelListArgs = "label list --repo " + repo + " --limit 1000 --json name"

func TestMissingLabelsFromRecordedLabelList(t *testing.T) {
	r := &fixtureRunner{t: t, responses: map[string]string{labelListArgs: "label-list.json"}}

	for name, tt := range map[string]struct {
		names, want []string
	}{
		"all present":                 {[]string{"ready-for-agent", "bug"}, nil},
		"some missing":                {[]string{"ready-for-agent", "needs-investigation", "agent-blocked"}, []string{"needs-investigation", "agent-blocked"}},
		"a name that differs in case": {[]string{"in-progress", "Ready-For-Agent"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := newTracker(r).MissingLabels(context.Background(), tt.names)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("missing = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateLabelCreatesItWithItsDescription(t *testing.T) {
	r := &fixtureRunner{t: t}

	if err := newTracker(r).CreateLabel(context.Background(), "in-progress", "Claimed by a Fliesswerk Run"); err != nil {
		t.Fatal(err)
	}

	want := []call{{args: "label create in-progress --repo " + repo + " --description Claimed by a Fliesswerk Run"}}
	if !slices.Equal(r.calls, want) {
		t.Errorf("calls = %q, want %q", r.calls, want)
	}
}

func TestAFailingLabelCreateNamesTheLabel(t *testing.T) {
	createArgs := "label create in-progress --repo " + repo + " --description Claimed by a Fliesswerk Run"
	r := &fixtureRunner{t: t, errs: map[string]error{createArgs: errors.New("gh label create: HTTP 403: Resource not accessible by personal access token")}}

	err := newTracker(r).CreateLabel(context.Background(), "in-progress", "Claimed by a Fliesswerk Run")

	if err == nil || !strings.Contains(err.Error(), "in-progress") || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("err = %v, want one naming the label and gh's reason", err)
	}
}

func TestTheCreateLabelFixIsTheGhLineToRun(t *testing.T) {
	fix := newTracker(&fixtureRunner{t: t}).Describe().CreateLabelFix

	got := fix("needs-investigation", "Fliesswerk handed it back to a person")

	want := `gh label create needs-investigation --repo ` + repo + ` --description "Fliesswerk handed it back to a person"`
	if got != want {
		t.Errorf("fix = %s\nwant  %s", got, want)
	}
}

func TestTheCreateLabelFixQuotesANameWithSpaces(t *testing.T) {
	fix := newTracker(&fixtureRunner{t: t}).Describe().CreateLabelFix

	if got, want := fix("agent ready", "Fliesswerk may work on it"), `gh label create "agent ready" --repo `+repo+` --description "Fliesswerk may work on it"`; got != want {
		t.Errorf("fix = %s\nwant  %s", got, want)
	}
}
