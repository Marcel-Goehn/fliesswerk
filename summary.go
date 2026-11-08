package main

import (
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// exitMeaning says what an exit code means, for the summary.
func exitMeaning(code int) string {
	switch code {
	case exitOK:
		return "nothing needs a person"
	case exitFailures:
		return "some Tickets failed or need a person's fix"
	case exitStopped:
		return "the Run was stopped"
	}
	return "the project isn't set up for a Run"
}

// summaryOf is the summary of a Run that ended with res.
func summaryOf(res factory.Result, id string, started time.Time, code int) runlog.Summary {
	stats := map[string]*factory.TicketStats{}
	s := runlog.Summary{
		RunID: id, StartedAt: started, DurationSeconds: time.Since(started).Seconds(),
		Iterations: res.Iterations, ExitCode: code, ExitMeaning: exitMeaning(code),
		StillReady: res.StillReady, Usage: runlog.UsageOf(res.Usage), LogDir: runlog.RelDir(id),
	}
	for _, st := range res.Stats {
		stats[st.Ticket.ID.Key] = st
		s.Tickets = append(s.Tickets, runlog.TicketUsage{ID: st.Ticket.ID.String(), Usage: runlog.UsageOf(st.Usage)})
	}
	for _, tk := range res.Closed {
		c := runlog.ClosedTicket{ID: tk.ID.String(), Title: tk.Title, Spec: specOf(tk)}
		if st := stats[tk.ID.Key]; st != nil {
			c.Commits, c.LargestContextWindow, c.CostDisplay = st.Commits, st.LargestContextWindow, st.Usage.CostString()
		}
		s.Closed = append(s.Closed, c)
	}
	for _, f := range res.Failed {
		c := runlog.FailedTicket{ID: f.Ticket.ID.String(), Title: f.Ticket.Title, Reason: f.Reason, Kind: f.Kind}
		if st := stats[f.Ticket.ID.Key]; st != nil {
			c.LargestContextWindow = st.LargestContextWindow
		}
		s.Failed = append(s.Failed, c)
	}
	if res.Stopped != nil {
		s.Stopped.Reason = res.Stopped.Error()
	}
	for _, sp := range res.StoppedSpecs {
		s.Stopped.Specs = append(s.Stopped.Specs, runlog.StoppedSpec{ID: sp.ID.String(), Branch: sp.Branch, Fix: sp.Fix})
	}
	for _, sk := range res.Skipped {
		s.Skipped.TextOnlyBlockers = append(s.Skipped.TextOnlyBlockers, runlog.TextOnlyBlocker{
			ID: sk.Ticket.ID.String(), Title: sk.Ticket.Title, Reason: sk.Reason, Fix: sk.Fix,
		})
	}
	for _, c := range res.ClaimedByOthers {
		s.Skipped.ClaimedByOthers = append(s.Skipped.ClaimedByOthers, runlog.ClaimedSpec{
			ID: c.ID.String(), Title: c.Title, By: c.Claim.By, At: c.Claim.At,
		})
	}
	s.Skipped.OutsideBlockers = res.Warnings
	for _, f := range res.SpecsFinished {
		s.SpecsFinished = append(s.SpecsFinished, runlog.FinishedSpec{ID: f.ID.String(), Title: f.Title, Branch: f.Branch})
	}
	return s
}

// specOf is the Display of the Ticket's Spec, "" if it stands alone.
func specOf(tk tracker.Ticket) string {
	if tk.Parent == nil {
		return ""
	}
	return tk.Parent.Display
}
