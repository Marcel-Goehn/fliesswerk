package runlog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
)

// schemaVersion is summary.json's "schema". Releases only add fields to it.
const schemaVersion = 1

// Summary is how a Run ended. It is shown as text, leaving out what is
// empty, and saved as JSON, where every list is present even when empty.
type Summary struct {
	RunID           string         `json:"run_id"`
	StartedAt       time.Time      `json:"started_at"`
	DurationSeconds float64        `json:"duration_seconds"`
	Iterations      int            `json:"iterations"`
	ExitCode        int            `json:"exit_code"`
	ExitMeaning     string         `json:"exit_meaning"`
	Closed          []ClosedTicket `json:"closed"`
	Failed          []FailedTicket `json:"failed"`
	Stopped         Stopped        `json:"stopped"`
	Skipped         Skipped        `json:"skipped"`
	// StillReady is how many Tickets were still ready when the Run reached
	// max_iterations.
	StillReady    int            `json:"still_ready"`
	SpecsFinished []FinishedSpec `json:"specs_finished"`
	// Memory is nil when nothing measured it.
	Memory       *Memory  `json:"memory"`
	QueuedWrites []string `json:"queued_writes"`
	// Tickets are the tokens and cost of each Ticket that ran a Step, and
	// Usage those of the whole Run.
	Tickets []TicketUsage `json:"tickets"`
	Usage   Usage         `json:"usage"`
	LogDir  string        `json:"log_dir"`
}

// ClosedTicket is a Ticket the Run closed.
type ClosedTicket struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Spec is empty for a standalone Ticket.
	Spec                 string `json:"spec"`
	Commits              int    `json:"commits"`
	LargestContextWindow int64  `json:"largest_context_window"`
	CostDisplay          string `json:"cost_display"`
}

// FailedTicket is a Ticket that didn't make it.
type FailedTicket struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Reason               string `json:"reason"`
	Kind                 string `json:"kind"`
	LargestContextWindow int64  `json:"largest_context_window"`
}

// Stopped is what ended the Run or part of it early.
type Stopped struct {
	// Reason is why the whole Run stopped, empty if it didn't.
	Reason string        `json:"reason"`
	Specs  []StoppedSpec `json:"specs"`
}

// StoppedSpec is a Spec halted by a rejected push.
type StoppedSpec struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Fix    string `json:"fix"`
}

// Skipped are the things the Run left alone that a person may act on.
type Skipped struct {
	TextOnlyBlockers []TextOnlyBlocker `json:"text_only_blockers"`
	ClaimedByOthers  []ClaimedSpec     `json:"claimed_by_others"`
	OutsideBlockers  []string          `json:"outside_blockers"`
}

// TextOnlyBlocker is a Ticket that names a Blocker with no native link.
type TextOnlyBlocker struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Reason string   `json:"reason"`
	Fix    []string `json:"fix"`
}

// ClaimedSpec is a Spec someone else claimed.
type ClaimedSpec struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	By    string `json:"by"`
	// At is the zero time when the date isn't known.
	At time.Time `json:"at"`
}

// FinishedSpec is a Spec whose last Ticket the Run closed.
type FinishedSpec struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
}

// Memory is what the Run's memory admission saw.
type Memory struct {
	PeakEstimateMB int64 `json:"peak_estimate_mb"`
	Waits          int   `json:"waits"`
}

// TicketUsage is the tokens and cost of one Ticket.
type TicketUsage struct {
	ID    string `json:"id"`
	Usage Usage  `json:"usage"`
}

// Usage is tokens and cost. Cost is per unit, never converted.
type Usage struct {
	Input            int64              `json:"input"`
	Output           int64              `json:"output"`
	CacheRead        int64              `json:"cache_read"`
	CacheWrite       int64              `json:"cache_write"`
	Cost             map[string]float64 `json:"cost"`
	StepsWithoutCost int                `json:"steps_without_cost"`
	CostDisplay      string             `json:"cost_display"`
}

// UsageOf is the summary's form of t.
func UsageOf(t harness.Totals) Usage {
	cost := map[string]float64{}
	for unit, v := range t.Costs {
		cost[string(unit)] = v
	}
	return Usage{
		Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite,
		Cost: cost, StepsWithoutCost: t.NoCost, CostDisplay: t.CostString(),
	}
}

func (u Usage) tokens() string { return tokens(u.Input+u.CacheRead+u.CacheWrite, u.Output) }

func tokens(in, out int64) string { return fmt.Sprintf("%d in, %d out", in, out) }

// StepEnd is the text of a Step's end line.
func StepEnd(d time.Duration, u harness.Usage, contextWindow int64) string {
	var cost harness.Totals
	cost.Add(u)
	return fmt.Sprintf("finished in %s, tokens %s, context %d, %s",
		d.Round(time.Second), tokens(u.Input+u.CacheRead+u.CacheWrite, u.Output), contextWindow, cost.CostString())
}

