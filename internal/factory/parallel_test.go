package factory_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

const specBranch = "kraftwerk/spec-1-spec-shopping-cart"

// meeting lets n Tickets wait for each other. It proves they overlap:
// Tickets that run one after the other never all arrive, however often
// each is retried.
type meeting struct {
	mu   sync.Mutex
	n    int
	here map[string]bool
	all  chan struct{}
}

func newMeeting(n int) *meeting {
	return &meeting{n: n, here: map[string]bool{}, all: make(chan struct{})}
}

// arrive blocks until all n Tickets have arrived, and reports whether they
// did in time.
func (m *meeting) arrive(ticket string) bool {
	m.mu.Lock()
	if !m.here[ticket] {
		m.here[ticket] = true
		if len(m.here) == m.n {
			close(m.all)
		}
	}
	m.mu.Unlock()
	select {
	case <-m.all:
		return true
	case <-time.After(10 * time.Second):
		return false
	}
}

func failedStep(why string) harness.Outcome {
	return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: why}
}

// ticketsOf is a Spec #1 with the Tickets numbered 2 and up, by title.
func ticketsOf(titles ...string) []fake.Issue {
	issues := []fake.Issue{{Number: 1, Title: "Spec: Shopping cart", Body: "A cart.", Labels: []string{ready}}}
	for i, title := range titles {
		issues = append(issues, fake.Issue{Number: i + 2, Title: title, Labels: []string{ready}, Parent: 1})
	}
	return issues
}

// byTicket runs the step of the Ticket it is for.
func byTicket(steps map[string]func(context.Context, factory.Step) harness.Outcome) func(context.Context, factory.Step) harness.Outcome {
	return func(ctx context.Context, s factory.Step) harness.Outcome {
		step, ok := steps[s.Ticket.ID.Display]
		if !ok {
			return failedStep("no step for " + s.Ticket.ID.Display)
		}
		return step(ctx, s)
	}
}

// commitFile commits a file with this content.
func commitFile(name, content string) func(context.Context, factory.Step) harness.Outcome {
	return commits(map[string]string{name: content}, "Added "+name+".")
}

// noWorkLeft checks that no Ticket branch or Factory worktree is left.
func noWorkLeft(t *testing.T, f fixture) {
	t.Helper()
	if got := git(t, f.work, "branch", "--list", "kraftwerk/ticket-*"); got != "" {
		t.Errorf("Ticket branches are left: %s", got)
	}
	if got := git(t, f.work, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Errorf("Factory worktrees are left:\n%s", got)
	}
}

// meets commits a file once the meeting is complete.
func meets(t *testing.T, m *meeting, name string) func(context.Context, factory.Step) harness.Outcome {
	t.Helper()
	return func(ctx context.Context, s factory.Step) harness.Outcome {
		if !m.arrive(s.Ticket.ID.Display) {
			return failedStep(s.Ticket.ID.Display + " ran alone: the Tickets didn't overlap")
		}
		return commitFile(name, "package x\n")(ctx, s)
	}
}

func TestAnIterationWorksUpToMaxParallelTicketsAtOnceAndMergesThemAll(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Add a", "Add b", "Add c")...)
	m := newMeeting(2)
	var closedWhenLastRan []bool
	opts := failOptions(f, tr, byTicket(map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": meets(t, m, "a.go"),
		"#3": meets(t, m, "b.go"),
		"#4": func(ctx context.Context, s factory.Step) harness.Outcome {
			closedWhenLastRan = []bool{issue(t, tr, 2).Closed, issue(t, tr, 3).Closed}
			return commitFile("c.go", "package x\n")(ctx, s)
		},
	}))
	opts.MaxParallel = 2

	res := factory.Run(context.Background(), opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if res.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2: #2 and #3 together, then #4", res.Iterations)
	}
	if len(closedWhenLastRan) != 2 || !closedWhenLastRan[0] || !closedWhenLastRan[1] {
		t.Errorf("#2 and #3 closed when #4 ran = %v, want both: the Iteration ends only when all of its Tickets have", closedWhenLastRan)
	}
	tip := f.remoteRef(t, specBranch)
	got := git(t, f.remote, "ls-tree", "--name-only", tip)
	for _, want := range []string{"a.go", "b.go", "c.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Spec branch lacks %s; it has:\n%s", want, got)
		}
	}
	if local := git(t, f.work, "rev-parse", specBranch); local != tip {
		t.Errorf("the local Spec branch is at %s, the remote one at %s", local, tip)
	}
	if merges := git(t, f.remote, "log", "--merges", "--format=%H", tip); len(strings.Fields(merges)) != 1 {
		t.Errorf("the Spec branch has merge commits %q, want one: the first Ticket fast-forwards, the second merges", merges)
	}
	noWorkLeft(t, f)
}

