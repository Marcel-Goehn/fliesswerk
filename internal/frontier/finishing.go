package frontier

import (
	"slices"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// Finishing is a Spec, or a standalone Ticket, that a Run would finish.
type Finishing struct {
	// Ticket is the one whose landing would finish it.
	Ticket tracker.Ticket
	// Spec is the Ticket's Spec, nil for a standalone Ticket.
	Spec *tracker.Spec
}

// ID is the Spec's, or the standalone Ticket's.
func (f Finishing) ID() tracker.ID { return f.Ticket.SpecID() }

// Title is the Spec's, or the standalone Ticket's.
func (f Finishing) Title() string {
	if f.Spec != nil {
		return f.Spec.Title
	}
	return f.Ticket.Title
}

// WouldFinish plays a Run over snap, at most iterations Iterations, as if
// every Ticket an Iteration takes landed, and lists the Specs and
// standalone Tickets that would finish, in the order they would. A Spec
// finishes once none of its Tickets is open.
func WouldFinish(snap tracker.Snapshot, opts Options, iterations int) []Finishing {
	landed := map[string]bool{}
	var out []Finishing
	for range iterations {
		p := Compute(afterLanding(snap, landed), opts)
		if len(p.Take) == 0 {
			break
		}
		for _, tk := range p.Take {
			landed[tk.ID.Key] = true
		}
		for _, tk := range p.Take {
			if tk.Parent == nil {
				out = append(out, Finishing{Ticket: tk})
				continue
			}
			spec, ok := snap.Specs[tk.Parent.Key]
			if !ok || !spec.Finished(func(id tracker.ID) bool { return landed[id.Key] }) || tk.ID.Key != lastTaken(tk.Parent.Key, p.Take) {
				continue
			}
			out = append(out, Finishing{Ticket: tk, Spec: &spec})
		}
	}
	return out
}

// afterLanding is snap with the landed Tickets closed.
func afterLanding(snap tracker.Snapshot, landed map[string]bool) tracker.Snapshot {
	state := func(id tracker.ID, s tracker.State) tracker.State {
		if landed[id.Key] {
			return tracker.Closed
		}
		return s
	}
	out := tracker.Snapshot{Specs: map[string]tracker.Spec{}}
	for _, tk := range snap.Tickets {
		if landed[tk.ID.Key] {
			continue
		}
		tk.Blockers = slices.Clone(tk.Blockers)
		for i, b := range tk.Blockers {
			tk.Blockers[i].State = state(b.ID, b.State)
		}
		out.Tickets = append(out.Tickets, tk)
	}
	for key, spec := range snap.Specs {
		spec.Tickets = slices.Clone(spec.Tickets)
		for i, st := range spec.Tickets {
			spec.Tickets[i].State = state(st.ID, st.State)
		}
		out.Specs[key] = spec
	}
	return out
}

// lastTaken is the key of the last of take in the Spec specKey, so a Spec
// whose last Tickets land in one Iteration is listed once.
func lastTaken(specKey string, take []tracker.Ticket) string {
	var last string
	for _, tk := range take {
		if tk.Parent != nil && tk.Parent.Key == specKey {
			last = tk.ID.Key
		}
	}
	return last
}
