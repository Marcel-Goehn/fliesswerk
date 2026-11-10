package frontier_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/frontier"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

const ready = "ready-for-agent"

func plan(t *testing.T, maxParallel int, issues ...fake.Issue) frontier.Plan {
	t.Helper()
	return planFor(t, frontier.Options{MaxParallel: maxParallel}, issues...)
}

// planFor plans over issues as the fake Tracker's user "me", with opts
// on top of the ready label and the fix.
func planFor(t *testing.T, opts frontier.Options, issues ...fake.Issue) frontier.Plan {
	t.Helper()
	tr := fake.New(ready, issues...)
	snap, err := tr.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opts.ReadyLabel, opts.LinkBlockerFix, opts.User = ready, tr.Describe().LinkBlockerFix, tr.Me
	return frontier.Compute(snap, opts)
}

const inProgress = "in-progress"

// day is a claim date, the nth of November 2026.
func day(n int) time.Time { return time.Date(2026, 11, n, 9, 0, 0, 0, time.UTC) }

func taken(p frontier.Plan) []string {
	var out []string
	for _, tk := range p.Take {
		out = append(out, tk.ID.Display)
	}
	return out
}

func TestReadyTicketIsTaken(t *testing.T) {
	p := plan(t, 1, fake.Issue{Number: 7, Title: "Do it", Labels: []string{ready}})

	if got := taken(p); !slices.Equal(got, []string{"#7"}) {
		t.Fatalf("taken = %v, want [#7]", got)
	}
}

func skip(t *testing.T, p frontier.Plan, display string) frontier.Skip {
	t.Helper()
	for _, s := range p.Skipped {
		if s.Ticket.ID.Display == display {
			return s
		}
	}
	t.Fatalf("%s not skipped; skipped = %+v", display, p.Skipped)
	return frontier.Skip{}
}

func TestOpenBlockerHoldsTicketBack(t *testing.T) {
	p := plan(t, 5,
		fake.Issue{Number: 3, Title: "First"},
		fake.Issue{Number: 4, Title: "Second", Labels: []string{ready}, BlockedBy: []int{3}},
	)

	if got := taken(p); len(got) != 0 {
		t.Fatalf("taken = %v, want none", got)
	}
	s := skip(t, p, "#4")
	if s.Reason != "waits for #3 (open Blocker)" {
		t.Fatalf("reason = %q", s.Reason)
	}
	if s.NeedsFix {
		t.Fatal("waiting on a Blocker needs no fix")
	}
}

func TestClosedBlockerReleasesTicket(t *testing.T) {
	p := plan(t, 5,
		fake.Issue{Number: 3, Title: "First", Closed: true},
		fake.Issue{Number: 4, Title: "Second", Labels: []string{ready}, BlockedBy: []int{3}},
	)

	if got := taken(p); !slices.Equal(got, []string{"#4"}) {
		t.Fatalf("taken = %v, want [#4]", got)
	}
}

func TestSpecWithReadyLabelIsNeverTaken(t *testing.T) {
	p := plan(t, 5,
		fake.Issue{Number: 1, Title: "Spec", Labels: []string{ready}},
		fake.Issue{Number: 2, Title: "Ticket", Labels: []string{ready}, Parent: 1},
	)

	if got := taken(p); !slices.Equal(got, []string{"#2"}) {
		t.Fatalf("taken = %v, want [#2]", got)
	}
	if s := skip(t, p, "#1"); s.Reason != "is a Spec; its Tickets are worked instead" || s.NeedsFix {
		t.Fatalf("skip = %+v", s)
	}
}

func TestOnlyOpenTicketsWithTheReadyLabelAreTaken(t *testing.T) {
	snap, _ := fake.New(ready, fake.Issue{Number: 1, Labels: []string{ready}}).Snapshot(context.Background())
	closed := snap.Tickets[0]
	closed.State = tracker.Closed
	unlabelled := snap.Tickets[0]
	unlabelled.ID = fake.ID(2)
	unlabelled.Labels = []string{"needs-triage"}
	snap.Tickets = []tracker.Ticket{closed, unlabelled}

	p := frontier.Compute(snap, frontier.Options{ReadyLabel: ready, MaxParallel: 5})

	if len(p.Take) != 0 {
		t.Fatalf("taken = %v, want none", taken(p))
	}
}