// waitFor polls until ok, and reports whether that happened in time.
func waitFor(ok func() bool) bool {
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return true
		}
	}
	return false
}

// afterClosed runs step once Ticket n is closed.
func afterClosed(t *testing.T, tr *fake.Tracker, n int, step func(context.Context, factory.Step) harness.Outcome) func(context.Context, factory.Step) harness.Outcome {
	t.Helper()
	return func(ctx context.Context, s factory.Step) harness.Outcome {
		if !waitFor(func() bool { return issue(t, tr, n).Closed }) {
			return failedStep(fmt.Sprintf("#%d never closed", n))
		}
		return step(ctx, s)
	}
}

// attempts runs the nth step at the nth call, and counts the calls.
type attempts struct {
	calls int
	steps []func(context.Context, factory.Step) harness.Outcome
}

func (a *attempts) run(ctx context.Context, s factory.Step) harness.Outcome {
	a.calls++
	if a.calls > len(a.steps) {
		return failedStep(fmt.Sprintf("%s was implemented %d times", s.Ticket.ID, a.calls))
	}
	return a.steps[a.calls-1](ctx, s)
}

// parallel are options for a Run working on two Tickets at once.
func parallel(f fixture, tr tracker.Tracker, steps map[string]func(context.Context, factory.Step) harness.Outcome) factory.Options {
	opts := failOptions(f, tr, byTicket(steps))
	opts.MaxParallel = 2
	return opts
}

func TestAConflictingTicketIsImplementedAfreshFromTheUpdatedSpecBranch(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Write it one way", "Write it another way")...)
	var sawOnRetry string
	ticket3 := &attempts{steps: []func(context.Context, factory.Step) harness.Outcome{
		afterClosed(t, tr, 2, commitFile("shared.txt", "from 3\n")),
		func(ctx context.Context, s factory.Step) harness.Outcome {
			data, err := os.ReadFile(filepath.Join(s.Dir, "shared.txt"))
			if err != nil {
				return failedStep(err.Error())
			}
			sawOnRetry = string(data)
			return commitFile("other.txt", "from 3\n")(ctx, s)
		},
	}}

	res := factory.Run(context.Background(), parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": commitFile("shared.txt", "from 2\n"),
		"#3": ticket3.run,
	}))

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if ticket3.calls != 2 || res.Iterations != 2 {
		t.Errorf("#3 was implemented %d times over %d Iterations, want twice over 2", ticket3.calls, res.Iterations)
	}
	if sawOnRetry != "from 2\n" {
		t.Errorf("the retry saw shared.txt = %q, want #2's: it starts from the updated Spec branch", sawOnRetry)
	}
	if !issue(t, tr, 3).Closed || len(issue(t, tr, 3).Comments) != 1 {
		t.Errorf("#3 = %+v, want it closed, with only the closing comment", issue(t, tr, 3))
	}
	tip := f.remoteRef(t, specBranch)
	if got := git(t, f.remote, "show", tip+":shared.txt"); got != "from 2" {
		t.Errorf("shared.txt on the Spec branch = %q, want #2's", got)
	}
	noWorkLeft(t, f)
}

