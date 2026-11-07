// Package frontier decides, in code, which Tickets are ready and which ones
// an Iteration takes. It works on a raw Tracker Snapshot and never parses
// an id.
package frontier

import (
	"cmp"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// Options shape a Plan.
type Options struct {
	ReadyLabel  string
	MaxParallel int
	// LinkBlockerFix names the fix for a text-only Blocker; it comes from
	// the Tracker's Description.
	LinkBlockerFix func(ticket, blocker tracker.ID) string
	// User is the login whose Claims are the user's own; any other
	// claimer's Specs are skipped.
	User string
	// Only, if set, names the one Spec or standalone Ticket to plan for, as
	// a person typed it; every other Ticket is left out.
	Only string
}

// Plan is what one Iteration would do with a Snapshot.
type Plan struct {
	// Take are the Tickets the Iteration works on, in order.
	Take []tracker.Ticket
	// Later are ready Tickets beyond max_parallel, in order.
	Later []tracker.Ticket
	// Skipped are the Tickets that aren't ready, with the reason.
	Skipped []Skip
	// Warnings are things a person should know, e.g. a Blocker outside the
	// Ticket's Spec.
	Warnings []string
	// ClaimedByOthers are the Specs (and standalone Tickets) with ready
	// Tickets that someone else claimed, in Spec order. Their Tickets are
	// in neither Take, Later nor Skipped.
	ClaimedByOthers []tracker.Claimed
}

// Skip is a Ticket that isn't ready, and why.
type Skip struct {
	Ticket tracker.Ticket
	Reason string
	// NeedsFix reports whether a person must act for the Ticket ever to
	// become ready; Fix then says how.
	NeedsFix bool
	Fix      []string
}

// Compute plans one Iteration over snap, finishing what's started: the
// Tickets of the user's claimed Specs go first, in claim order, then those
// of unclaimed Specs by Spec order; within a Spec, by their own order. A
// standalone Ticket is a Spec of one. Specs someone else claimed are
// skipped.
func Compute(snap tracker.Snapshot, opts Options) Plan {
	var p Plan
	var tickets []tracker.Ticket
	for _, tk := range snap.Tickets {
		if opts.Only == "" || tk.SpecID().Names(opts.Only) {
			tickets = append(tickets, tk)
		}
	}
	slices.SortStableFunc(tickets, func(a, b tracker.Ticket) int {
		ca, cb := snap.ClaimOf(a), snap.ClaimOf(b)
		return cmp.Or(
			cmp.Compare(claimedFirst(ca), claimedFirst(cb)),
			compareClaimDates(ca, cb),
			cmp.Compare(a.SpecID().Order, b.SpecID().Order),
			cmp.Compare(a.ID.Order, b.ID.Order),
		)
	})
	others := map[string]bool{}
	for _, tk := range tickets {
		if tk.State != tracker.Open || !slices.Contains(tk.Labels, opts.ReadyLabel) {
			continue // not something the Snapshot should hold; ignore it
		}
		if c := snap.ClaimOf(tk); c != nil && c.By != opts.User && !tk.HasChildren {
			if spec := tk.SpecID(); !others[spec.Key] {
				others[spec.Key] = true
				p.ClaimedByOthers = append(p.ClaimedByOthers, tracker.Claimed{ID: spec, Title: specTitle(tk, snap), Claim: *c})
			}
			continue
		}
		if !tk.HasChildren {
			p.Warnings = append(p.Warnings, outsideBlockers(tk, snap.Specs)...)
		}
		if s, ok := notReady(tk, opts); ok {
			p.Skipped = append(p.Skipped, s)
			continue
		}
		if len(p.Take) < max(opts.MaxParallel, 1) {
			p.Take = append(p.Take, tk)
		} else {
			p.Later = append(p.Later, tk)
		}
	}
	slices.SortFunc(p.ClaimedByOthers, func(a, b tracker.Claimed) int { return cmp.Compare(a.ID.Order, b.ID.Order) })
	return p
}

// specTitle is the title of tk's Spec, or tk's own if it stands alone.
func specTitle(tk tracker.Ticket, snap tracker.Snapshot) string {
	if tk.Parent != nil {
		return snap.Specs[tk.Parent.Key].Title
	}
	return tk.Title
}

// claimedFirst puts claimed Specs before unclaimed ones.
func claimedFirst(c *tracker.Claim) int {
	if c != nil {
		return 0
	}
	return 1
}

// compareClaimDates orders two Claims by date, a Claim of unknown date
// after the dated ones. Unclaimed Specs compare equal.
func compareClaimDates(a, b *tracker.Claim) int {
	if a == nil || b == nil {
		return 0
	}
	return cmp.Or(cmp.Compare(boolInt(a.At.IsZero()), boolInt(b.At.IsZero())), a.At.Compare(b.At))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// notReady reports why tk isn't ready, if it isn't.
func notReady(tk tracker.Ticket, opts Options) (Skip, bool) {
	if tk.HasChildren {
		return Skip{Ticket: tk, Reason: "is a Spec; its Tickets are worked instead"}, true
	}
	if len(tk.TextOnlyBlockers) > 0 {
		s := Skip{Ticket: tk, NeedsFix: true}
		var names []string
		for _, b := range tk.TextOnlyBlockers {
			names = append(names, b.Display)
			if opts.LinkBlockerFix != nil {
				s.Fix = append(s.Fix, opts.LinkBlockerFix(tk.ID, b))
			}
		}
		s.Reason = "names " + strings.Join(names, ", ") + " as " + plural(len(names), "a Blocker", "Blockers") +
			" in its text, with no native blocking link"
		return s, true
	}
	var open []string
	for _, b := range tk.Blockers {
		if b.State == tracker.Open {
			open = append(open, b.ID.Display)
		}
	}
	if len(open) > 0 {
		return Skip{Ticket: tk, Reason: "waits for " + strings.Join(open, ", ") + " (open " + plural(len(open), "Blocker", "Blockers") + ")"}, true
	}
	return Skip{}, false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// outsideBlockers warns about each of tk's Blockers outside its Spec: their
// code won't be on the Spec branch. For a standalone Ticket, every Blocker
// is outside.
func outsideBlockers(tk tracker.Ticket, specs map[string]tracker.Spec) []string {
	inSpec := map[string]bool{}
	if tk.Parent != nil {
		for _, st := range specs[tk.Parent.Key].Tickets {
			inSpec[st.ID.Key] = true
		}
	}
	var out []string
	for _, b := range tk.Blockers {
		if inSpec[b.ID.Key] {
			continue
		}
		if tk.Parent != nil {
			out = append(out, tk.ID.Display+": Blocker "+b.ID.Display+" is outside its Spec "+tk.Parent.Display+
				"; its code won't be on the Spec branch unless it has reached the base branch")
		} else {
			out = append(out, tk.ID.Display+": Blocker "+b.ID.Display+" is outside this standalone Ticket"+
				"; its code won't be on the Ticket's branch unless it has reached the base branch")
		}
	}
	return out
}