func TestTextOnlyBlockerSkipsTicketWithFix(t *testing.T) {
	p := plan(t, 5,
		fake.Issue{Number: 3, Title: "First", Closed: true},
		fake.Issue{Number: 4, Title: "Second", Labels: []string{ready}, TextOnlyBlockers: []int{3}},
	)

	if got := taken(p); len(got) != 0 {
		t.Fatalf("taken = %v, want none", got)
	}
	s := skip(t, p, "#4")
	if !s.NeedsFix {
		t.Fatal("a text-only Blocker needs a fix")
	}
	if s.Reason != "names #3 as a Blocker in its text, with no native blocking link" {
		t.Fatalf("reason = %q", s.Reason)
	}
	if !slices.Equal(s.Fix, []string{"link #3 as a Blocker of #4"}) {
		t.Fatalf("fix = %q", s.Fix)
	}
}

func TestIterationTakesUpToMaxParallelBySpecThenTicketNumber(t *testing.T) {
	issues := []fake.Issue{
		{Number: 20, Title: "Spec B"},
		{Number: 10, Title: "Spec A"},
		{Number: 25, Title: "B2", Labels: []string{ready}, Parent: 20},
		{Number: 21, Title: "B1", Labels: []string{ready}, Parent: 20},
		{Number: 30, Title: "A2", Labels: []string{ready}, Parent: 10},
		{Number: 11, Title: "A1", Labels: []string{ready}, Parent: 10},
		{Number: 15, Title: "Standalone", Labels: []string{ready}},
	}

	all := plan(t, 10, issues...)
	if got := taken(all); !slices.Equal(got, []string{"#11", "#30", "#15", "#21", "#25"}) {
		t.Fatalf("taken = %v, want Spec #10's, then standalone #15, then Spec #20's", got)
	}

	capped := plan(t, 2, issues...)
	if got := taken(capped); !slices.Equal(got, []string{"#11", "#30"}) {
		t.Fatalf("taken = %v, want [#11 #30]", got)
	}
	var later []string
	for _, tk := range capped.Later {
		later = append(later, tk.ID.Display)
	}
	if !slices.Equal(later, []string{"#15", "#21", "#25"}) {
		t.Fatalf("later = %v, want [#15 #21 #25]", later)
	}
}

func TestBlockerOutsideTheSpecIsRespectedAndWarned(t *testing.T) {
	issues := []fake.Issue{
		{Number: 1, Title: "Spec A"},
		{Number: 2, Title: "In A", Parent: 1, Closed: true},
		{Number: 5, Title: "Spec B"},
		{Number: 6, Title: "In B", Parent: 5},
		{Number: 7, Title: "Elsewhere"},
		{Number: 3, Title: "Inside", Labels: []string{ready}, Parent: 1, BlockedBy: []int{2}},
		{Number: 4, Title: "Across", Labels: []string{ready}, Parent: 1, BlockedBy: []int{6}},
		{Number: 8, Title: "Alone", Labels: []string{ready}, BlockedBy: []int{7}},
	}

	p := plan(t, 5, issues...)

	if got := taken(p); !slices.Equal(got, []string{"#3"}) {
		t.Fatalf("taken = %v, want [#3]: open Blockers outside the Spec still hold Tickets back", got)
	}
	want := []string{
		"#4: Blocker #6 is outside its Spec #1; its code won't be on the Spec branch unless it has reached the base branch",
		"#8: Blocker #7 is outside this standalone Ticket; its code won't be on the Ticket's branch unless it has reached the base branch",
	}
	if !slices.Equal(p.Warnings, want) {
		t.Fatalf("warnings = %q\nwant       %q", p.Warnings, want)
	}
}