func TestASecondFailedMergeHandsTheTicketBack(t *testing.T) {
	f := newFixture(t)
	issues := ticketsOf("Write it one way", "Write it another way", "Write it a ticket3 way")
	issues[3].BlockedBy = []int{2}
	tr := fake.New(ready, issues...)
	ticket3 := &attempts{steps: []func(context.Context, factory.Step) harness.Outcome{
		afterClosed(t, tr, 2, commitFile("shared.txt", "from 3\n")),
		afterClosed(t, tr, 4, commitFile("shared.txt", "from 3 again\n")),
	}}

	res := factory.Run(context.Background(), parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": commitFile("shared.txt", "from 2\n"),
		"#3": ticket3.run,
		"#4": commitFile("shared.txt", "from 4\n"),
	}))

	if ticket3.calls != 2 {
		t.Errorf("#3 was implemented %d times, want twice", ticket3.calls)
	}
	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindMerge {
		t.Fatalf("failed = %+v, want #3's merge", res.Failed)
	}
	handedBack(t, tr, 3, "conflict in shared.txt")
	if got := git(t, f.remote, "show", f.remoteRef(t, specBranch)+":shared.txt"); got != "from 4" {
		t.Errorf("shared.txt on the Spec branch = %q, want #4's", got)
	}
	noWorkLeft(t, f)
}

func TestAFailedVerifyOnTheMergedResultReQueuesTheTicketAndLeavesTheSpecBranch(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Add a", "Add b")...)
	var tipAtRetry string
	ticket3 := &attempts{steps: []func(context.Context, factory.Step) harness.Outcome{
		afterClosed(t, tr, 2, commitFile("b.go", "package x\n")),
		func(ctx context.Context, s factory.Step) harness.Outcome {
			tipAtRetry = f.remoteRef(t, specBranch)
			return commitFile("b.go", "package x\n")(ctx, s)
		},
	}}
	opts := parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": commitFile("a.go", "package x\n"),
		"#3": ticket3.run,
	})
	// It passes everywhere but on a merge commit.
	opts.Verify = []verify.Command{{Name: "test", Line: "git rev-parse --verify -q HEAD^2 && exit 1 || exit 0"}}

	res := factory.Run(context.Background(), opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if ticket3.calls != 2 {
		t.Errorf("#3 was implemented %d times, want twice", ticket3.calls)
	}
	if merges := git(t, f.remote, "log", "--merges", "--format=%H", tipAtRetry); merges != "" {
		t.Errorf("the merge that failed verify reached the remote: %s", merges)
	}
	if !issue(t, tr, 3).Closed {
		t.Error("#3 wasn't closed")
	}
	noWorkLeft(t, f)
}

// holdingClose is a Tracker whose Close of one Ticket waits.
type holdingClose struct {
	*fake.Tracker
	ticket  tracker.ID
	reached chan struct{}
	release chan struct{}
}

func (h *holdingClose) Close(ctx context.Context, id tracker.ID, text string) error {
	if id.Key == h.ticket.Key {
		close(h.reached)
		<-h.release
	}
	return h.Tracker.Close(ctx, id, text)
}

func TestMergesOfASpecRunOneAtATimeInTheOrderTheTicketsFinished(t *testing.T) {
	f := newFixture(t)
	tr := &holdingClose{
		Tracker: fake.New(ready, ticketsOf("Add a", "Add b", "Add c")...),
		ticket:  fake.ID(2), reached: make(chan struct{}), release: make(chan struct{}),
	}
	thirdQueued, fourthQueued := make(chan struct{}), make(chan struct{})
	after := func(gate <-chan struct{}, name string) func(context.Context, factory.Step) harness.Outcome {
		return func(ctx context.Context, s factory.Step) harness.Outcome {
			select {
			case <-gate:
			case <-time.After(20 * time.Second):
				return failedStep("the gate never opened")
			}
			return commitFile(name, "package x\n")(ctx, s)
		}
	}
	opts := failOptions(f, nil, byTicket(map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": commitFile("a.go", "package x\n"),
		"#3": after(tr.reached, "b.go"),
		"#4": after(thirdQueued, "c.go"),
	}))
	opts.Tracker, opts.MaxParallel = tr, 3
	opts.Log = func(l runlog.Line) {
		if !strings.Contains(l.Text, "queued to merge") {
			return
		}
		switch l.Ticket {
		case "#3":
			close(thirdQueued)
		case "#4":
			close(fourthQueued)
			close(tr.release)
		}
	}

	res := factory.Run(context.Background(), opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	var order []string
	for _, tk := range res.Closed {
		order = append(order, tk.ID.Display)
	}
	if got := strings.Join(order, " "); got != "#2 #3 #4" {
		t.Errorf("closed in the order %s, want #2 #3 #4", got)
	}
}

