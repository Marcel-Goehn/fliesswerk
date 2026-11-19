// Package fake is an in-memory Tracker for tests. Issues are numbered like
// GitHub's, and every write is recorded.
package fake

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// Issue is one issue in the fake Tracker.
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
	Closed bool
	// Parent is the number of the Spec, 0 for none.
	Parent int
	// BlockedBy are the numbers of native Blockers.
	BlockedBy []int
	// TextOnlyBlockers are numbers named as Blockers in text only.
	TextOnlyBlockers []int
	Comments         []tracker.Comment
	Assignees        []string
	// ClaimedBy and ClaimedAt are who added the in-progress label and
	// when. An issue with the label is claimed; without ClaimedBy, by its
	// first assignee.
	ClaimedBy string
	ClaimedAt time.Time
}

// Write is one recorded write.
type Write struct {
	Op     string // "comment", "close", "labels", "claim", "release" or "create_label"
	Ticket int
	Text   string
	Add    []string
	Remove []string
	// By is whose Claim a release drops.
	By string
}

// Tracker is an in-memory Tracker. The zero value is not usable; use New.
type Tracker struct {
	ReadyLabel string
	// InProgressLabel marks a claimed issue; New sets "in-progress".
	InProgressLabel string
	// Me is the login the fake's token acts as; New sets "me".
	Me string
	// Now stamps a Claim; New sets time.Now.
	Now func() time.Time
	// Labels are the labels the fake's repository has.
	Labels []string

	mu     sync.Mutex
	issues map[int]*Issue
	writes []Write
}

var _ tracker.Tracker = (*Tracker)(nil)

// New returns a fake Tracker holding issues, whose ready label is
// readyLabel.
func New(readyLabel string, issues ...Issue) *Tracker {
	t := &Tracker{ReadyLabel: readyLabel, InProgressLabel: "in-progress", Me: "me", Now: time.Now, issues: map[int]*Issue{}}
	for _, i := range issues {
		t.issues[i.Number] = &i
	}
	return t
}

// ID is the ID the fake Tracker gives issue n.
func ID(n int) tracker.ID {
	s := strconv.Itoa(n)
	return tracker.ID{Key: s, Display: "#" + s, Branch: s, Order: int64(n)}
}

func state(closed bool) tracker.State {
	if closed {
		return tracker.Closed
	}
	return tracker.Open
}

// Snapshot returns every open issue with the ready label, and their Specs.
func (t *Tracker) Snapshot(context.Context) (tracker.Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := tracker.Snapshot{Specs: map[string]tracker.Spec{}}
	for _, n := range t.numbers() {
		i := t.issues[n]
		if i.Closed || !slices.Contains(i.Labels, t.ReadyLabel) {
			continue
		}
		tk := tracker.Ticket{
			ID: ID(n), Title: i.Title, Body: i.Body, Labels: slices.Clone(i.Labels),
			State: state(i.Closed), HasChildren: len(t.children(n)) > 0,
		}
		if i.Parent == 0 {
			tk.Claim = t.claim(i)
		}
		for _, b := range i.BlockedBy {
			closed := t.issues[b] != nil && t.issues[b].Closed
			tk.Blockers = append(tk.Blockers, tracker.Blocker{ID: ID(b), State: state(closed)})
		}
		for _, b := range i.TextOnlyBlockers {
			tk.TextOnlyBlockers = append(tk.TextOnlyBlockers, ID(b))
		}
		if i.Parent != 0 {
			p := ID(i.Parent)
			tk.Parent = &p
			snap.Specs[p.Key] = t.spec(i.Parent)
		}
		snap.Tickets = append(snap.Tickets, tk)
	}
	return snap, nil
}

func (t *Tracker) numbers() []int {
	ns := make([]int, 0, len(t.issues))
	for n := range t.issues {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	return ns
}

func (t *Tracker) children(parent int) []int {
	var out []int
	for _, n := range t.numbers() {
		if t.issues[n].Parent == parent {
			out = append(out, n)
		}
	}
	return out
}

func (t *Tracker) spec(n int) tracker.Spec {
	s := tracker.Spec{ID: ID(n)}
	if p := t.issues[n]; p != nil {
		s.Title, s.Body, s.Claim = p.Title, p.Body, t.claim(p)
	}
	for _, c := range t.children(n) {
		i := t.issues[c]
		s.Tickets = append(s.Tickets, tracker.SpecTicket{ID: ID(c), Title: i.Title, State: state(i.Closed)})
	}
	return s
}

func (t *Tracker) issue(id tracker.ID) (*Issue, error) {
	n, err := strconv.Atoi(id.Key)
	if err != nil || t.issues[n] == nil {
		return nil, fmt.Errorf("fake tracker: no issue %s", id.Display)
	}
	return t.issues[n], nil
}

// Comments returns a Ticket's comments.
func (t *Tracker) Comments(_ context.Context, ticket tracker.ID) ([]tracker.Comment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, err := t.issue(ticket)
	if err != nil {
		return nil, err
	}
	return slices.Clone(i.Comments), nil
}

// Comment adds a comment.
func (t *Tracker) Comment(_ context.Context, ticket tracker.ID, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, err := t.issue(ticket)
	if err != nil {
		return err
	}
	i.Comments = append(i.Comments, tracker.Comment{Author: "fliesswerk", Body: text})
	t.writes = append(t.writes, Write{Op: "comment", Ticket: i.Number, Text: text})
	return nil
}

// Close closes a Ticket with a last comment.
func (t *Tracker) Close(_ context.Context, ticket tracker.ID, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, err := t.issue(ticket)
	if err != nil {
		return err
	}
	i.Comments = append(i.Comments, tracker.Comment{Author: "fliesswerk", Body: text})
	i.Closed = true
	t.writes = append(t.writes, Write{Op: "close", Ticket: i.Number, Text: text})
	return nil
}

// SetLabels adds and removes labels.
func (t *Tracker) SetLabels(_ context.Context, ticket tracker.ID, add, remove []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, err := t.issue(ticket)
	if err != nil {
		return err
	}
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return slices.Contains(remove, l) })
	for _, l := range add {
		if !slices.Contains(i.Labels, l) {
			i.Labels = append(i.Labels, l)
		}
	}
	t.writes = append(t.writes, Write{Op: "labels", Ticket: i.Number, Add: add, Remove: remove})
	return nil
}