// Text is the summary for a person, its sections in the Spec's order.
func (s Summary) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nRun finished in %s after %d %s. Exit code %d: %s.\n",
		(time.Duration(s.DurationSeconds * float64(time.Second))).Round(time.Second), s.Iterations, plural(s.Iterations, "Iteration", "Iterations"), s.ExitCode, s.ExitMeaning)
	s.writeClosed(&b)
	s.writeFailed(&b)
	s.writeStopped(&b)
	s.writeSkipped(&b)
	if s.StillReady > 0 {
		fmt.Fprintf(&b, "\nStill ready at the limit: %d\n", s.StillReady)
	}
	section(&b, "Specs finished", s.SpecsFinished, func(f FinishedSpec) string {
		return fmt.Sprintf("  %s  %s  (%s)", f.ID, f.Title, f.Branch)
	})
	if s.Memory != nil {
		fmt.Fprintf(&b, "\nMemory: peak estimate %d MB, %d %s\n", s.Memory.PeakEstimateMB, s.Memory.Waits, plural(s.Memory.Waits, "wait", "waits"))
	}
	section(&b, "Writes queued for replay", s.QueuedWrites, func(w string) string { return "  " + w })
	s.writeTokens(&b)
	fmt.Fprintf(&b, "\nLogs: %s\n", s.LogDir)
	return b.String()
}

// writeTokens writes the tokens and cost, if any Step ran.
func (s Summary) writeTokens(b *strings.Builder) {
	if len(s.Tickets) == 0 {
		return
	}
	b.WriteString("\nTokens and cost:\n")
	for _, t := range s.Tickets {
		fmt.Fprintf(b, "  %s  %s  %s\n", t.ID, t.Usage.tokens(), t.Usage.CostDisplay)
	}
	fmt.Fprintf(b, "  Run  %s  %s\n", s.Usage.tokens(), s.Usage.CostDisplay)
}

func (s Summary) writeClosed(b *strings.Builder) {
	section(b, "Closed", s.Closed, func(c ClosedTicket) string {
		where := "standalone"
		if c.Spec != "" {
			where = "Spec " + c.Spec
		}
		return fmt.Sprintf("  %s  %s  (%s)  %d %s  context %d  %s", c.ID, c.Title, where, c.Commits, plural(c.Commits, "commit", "commits"), c.LargestContextWindow, c.CostDisplay)
	})
}

func (s Summary) writeFailed(b *strings.Builder) {
	section(b, "Failed", s.Failed, func(f FailedTicket) string {
		return fmt.Sprintf("  %s  %s: %s  [%s]  context %d", f.ID, f.Title, f.Reason, f.Kind, f.LargestContextWindow)
	})
}

func (s Summary) writeStopped(b *strings.Builder) {
	if s.Stopped.Reason == "" && len(s.Stopped.Specs) == 0 {
		return
	}
	b.WriteString("\nStopped:\n")
	if s.Stopped.Reason != "" {
		fmt.Fprintf(b, "  %s\n", s.Stopped.Reason)
	}
	for _, sp := range s.Stopped.Specs {
		fmt.Fprintf(b, "  %s  %s\n       fix: %s\n", sp.ID, sp.Branch, sp.Fix)
	}
}

func (s Summary) writeSkipped(b *strings.Builder) {
	sk := s.Skipped
	if len(sk.TextOnlyBlockers)+len(sk.ClaimedByOthers)+len(sk.OutsideBlockers) == 0 {
		return
	}
	b.WriteString("\nSkipped:\n")
	for _, t := range sk.TextOnlyBlockers {
		fmt.Fprintf(b, "  %s  %s: %s\n", t.ID, t.Title, t.Reason)
		for _, fix := range t.Fix {
			fmt.Fprintf(b, "       fix: %s\n", fix)
		}
	}
	for _, c := range sk.ClaimedByOthers {
		fmt.Fprintf(b, "  %s  %s: claimed by %s", c.ID, c.Title, c.By)
		if !c.At.IsZero() {
			fmt.Fprintf(b, " on %s", c.At.Format(time.DateOnly))
		}
		b.WriteString("\n")
	}
	for _, w := range sk.OutsideBlockers {
		fmt.Fprintf(b, "  %s\n", w)
	}
}

// section writes a titled list, or nothing if it is empty.
func section[T any](b *strings.Builder, title string, items []T, line func(T) string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, it := range items {
		b.WriteString(line(it) + "\n")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// json is the summary as summary.json holds it.
func (s Summary) json() []byte {
	s = s.withLists()
	data, err := json.MarshalIndent(struct {
		Schema int `json:"schema"`
		Summary
	}{schemaVersion, s}, "", "  ")
	if err != nil {
		return []byte("{\"schema\":1}\n")
	}
	return append(data, '\n')
}

// withLists is s with every missing list empty, so a script can iterate
// over any of them.
func (s Summary) withLists() Summary {
	s.Closed = orEmpty(s.Closed)
	s.Failed = orEmpty(s.Failed)
	s.Stopped.Specs = orEmpty(s.Stopped.Specs)
	s.Skipped.TextOnlyBlockers = orEmpty(s.Skipped.TextOnlyBlockers)
	s.Skipped.ClaimedByOthers = orEmpty(s.Skipped.ClaimedByOthers)
	s.Skipped.OutsideBlockers = orEmpty(s.Skipped.OutsideBlockers)
	s.SpecsFinished = orEmpty(s.SpecsFinished)
	s.QueuedWrites = orEmpty(s.QueuedWrites)
	s.Tickets = orEmpty(s.Tickets)
	if s.Usage.Cost == nil {
		s.Usage.Cost = map[string]float64{}
	}
	return s
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