func TestTicketsOfTwoSpecsLandOnTheirOwnBranches(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready,
		fake.Issue{Number: 1, Title: "Spec: One", Labels: []string{ready}},
		fake.Issue{Number: 2, Title: "Add a", Labels: []string{ready}, Parent: 1},
		fake.Issue{Number: 10, Title: "Spec: Two", Labels: []string{ready}},
		fake.Issue{Number: 11, Title: "Add b", Labels: []string{ready}, Parent: 10})
	m := newMeeting(2)

	res := factory.Run(context.Background(), parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2":  meets(t, m, "a.go"),
		"#11": meets(t, m, "b.go"),
	}))

	if res.Stopped != nil || len(res.Failed) != 0 || len(res.Closed) != 2 {
		t.Fatalf("result = %+v, want both closed", res)
	}
	for branch, want := range map[string]string{"kraftwerk/spec-1-spec-one": "a.go", "kraftwerk/spec-10-spec-two": "b.go"} {
		files := strings.Fields(git(t, f.remote, "ls-tree", "--name-only", f.remoteRef(t, branch)))
		if !slices.Contains(files, want) || slices.Contains(files, map[string]string{"a.go": "b.go", "b.go": "a.go"}[want]) {
			t.Errorf("%s holds %v, want %s and not the other Spec's file", branch, files, want)
		}
	}
	if len(res.SpecsFinished) != 2 {
		t.Errorf("Specs finished = %+v, want both", res.SpecsFinished)
	}
}

// untilKilled is a Step that runs until it is killed, closing started
// first.
func untilKilled(started chan struct{}, killed *atomic.Bool) func(context.Context, factory.Step) harness.Outcome {
	return func(ctx context.Context, _ factory.Step) harness.Outcome {
		close(started)
		select {
		case <-ctx.Done():
			killed.Store(true)
		case <-time.After(30 * time.Second):
		}
		return failedStep("killed")
	}
}

func TestARejectedPushKillsThatSpecsTicketsInFlightAndOtherSpecsCarryOn(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, append(ticketsOf("Add a", "Add b"),
		fake.Issue{Number: 10, Title: "Spec: Search", Labels: []string{ready}},
		fake.Issue{Number: 11, Title: "Add search", Labels: []string{ready}, Parent: 10})...)
	started := make(chan struct{})
	var killed atomic.Bool
	opts := failOptions(f, tr, byTicket(map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": func(ctx context.Context, s factory.Step) harness.Outcome {
			<-started
			// Someone else pushes to the Spec branch in the meantime.
			git(t, s.Dir, "commit", "-q", "--allow-empty", "-m", "Someone else's commit")
			git(t, s.Dir, "push", "-q", "origin", "HEAD:refs/heads/"+specBranch)
			git(t, s.Dir, "reset", "-q", "--hard", "HEAD~1")
			return commitFile("a.go", "package x\n")(ctx, s)
		},
		"#3": untilKilled(started, &killed),
		"#11": func(ctx context.Context, s factory.Step) harness.Outcome {
			if !waitFor(killed.Load) {
				return failedStep("#3 was never killed")
			}
			return commitFile("search.go", "package x\n")(ctx, s)
		},
	}))
	opts.MaxParallel = 3

	res := factory.Run(context.Background(), opts)

	if res.Stopped != nil {
		t.Fatalf("the Run stopped: %v", res.Stopped)
	}
	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindPushRejected {
		t.Fatalf("failed = %+v, want only #2's push", res.Failed)
	}
	if !killed.Load() {
		t.Error("#3's Step wasn't killed")
	}
	if i := issue(t, tr, 3); i.Closed || len(i.Comments) != 0 || !slices.Contains(i.Labels, ready) {
		t.Errorf("#3 = %+v, want it left untouched", i)
	}
	if !issue(t, tr, 11).Closed {
		t.Error("#11 wasn't closed: the other Spec should carry on")
	}
	if gitOK(f.work, "rev-parse", "--verify", "-q", "refs/heads/kraftwerk/ticket-3") {
		t.Error("#3's work wasn't discarded")
	}
}

