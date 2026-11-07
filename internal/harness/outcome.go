package harness

import "fmt"

// OutcomeKind is how a Step ended.
type OutcomeKind int

const (
	// Reported means the agent gave a report that matches the schema.
	Reported OutcomeKind = iota
	// NoReport means the agent finished without a valid report: a failure,
	// never a success.
	NoReport
	// Failed means the Harness reported an error, or exited before the end
	// of the session.
	Failed
)

func (k OutcomeKind) String() string {
	switch k {
	case Reported:
		return "report"
	case NoReport:
		return "no_report"
	case Failed:
		return "error"
	}
	return "unknown"
}

// Outcome is how a Step ended: exactly one of a report, no report, or an
// error with its class. It also holds the Step's Usage.
type Outcome struct {
	Kind OutcomeKind
	// Report is set when Kind is Reported.
	Report Report
	// Class and Message are set when Kind is Failed.
	Class   ErrorClass
	Message string
	Usage   Usage
}

// Resolve decides a Step's Outcome from all its events and its exit code.
// Failures are read from the stream first: an error event decides the
// Outcome whatever the exit code, the most severe class winning (auth,
// then transient, then other). A report decides it even on a non-zero
// exit, since the core kills a Harness that lingers after its terminal
// event. Without a terminal event, a non-zero exit is an error and a zero
// one is no report.
func Resolve(events []Event, exitCode int) Outcome {
	out := Outcome{Kind: NoReport, Usage: stepUsage(events)}
	var failure *Event
	terminal := false
	for i, e := range events {
		terminal = terminal || e.Terminal
		switch e.Kind {
		case Error:
			if failure == nil || severity(e.Class) > severity(failure.Class) {
				failure = &events[i]
			}
		case Final:
			if e.Report != nil {
				out.Kind, out.Report = Reported, *e.Report
			}
		}
	}
	switch {
	case failure != nil:
		return Outcome{Kind: Failed, Class: failure.Class, Message: failure.Text, Usage: out.Usage}
	case out.Kind == Reported:
		return out
	case !terminal && exitCode != 0:
		return Outcome{Kind: Failed, Class: Other, Message: fmt.Sprintf("exited with code %d before the end of the session", exitCode), Usage: out.Usage}
	}
	return out
}

func severity(c ErrorClass) int {
	switch c {
	case Auth:
		return 2
	case Transient:
		return 1
	}
	return 0
}

// stepUsage is the Step's cumulative Usage if the Harness reported one,
// otherwise the sum of its per-call Usages, each call counted once with
// the last Usage reported for it. Per-call Usages carry no cost, and
// none is made up.
func stepUsage(events []Event) Usage {
	var calls []string
	byCall := map[string]Usage{}
	for _, e := range events {
		if e.Kind != UsageReport {
			continue
		}
		if e.Cumulative {
			return e.Usage
		}
		if _, ok := byCall[e.CallID]; !ok {
			calls = append(calls, e.CallID)
		}
		byCall[e.CallID] = e.Usage
	}
	var sum Usage
	for _, id := range calls {
		u := byCall[id]
		sum.Input += u.Input
		sum.Output += u.Output
		sum.CacheRead += u.CacheRead
		sum.CacheWrite += u.CacheWrite
	}
	return sum
}