// claim is i's Claim, nil if it has none.
func (t *Tracker) claim(i *Issue) *tracker.Claim {
	if !slices.Contains(i.Labels, t.InProgressLabel) {
		return nil
	}
	by := i.ClaimedBy
	if by == "" && len(i.Assignees) > 0 {
		by = i.Assignees[0]
	}
	return &tracker.Claim{By: by, At: i.ClaimedAt}
}

// User is Me.
func (t *Tracker) User(context.Context) (string, error) { return t.Me, nil }

// Claims returns every open claimed issue, by number.
func (t *Tracker) Claims(context.Context) ([]tracker.Claimed, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []tracker.Claimed
	for _, n := range t.numbers() {
		i := t.issues[n]
		if c := t.claim(i); c != nil && !i.Closed {
			out = append(out, tracker.Claimed{ID: ID(n), Title: i.Title, Claim: *c})
		}
	}
	return out, nil
}

// Claim adds the in-progress label and Me as assignee.
func (t *Tracker) Claim(_ context.Context, id tracker.ID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, err := t.issue(id)
	if err != nil {
		return err
	}
	if !slices.Contains(i.Labels, t.InProgressLabel) {
		i.Labels = append(i.Labels, t.InProgressLabel)
	}
	if !slices.Contains(i.Assignees, t.Me) {
		i.Assignees = append(i.Assignees, t.Me)
	}
	i.ClaimedBy, i.ClaimedAt = t.Me, t.Now()
	t.writes = append(t.writes, Write{Op: "claim", Ticket: i.Number})
	return nil
}

// Release removes the in-progress label and by as assignee.
func (t *Tracker) Release(_ context.Context, id tracker.ID, by string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, err := t.issue(id)
	if err != nil {
		return err
	}
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return l == t.InProgressLabel })
	i.Assignees = slices.DeleteFunc(i.Assignees, func(a string) bool { return a == by })
	i.ClaimedBy, i.ClaimedAt = "", time.Time{}
	t.writes = append(t.writes, Write{Op: "release", Ticket: i.Number, By: by})
	return nil
}

// MissingLabels returns those of names not in Labels, compared without
// regard to case, like GitHub.
func (t *Tracker) MissingLabels(_ context.Context, names []string) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var missing []string
	for _, n := range names {
		if !slices.ContainsFunc(t.Labels, func(l string) bool { return strings.EqualFold(l, n) }) {
			missing = append(missing, n)
		}
	}
	return missing, nil
}

// CreateLabel adds name to Labels, recording a create_label write with
// the label in Add and its description in Text.
func (t *Tracker) CreateLabel(_ context.Context, name, description string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if slices.ContainsFunc(t.Labels, func(l string) bool { return strings.EqualFold(l, name) }) {
		return fmt.Errorf("fake tracker: the label %s already exists", name)
	}
	t.Labels = append(t.Labels, name)
	t.writes = append(t.writes, Write{Op: "create_label", Add: []string{name}, Text: description})
	return nil
}

// Issues returns every issue as it is now, by number.
func (t *Tracker) Issues() []Issue {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Issue, 0, len(t.issues))
	for _, n := range t.numbers() {
		out = append(out, *t.issues[n])
	}
	return out
}

// Writes returns every write so far, in order.
func (t *Tracker) Writes() []Write {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.writes)
}

// Describe describes the fake Tracker.
func (t *Tracker) Describe() tracker.Description {
	return tracker.Description{
		Kind: "fake", ConfigSection: "tracker", TokenSource: "none",
		LinkBlockerFix: func(ticket, blocker tracker.ID) string {
			return fmt.Sprintf("link %s as a Blocker of %s", blocker, ticket)
		},
		CreateLabelFix: func(name, description string) string {
			return fmt.Sprintf("create the label %s (%s)", name, description)
		},
	}
}