func TestAnErrorThatStopsTheRunKillsTheOtherTicketsInFlight(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Add a", "Add b")...)
	started := make(chan struct{})
	var killed atomic.Bool

	res := factory.Run(context.Background(), parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": func(context.Context, factory.Step) harness.Outcome {
			<-started
			return harness.Outcome{Kind: harness.Failed, Class: harness.Auth, Message: "bad key"}
		},
		"#3": untilKilled(started, &killed),
	}))

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "auth error") {
		t.Fatalf("stopped = %v, want the auth error, not the kill it caused", res.Stopped)
	}
	if !killed.Load() {
		t.Error("#3's Step wasn't killed")
	}
	if i := issue(t, tr, 3); i.Closed || len(i.Comments) != 0 {
		t.Errorf("#3 = %+v, want it left untouched", i)
	}
}

func TestDrainingLetsEveryTicketInFlightLandAndStartsNothingNew(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Add a", "Add b", "Add c")...)
	m, drain := newMeeting(2), make(chan struct{})
	var once sync.Once
	drained := func(name string) func(context.Context, factory.Step) harness.Outcome {
		return func(ctx context.Context, s factory.Step) harness.Outcome {
			if !m.arrive(s.Ticket.ID.Display) {
				return failedStep("the Tickets didn't overlap")
			}
			once.Do(func() { close(drain) })
			return commitFile(name, "package x\n")(ctx, s)
		}
	}
	opts := parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": drained("a.go"),
		"#3": drained("b.go"),
	})
	opts.Drain = drain

	res := factory.Run(context.Background(), opts)

	if len(res.Failed) != 0 || len(res.Closed) != 2 {
		t.Fatalf("result = %+v, want #2 and #3 closed", res)
	}
	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "Ctrl-C") {
		t.Errorf("stopped = %v, want the drain", res.Stopped)
	}
	if issue(t, tr, 4).Closed {
		t.Error("#4 was started after the drain")
	}
}

func TestKillingTheRunDiscardsEveryTicketInFlight(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Add a", "Add b")...)
	ctx, kill := context.WithCancel(context.Background())
	defer kill()
	m := newMeeting(2)
	waitsForKill := func(ctx context.Context, s factory.Step) harness.Outcome {
		if m.arrive(s.Ticket.ID.Display) {
			kill()
		}
		<-ctx.Done()
		return failedStep("killed")
	}

	res := factory.Run(ctx, parallel(f, tr, map[string]func(context.Context, factory.Step) harness.Outcome{"#2": waitsForKill, "#3": waitsForKill}))

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "killed") {
		t.Fatalf("stopped = %v, want the kill", res.Stopped)
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed = %+v, want nothing held against the Tickets", res.Failed)
	}
	noWorkLeft(t, f)
}

// drainingOnComments is a Tracker that drains the Run while the Run sets
// up the Ticket it reads the comments of.
type drainingOnComments struct {
	*fake.Tracker
	drain chan struct{}
}

func (d *drainingOnComments) Comments(ctx context.Context, id tracker.ID) ([]tracker.Comment, error) {
	close(d.drain)
	return d.Tracker.Comments(ctx, id)
}

func TestAnErrorThatStopsTheRunOutranksADrainThatCameFirst(t *testing.T) {
	f := newFixture(t)
	tr := &drainingOnComments{Tracker: fake.New(ready, ticketsOf("Add a", "Add b")...), drain: make(chan struct{})}
	opts := parallel(f, nil, map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": func(context.Context, factory.Step) harness.Outcome {
			return harness.Outcome{Kind: harness.Failed, Class: harness.Auth, Message: "bad key"}
		},
	})
	opts.Tracker, opts.Drain = tr, tr.drain

	res := factory.Run(context.Background(), opts)

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "auth error") {
		t.Fatalf("stopped = %v, want the auth error: the Tickets in flight didn't finish", res.Stopped)
	}
}
