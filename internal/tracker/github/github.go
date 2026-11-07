// Package github is the Tracker adapter for GitHub Issues. It only ever
// shells out to `gh`; a configured token reaches gh as GH_TOKEN in its
// environment, never on its command line.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// Kind is the `[tracker] kind` that selects this adapter.
const Kind = "github"

// MinVersion is the oldest gh whose `--json` returns parent, subIssues and
// blockedBy.
var MinVersion = [3]int{2, 94, 0}

// Runner runs gh with args, feeding it stdin, and returns its stdout. An
// error carries what gh printed on stderr.
type Runner interface {
	Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
}

// Options configure a Tracker.
type Options struct {
	// Repo is owner/name. Empty means the repository gh finds from the
	// current directory.
	Repo       string
	ReadyLabel string
	Runner     Runner
	// GOOS picks the install command `check` names; runtime.GOOS in
	// production.
	GOOS string
}

// Tracker is the GitHub Issues Tracker.
type Tracker struct {
	opts Options
}

var _ tracker.Tracker = (*Tracker)(nil)

// New returns a GitHub Tracker.
func New(opts Options) *Tracker {
	return &Tracker{opts: opts}
}

// The JSON gh prints.
type (
	ghRef struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Title  string `json:"title"`
		URL    string `json:"url"`
	}
	ghRefs struct {
		Nodes      []ghRef `json:"nodes"`
		TotalCount int     `json:"totalCount"`
	}
	ghIssue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
		URL    string `json:"url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Parent    *ghRef `json:"parent"`
		SubIssues ghRefs `json:"subIssues"`
		BlockedBy ghRefs `json:"blockedBy"`
	}
	ghComments struct {
		Comments []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"comments"`
	}
)

const (
	// listLimit caps one Snapshot; reaching it is an error, not a cut.
	listLimit  = 1000
	listFields = "number,title,body,labels,state,url,parent,subIssues,blockedBy"
	specFields = "number,title,body,state,url,subIssues"
)

// Snapshot reads every open issue with the ready label in one gh call,
// then each parent Spec in one call per Spec.
func (t *Tracker) Snapshot(ctx context.Context) (tracker.Snapshot, error) {
	args := t.args("issue", "list", "")
	args = append(args, "--state", "open", "--label", t.opts.ReadyLabel, "--limit", strconv.Itoa(listLimit), "--json", listFields)
	var issues []ghIssue
	if err := t.runJSON(ctx, &issues, args...); err != nil {
		return tracker.Snapshot{}, fmt.Errorf("reading the ready Tickets: %w", err)
	}

	home := t.opts.Repo
	if home == "" && len(issues) > 0 {
		home = repoOf(issues[0].URL)
	}
	if len(issues) >= listLimit {
		return tracker.Snapshot{}, fmt.Errorf("gh returned %d ready issues, its limit; this kraftwerk can't read more", len(issues))
	}
	snap := tracker.Snapshot{Specs: map[string]tracker.Spec{}}
	for _, i := range issues {
		// A cut-short Blocker list would make a blocked Ticket look ready.
		if len(i.BlockedBy.Nodes) < i.BlockedBy.TotalCount {
			return tracker.Snapshot{}, fmt.Errorf("gh returned only %d of its %d Blockers for #%d; can't tell whether it is ready",
				len(i.BlockedBy.Nodes), i.BlockedBy.TotalCount, i.Number)
		}
		tk := tracker.Ticket{
			ID:          idFor(home, i.URL, i.Number),
			Title:       i.Title,
			Body:        i.Body,
			State:       stateOf(i.State),
			HasChildren: i.SubIssues.TotalCount > 0,
		}
		for _, l := range i.Labels {
			tk.Labels = append(tk.Labels, l.Name)
		}
		linked := map[string]bool{}
		for _, b := range i.BlockedBy.Nodes {
			id := idFor(home, b.URL, b.Number)
			linked[id.Key] = true
			tk.Blockers = append(tk.Blockers, tracker.Blocker{ID: id, State: stateOf(b.State)})
		}
		for _, ref := range textBlockers(home, i.Body) {
			if !linked[ref.Key] {
				tk.TextOnlyBlockers = append(tk.TextOnlyBlockers, ref)
			}
		}
		if i.Parent != nil {
			p := idFor(home, i.Parent.URL, i.Parent.Number)
			tk.Parent = &p
			if _, done := snap.Specs[p.Key]; !done {
				spec, err := t.spec(ctx, home, p)
				if err != nil {
					return tracker.Snapshot{}, err
				}
				snap.Specs[p.Key] = spec
			}
		}
		snap.Tickets = append(snap.Tickets, tk)
	}
	return snap, nil
}

func (t *Tracker) spec(ctx context.Context, home string, id tracker.ID) (tracker.Spec, error) {
	var i ghIssue
	if err := t.runJSON(ctx, &i, append(t.args("issue", "view", id.Key), "--json", specFields)...); err != nil {
		return tracker.Spec{}, fmt.Errorf("reading Spec %s: %w", id.Display, err)
	}
	if len(i.SubIssues.Nodes) < i.SubIssues.TotalCount {
		return tracker.Spec{}, fmt.Errorf("gh returned only %d of the %d Tickets of Spec %s", len(i.SubIssues.Nodes), i.SubIssues.TotalCount, id.Display)
	}
	s := tracker.Spec{ID: id, Title: i.Title, Body: i.Body}
	for _, c := range i.SubIssues.Nodes {
		s.Tickets = append(s.Tickets, tracker.SpecTicket{ID: idFor(home, c.URL, c.Number), Title: c.Title, State: stateOf(c.State)})
	}
	return s, nil
}

