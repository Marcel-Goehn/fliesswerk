package runlog_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/runlog"
)

func TestTheSummaryListsEverySectionInTheSpecsOrder(t *testing.T) {
	s := runlog.Summary{
		RunID: "r", DurationSeconds: 192, Iterations: 2, ExitCode: 1, ExitMeaning: "failures",
		Closed: []runlog.ClosedTicket{{ID: "#2", Title: "Add cart", Spec: "#1", Commits: 2, LargestContextWindow: 4500, CostDisplay: "$0.50"}},
		Failed: []runlog.FailedTicket{{ID: "#3", Title: "Add tax", Reason: "no report", Kind: "no_report", LargestContextWindow: 900}},
		Stopped: runlog.Stopped{
			Reason: "the Tracker went away",
			Specs:  []runlog.StoppedSpec{{ID: "#7", Branch: "kw/spec-7", Fix: "update it"}},
		},
		Skipped: runlog.Skipped{
			TextOnlyBlockers: []runlog.TextOnlyBlocker{{ID: "#4", Title: "Add VAT", Reason: "names #9 as a Blocker", Fix: []string{"link it"}}},
			ClaimedByOthers:  []runlog.ClaimedSpec{{ID: "#8", Title: "Search", By: "alice", At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}},
			OutsideBlockers:  []string{"#5: Blocker #6 is outside its Spec"},
		},
		StillReady:    3,
		SpecsFinished: []runlog.FinishedSpec{{ID: "#1", Title: "Cart", Branch: "kw/spec-1"}},
		Memory:        &runlog.Memory{PeakEstimateMB: 2048, Waits: 2},
		QueuedWrites:  []string{"comment on #2"},
		Tickets:       []runlog.TicketUsage{{ID: "#2", Usage: runlog.Usage{Input: 10, CacheRead: 5, Output: 3, CostDisplay: "$0.50"}}},
		Usage:         runlog.Usage{Input: 10, CacheRead: 5, Output: 3, CostDisplay: "$0.50"},
		LogDir:        ".fliesswerk/logs/r",
	}

	want := `
Run finished in 3m12s after 2 Iterations. Exit code 1: failures.

Closed:
  #2  Add cart  (Spec #1)  2 commits  context 4500  $0.50

Failed:
  #3  Add tax: no report  [no_report]  context 900

Stopped:
  the Tracker went away
  #7  kw/spec-7
       fix: update it

Skipped:
  #4  Add VAT: names #9 as a Blocker
       fix: link it
  #8  Search: claimed by alice on 2026-11-01
  #5: Blocker #6 is outside its Spec

Still ready at the limit: 3

Specs finished:
  #1  Cart  (kw/spec-1)

Memory: peak estimate 2048 MB, 2 waits

Writes queued for replay:
  comment on #2

Tokens and cost:
  #2  15 in, 3 out  $0.50
  Run  15 in, 3 out  $0.50

Logs: .fliesswerk/logs/r
`
	if got := s.Text(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmptySectionsAreLeftOut(t *testing.T) {
	s := runlog.Summary{DurationSeconds: 5, Iterations: 1, ExitMeaning: "all done", Usage: runlog.Usage{CostDisplay: "no cost reported"}, LogDir: "d"}

	want := `
Run finished in 5s after 1 Iteration. Exit code 0: all done.

Logs: d
`
	if got := s.Text(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTheJSONKeepsEverySectionForScripts(t *testing.T) {
	var term strings.Builder
	l, err := runlog.Open(runlog.Options{Root: t.TempDir(), Start: start, Terminal: &term})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(l.Finish(runlog.Summary{}), &got); err != nil {
		t.Fatal(err)
	}

	skipped, ok := got["skipped"].(map[string]any)
	if !ok {
		t.Fatalf("skipped = %v", got["skipped"])
	}
	for _, list := range []string{"text_only_blockers", "claimed_by_others", "outside_blockers"} {
		if _, ok := skipped[list].([]any); !ok {
			t.Errorf("skipped.%s = %v, want a list", list, skipped[list])
		}
	}
	stopped, _ := got["stopped"].(map[string]any)
	if _, ok := stopped["specs"].([]any); !ok {
		t.Errorf("stopped.specs = %v, want a list", stopped["specs"])
	}
	if _, ok := got["tickets"].([]any); !ok {
		t.Errorf("tickets = %v, want a list", got["tickets"])
	}
	if v, present := got["memory"]; !present || v != nil {
		t.Errorf("memory = %v (present %v), want null", v, present)
	}
}

func TestAStepsEndLineShowsDurationTokensContextAndCost(t *testing.T) {
	got := runlog.StepEnd(75*time.Second, usageOf(), 4500)
	want := "finished in 1m15s, tokens 1150 in, 200 out, context 4500, $0.03"
	if got != want {
		t.Errorf("StepEnd = %q, want %q", got, want)
	}
}

func usageOf() harness.Usage {
	return harness.Usage{Input: 100, CacheRead: 1000, CacheWrite: 50, Output: 200, Cost: 0.03, CostUnit: harness.USD}
}
