package harness

// EventKind is what an Event is.
type EventKind int

const (
	// Text is text the agent wrote.
	Text EventKind = iota
	// ToolCall is a tool the agent called.
	ToolCall
	// UsageReport is token usage, and cost where the Harness reports it.
	UsageReport
	// Error is a failure the Harness reported, with its class.
	Error
	// Final is the end of the session, with the report if there is one.
	Final
)

func (k EventKind) String() string {
	switch k {
	case Text:
		return "text"
	case ToolCall:
		return "tool_call"
	case UsageReport:
		return "usage"
	case Error:
		return "error"
	case Final:
		return "final"
	}
	return "unknown"
}

// ErrorClass decides what the core does after an Error.
type ErrorClass string

const (
	// Auth stops the Run: the credential needs fixing.
	Auth ErrorClass = "auth"
	// Transient is a rate limit, an overload or a network failure: the
	// same Step is retried with backoff.
	Transient ErrorClass = "transient"
	// Other is any other failure: one fresh retry of the Step.
	Other ErrorClass = "other"
	// OutOfMemory is a Harness something killed outright before the end of
	// its session, which is what running out of memory looks like from
	// outside: the memory estimate is raised and the Step gets one fresh
	// retry.
	OutOfMemory ErrorClass = "out_of_memory"
)

// Event is one common event parsed from a Harness's output. Only the
// fields of its Kind are set.
type Event struct {
	Kind EventKind

	// Text is the agent's text (Text) or the error message (Error).
	Text string

	// Tool is the tool's name and Summary a one-line summary of its
	// input (ToolCall).
	Tool    string
	Summary string

	// Usage is set for UsageReport.
	Usage Usage
	// Cumulative marks the Usage of the whole Step, as opposed to that of
	// one model call. A Step's cumulative Usage supersedes its per-call
	// ones.
	Cumulative bool
	// CallID names the model call a per-call Usage belongs to. A Harness
	// may report one call's Usage more than once; the core counts it once.
	CallID string

	// Class is set for Error.
	Class ErrorClass

	// Report is the agent's report (Final), nil if it gave none or one
	// that doesn't match the report schema.
	Report *Report

	// Terminal marks the Harness's last event of a session (Claude Code's
	// `result`). After it the core only waits a grace period.
	Terminal bool
}

// CostUnit is the unit a Harness reports cost in.
type CostUnit string

const (
	// NoCost means the Harness reported no cost. A cost is never made up.
	NoCost  CostUnit = ""
	USD     CostUnit = "usd"
	Credits CostUnit = "credits"
)

// Usage is token usage, normalised across Harnesses.
type Usage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	// Cost is in CostUnit, and zero when CostUnit is NoCost.
	Cost     float64
	CostUnit CostUnit
}