// Comments returns a Ticket's comments, oldest first.
func (t *Tracker) Comments(ctx context.Context, ticket tracker.ID) ([]tracker.Comment, error) {
	var c ghComments
	if err := t.runJSON(ctx, &c, append(t.args("issue", "view", ticket.Key), "--json", "comments")...); err != nil {
		return nil, fmt.Errorf("reading the comments of %s: %w", ticket.Display, err)
	}
	out := make([]tracker.Comment, 0, len(c.Comments))
	for _, gc := range c.Comments {
		out = append(out, tracker.Comment{Author: gc.Author.Login, CreatedAt: gc.CreatedAt, Body: gc.Body})
	}
	return out, nil
}

// Comment adds a comment. The text goes to gh on stdin, so no comment is
// ever too long for a command line.
func (t *Tracker) Comment(ctx context.Context, ticket tracker.ID, text string) error {
	args := append(t.args("issue", "comment", ticket.Key), "--body-file", "-")
	if _, err := t.opts.Runner.Run(ctx, []byte(text), args...); err != nil {
		return fmt.Errorf("commenting on %s: %w", ticket.Display, err)
	}
	return nil
}

// Close comments, then closes the Ticket as completed. gh can only take a
// closing comment on its command line, so the two are separate calls.
func (t *Tracker) Close(ctx context.Context, ticket tracker.ID, text string) error {
	if err := t.Comment(ctx, ticket, text); err != nil {
		return err
	}
	args := append(t.args("issue", "close", ticket.Key), "--reason", "completed")
	if _, err := t.opts.Runner.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("closing %s: %w", ticket.Display, err)
	}
	return nil
}

// SetLabels adds and removes labels.
func (t *Tracker) SetLabels(ctx context.Context, ticket tracker.ID, add, remove []string) error {
	args := t.args("issue", "edit", ticket.Key)
	for _, l := range add {
		args = append(args, "--add-label", l)
	}
	for _, l := range remove {
		args = append(args, "--remove-label", l)
	}
	if _, err := t.opts.Runner.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("setting the labels of %s: %w", ticket.Display, err)
	}
	return nil
}

// Describe declares what the GitHub Tracker needs.
func (t *Tracker) Describe() tracker.Description {
	return tracker.Description{
		Kind:          Kind,
		ConfigSection: "tracker",
		Questions: []tracker.Question{
			{Key: "tracker.repo", Prompt: "Which GitHub repository holds your Tickets (owner/name)?"},
			{Key: "tracker.ready_label", Prompt: "Which label marks a Ticket Ready for agent?", Default: "ready-for-agent"},
		},
		Checks: []tracker.Check{
			{Name: "gh", Run: t.checkVersion},
			{Name: "gh login", Run: t.checkLogin},
		},
		TokenSource:    "`[tracker] token` in config.local.toml, handed to gh as GH_TOKEN; without it, gh's own login",
		LinkBlockerFix: t.linkBlockerFix,
	}
}

func (t *Tracker) linkBlockerFix(ticket, blocker tracker.ID) string {
	args := t.args("issue", "edit", ticket.Key)
	return "gh " + strings.Join(append(args, "--add-blocked-by", target(blocker.Key)), " ")
}

// args starts a gh command on the issue with the given key (none if
// empty). An issue of another repo is addressed by URL.
func (t *Tracker) args(cmd, sub, key string) []string {
	args := []string{cmd, sub}
	if key != "" {
		args = append(args, target(key))
		if strings.Contains(key, "#") {
			return args
		}
	}
	if t.opts.Repo != "" {
		args = append(args, "--repo", t.opts.Repo)
	}
	return args
}

// target is what gh takes to name the issue with key.
func target(key string) string {
	if repo, n, ok := strings.Cut(key, "#"); ok {
		return "https://github.com/" + repo + "/issues/" + n
	}
	return key
}

func (t *Tracker) runJSON(ctx context.Context, v any, args ...string) error {
	out, err := t.opts.Runner.Run(ctx, nil, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("gh printed JSON this kraftwerk can't read: %w", err)
	}
	return nil
}

// idFor makes the ID of issue n at issueURL. Issues of the home repo are
// "#n"; others are "owner/name#n".
func idFor(home, issueURL string, n int) tracker.ID {
	num := strconv.Itoa(n)
	repo := repoOf(issueURL)
	if repo == "" || strings.EqualFold(repo, home) {
		return tracker.ID{Key: num, Display: "#" + num, Branch: num, Order: int64(n)}
	}
	return tracker.ID{
		Key:     repo + "#" + num,
		Display: repo + "#" + num,
		Branch:  strings.ReplaceAll(repo, "/", "-") + "-" + num,
		Order:   int64(n),
	}
}

// repoOf is the owner/name of an issue URL, empty if it isn't one.
func repoOf(issueURL string) string {
	u, err := url.Parse(issueURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "issues" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func stateOf(s string) tracker.State {
	if strings.EqualFold(s, "closed") {
		return tracker.Closed
	}
	return tracker.Open
}

// ErrNotInstalled is what a Runner returns when there is no gh to run.
var ErrNotInstalled = errors.New("gh is not installed")
