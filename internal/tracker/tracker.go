// Package tracker is the boundary between the Factory's core and the
// Tracker its Specs and Tickets live in. Adapters (GitHub first) are
// compiled in and implement Tracker; the core only sees the types here.
package tracker

import (
	"context"
	"time"
)

// ID names an issue in a Tracker. It is opaque: the core compares IDs by
// Key and shows them, but never parses them. Only the adapter that made an
// ID may look inside it.
type ID struct {
	// Key is unique within the Tracker.
	Key string
	// Display is how a person refers to the issue, e.g. "#42".
	Display string
	// Branch is a form safe to put in a git branch name, e.g. "42".
	Branch string
	// Order sorts issues of one Tracker, lower first (older, for GitHub).
	Order int64
}

func (id ID) String() string { return id.Display }

// State is whether an issue is open or closed.
type State int

const (
	Open State = iota
	Closed
)

func (s State) String() string {
	if s == Closed {
		return "closed"
	}
	return "open"
}

// Blocker is an issue that must be closed before a Ticket can be worked on,
// as recorded by the Tracker's own blocking relationship.
type Blocker struct {
	ID    ID
	State State
}

// Ticket is an issue as the Snapshot found it: every open issue with the
// ready label, Specs included. Whether it is ready is the core's call.
type Ticket struct {
	ID     ID
	Title  string
	Body   string
	Labels []string
	State  State
	// Parent is the Spec the Ticket belongs to, nil for a standalone Ticket.
	Parent *ID
	// HasChildren reports whether the issue has sub-issues, which makes it
	// a Spec.
	HasChildren bool
	Blockers    []Blocker
	// TextOnlyBlockers are issues the Ticket's text names as Blockers that
	// have no matching native blocking link. Text never makes a Blocker.
	TextOnlyBlockers []ID
}

// SpecTicket is one Ticket in a Spec's list.
type SpecTicket struct {
	ID    ID
	Title string
	State State
}

// Spec is the parent of one or more Tickets in the Snapshot.
type Spec struct {
	ID    ID
	Title string
	Body  string
	// Tickets is the Spec's full list of sub-issues, in the Tracker's
	// order.
	Tickets []SpecTicket
}

// Snapshot is what the Tracker holds that the core needs to pick Tickets.
type Snapshot struct {
	Tickets []Ticket
	// Specs holds the parent of every Ticket that has one, by ID.Key.
	Specs map[string]Spec
}

// Comment is one comment on an issue.
type Comment struct {
	Author    string
	CreatedAt time.Time
	Body      string
}

// Tracker is the boundary every Tracker adapter implements. There are no
// other writes than Comment, Close and SetLabels.
type Tracker interface {
	// Snapshot reads every open Ticket with the ready label, and the Spec
	// of each.
	Snapshot(ctx context.Context) (Snapshot, error)
	// Comments returns a Ticket's comments, oldest first.
	Comments(ctx context.Context, ticket ID) ([]Comment, error)
	// Comment adds a comment to a Ticket.
	Comment(ctx context.Context, ticket ID, text string) error
	// Close closes a Ticket as completed, with text as its last comment.
	Close(ctx context.Context, ticket ID, text string) error
	// SetLabels adds and removes labels on a Ticket.
	SetLabels(ctx context.Context, ticket ID, add, remove []string) error
	// Describe tells the rest of the Factory what this Tracker needs.
	Describe() Description
}

// Description is what a Tracker declares about itself.
type Description struct {
	// Kind is the value of `[tracker] kind` that selects it.
	Kind string
	// ConfigSection is the config table that holds its settings.
	ConfigSection string
	// Questions are what `init` asks to fill in ConfigSection.
	Questions []Question
	// Checks are the machine checks `check` runs, in order.
	Checks []Check
	// TokenSource says, in plain words, where the Tracker's token comes
	// from.
	TokenSource string
	// LinkBlockerFix is how to turn a text-only Blocker into a native one.
	LinkBlockerFix func(ticket, blocker ID) string
}

// Question is one question `init` asks.
type Question struct {
	// Key is the dotted config key the answer goes to.
	Key     string
	Prompt  string
	Default string
}

// Check is one machine check. Run returns an error that names the fix.
type Check struct {
	Name string
	Run  func(ctx context.Context) error
}
