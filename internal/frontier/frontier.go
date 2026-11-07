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

// Compute plans one Iteration over snap. Tickets go in order of their
// Spec, then their own order; a standalone Ticket is a Spec of one.
func Compute(snap tracker.Snapshot, opts Options) Plan {
	var p Plan
	tickets := slices.Clone(snap.Tickets)
	slices.SortStableFunc(tickets, func(a, b tracker.Ticket) int {
		return cmp.Or(cmp.Compare(specOrder(a), specOrder(b)), cmp.Compare(a.ID.Order, b.ID.Order))
	})
	for _, tk := range tickets {
		if tk.State != tracker.Open || !slices.Contains(tk.Labels, opts.ReadyLabel) {
			continue // not something the Snapshot should hold; ignore it
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
	return p
}

// specOrder is the order of the Spec tk belongs to.
func specOrder(tk tracker.Ticket) int64 {
	if tk.Parent != nil {
		return tk.Parent.Order
	}
	return tk.ID.Order
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