func TestTheUsersClaimedSpecsComeFirstInClaimOrderThenSpecsByNumber(t *testing.T) {
	issues := []fake.Issue{
		{Number: 10, Title: "Spec unclaimed"},
		{Number: 20, Title: "Spec claimed second", Labels: []string{inProgress}, ClaimedBy: "me", ClaimedAt: day(2)},
		{Number: 30, Title: "Spec claimed first", Labels: []string{inProgress}, ClaimedBy: "me", ClaimedAt: day(1)},
		{Number: 5, Title: "Standalone", Labels: []string{ready}},
		{Number: 11, Title: "A", Labels: []string{ready}, Parent: 10},
		{Number: 22, Title: "B2", Labels: []string{ready}, Parent: 20},
		{Number: 21, Title: "B1", Labels: []string{ready}, Parent: 20},
		{Number: 31, Title: "C", Labels: []string{ready}, Parent: 30},
		{Number: 40, Title: "Standalone, claimed", Labels: []string{ready, inProgress}, ClaimedBy: "me", ClaimedAt: day(3)},
	}

	p := planFor(t, frontier.Options{MaxParallel: 10}, issues...)

	if got, want := taken(p), []string{"#31", "#21", "#22", "#40", "#5", "#11"}; !slices.Equal(got, want) {
		t.Fatalf("taken = %v, want %v", got, want)
	}
}

func TestSpecsClaimedByOthersAreSkippedAndListedWithClaimerAndDate(t *testing.T) {
	issues := []fake.Issue{
		{Number: 10, Title: "Alice's Spec", Labels: []string{inProgress}, Assignees: []string{"alice"}, ClaimedAt: day(4)},
		{Number: 11, Title: "In it", Labels: []string{ready}, Parent: 10},
		{Number: 12, Title: "Also in it", Labels: []string{ready}, Parent: 10},
		{Number: 5, Title: "Bob's Ticket", Labels: []string{ready, inProgress}, ClaimedBy: "bob", ClaimedAt: day(2)},
		{Number: 6, Title: "Free", Labels: []string{ready}},
	}

	p := planFor(t, frontier.Options{MaxParallel: 10}, issues...)

	if got := taken(p); !slices.Equal(got, []string{"#6"}) {
		t.Fatalf("taken = %v, want [#6]", got)
	}
	want := []tracker.Claimed{
		{ID: fake.ID(5), Title: "Bob's Ticket", Claim: tracker.Claim{By: "bob", At: day(2)}},
		{ID: fake.ID(10), Title: "Alice's Spec", Claim: tracker.Claim{By: "alice", At: day(4)}},
	}
	if !slices.Equal(p.ClaimedByOthers, want) {
		t.Fatalf("claimed by others = %+v\nwant                %+v", p.ClaimedByOthers, want)
	}
	if len(p.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want none: claimed Specs are listed once, not per Ticket", p.Skipped)
	}
}

func TestOnlyLimitsThePlanToOneSpecOrStandaloneTicket(t *testing.T) {
	issues := []fake.Issue{
		{Number: 10, Title: "Spec"},
		{Number: 11, Title: "In it", Labels: []string{ready}, Parent: 10},
		{Number: 5, Title: "Standalone", Labels: []string{ready}},
		{Number: 20, Title: "Other Spec", Labels: []string{inProgress}, ClaimedBy: "alice"},
		{Number: 21, Title: "In other", Labels: []string{ready}, Parent: 20},
	}

	for _, tc := range []struct {
		only string
		want []string
	}{
		{"10", []string{"#11"}},
		{"#5", []string{"#5"}},
		{"11", nil}, // a Ticket of a Spec is not a scope
	} {
		p := planFor(t, frontier.Options{MaxParallel: 10, Only: tc.only}, issues...)
		if got := taken(p); !slices.Equal(got, tc.want) {
			t.Errorf("only %s: taken = %v, want %v", tc.only, got, tc.want)
		}
		if len(p.ClaimedByOthers) != 0 {
			t.Errorf("only %s: claimed by others = %+v, want none outside the scope", tc.only, p.ClaimedByOthers)
		}
	}
}
