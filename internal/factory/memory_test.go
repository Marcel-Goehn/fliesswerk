package factory_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/factory"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/memory"
	"github.com/Marcel-Goehn/fliesswerk/internal/runlog"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

const gib = int64(1) << 30

// lineLog collects what a Run logs.
type lineLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineLog) add(line runlog.Line) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line.Text)
}

func (l *lineLog) has(part string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}

// memoryRun is a Run over the Tickets with memory admission on, and free
// bytes free throughout unless the test changes it.
type memoryRun struct {
	opts      factory.Options
	tr        *fake.Tracker
	log       *lineLog
	sandboxes *sandboxes
}

func newMemoryRun(f fixture, free int64, titles []string, step func(context.Context, factory.Step) harness.Outcome) *memoryRun {
	tr := fake.New(ready, ticketsOf(titles...)...)
	m := &memoryRun{tr: tr, log: &lineLog{}, sandboxes: &sandboxes{}}
	m.opts = options(f, tr, step)
	m.opts.MaxParallel = len(titles)
	m.opts.Sandboxes = m.sandboxes.create
	m.opts.FreeMemory = func(context.Context) (int64, error) { return free, nil }
	m.opts.MemoryPoll = 5 * time.Millisecond
	m.opts.Log = m.log.add
	return m
}

// oneAtATime commits a file per Ticket and counts how many Steps ran at once.
func oneAtATime(most *atomic.Int32) func(context.Context, factory.Step) harness.Outcome {
	var running atomic.Int32
	return func(ctx context.Context, s factory.Step) harness.Outcome {
		now := running.Add(1)
		defer running.Add(-1)
		for {
			seen := most.Load()
			if now <= seen || most.CompareAndSwap(seen, now) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		return commitFile("t"+s.Ticket.ID.Branch+".go", "package x\n")(ctx, s)
	}
}

func TestATicketThatDoesNotFitWaitsInTheSameIteration(t *testing.T) {
	f := newFixture(t)
	var most atomic.Int32
	// Enough for one Sandbox at the 2 GB estimate, not for two.
	m := newMemoryRun(f, 3*gib, []string{"Cart", "Totals"}, oneAtATime(&most))

	res := factory.Run(context.Background(), m.opts)

	if res.Stopped != nil || len(res.Failed) != 0 || len(res.Closed) != 2 {
		t.Fatalf("result = %+v, want both Tickets closed", res)
	}
	if res.Iterations != 1 {
		t.Errorf("Iterations = %d, want the wait inside one Iteration", res.Iterations)
	}
	if most.Load() != 1 {
		t.Errorf("%d Steps ran at once, want 1", most.Load())
	}
	if res.Memory == nil || res.Memory.Waits != 1 {
		t.Errorf("memory = %+v, want one wait", res.Memory)
	}
	if !m.log.has("waiting for memory") {
		t.Errorf("the log doesn't mention the wait: %q", m.log.lines)
	}
}

func TestTicketsThatFitRunAtOnce(t *testing.T) {
	f := newFixture(t)
	together := newMeeting(2)
	m := newMemoryRun(f, 16*gib, []string{"Cart", "Totals"}, byTicket(map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": meets(t, together, "cart.go"),
		"#3": meets(t, together, "totals.go"),
	}))

	res := factory.Run(context.Background(), m.opts)

	if len(res.Failed) != 0 || len(res.Closed) != 2 {
		t.Fatalf("result = %+v, want both Tickets closed", res)
	}
	if res.Memory == nil || res.Memory.Waits != 0 {
		t.Errorf("memory = %+v, want no waits", res.Memory)
	}
}

func TestOneSandboxStartsEvenWithoutTheMemoryForIt(t *testing.T) {
	f := newFixture(t)
	m := newMemoryRun(f, gib/2, []string{"Cart"}, commitFile("cart.go", "package cart\n"))

	res := factory.Run(context.Background(), m.opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want the Ticket closed", res)
	}
	if !m.log.has("at least one Sandbox always starts") {
		t.Errorf("no warning in the log: %q", m.log.lines)
	}
}

func TestAMeasuredPeakIsTheNextRunsEstimate(t *testing.T) {
	f := newFixture(t)
	first := newMemoryRun(f, 16*gib, []string{"Cart"}, commitFile("cart.go", "package cart\n"))
	first.sandboxes.use = func(string) int64 { return gib }

	res := factory.Run(context.Background(), first.opts)
	if res.Memory == nil || res.Memory.PeakEstimate != gib {
		t.Fatalf("first Run's memory = %+v, want the measured 1 GiB", res.Memory)
	}

	// 3 GiB holds two Sandboxes of 1 GiB, and wouldn't hold two of 2 GiB.
	together := newMeeting(2)
	second := newMemoryRun(f, 3*gib, []string{"Totals", "Page"}, byTicket(map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": meets(t, together, "totals.go"),
		"#3": meets(t, together, "page.go"),
	}))
	res = factory.Run(context.Background(), second.opts)

	if len(res.Failed) != 0 || len(res.Closed) != 2 {
		t.Fatalf("second Run = %+v, want both Tickets closed together", res)
	}
}

func TestAHigherPeakRaisesTheEstimateAndALowerOneDoesNot(t *testing.T) {
	f := newFixture(t)
	for _, use := range []int64{3 * gib, gib} {
		m := newMemoryRun(f, 16*gib, []string{"Cart"}, commitFile("cart.go", "package cart\n"))
		m.sandboxes.use = func(string) int64 { return use }

		res := factory.Run(context.Background(), m.opts)

		if res.Memory == nil || res.Memory.PeakEstimate != 3*gib {
			t.Fatalf("after a peak of %d, memory = %+v, want the highest, 3 GiB", use, res.Memory)
		}
		git(t, f.work, "branch", "-D", "fliesswerk/spec-1-spec-shopping-cart")
		git(t, f.remote, "branch", "-D", "fliesswerk/spec-1-spec-shopping-cart")
	}
}

func outOfMemory() harness.Outcome {
	return harness.Outcome{Kind: harness.Failed, Class: harness.OutOfMemory, Message: "killed"}
}

func TestAnOOMKilledStepRaisesTheEstimateAndIsRetriedOnce(t *testing.T) {
	f := newFixture(t)
	var attempts atomic.Int32
	commit := commitFile("cart.go", "package cart\n")
	m := newMemoryRun(f, 16*gib, []string{"Cart"}, func(ctx context.Context, s factory.Step) harness.Outcome {
		if attempts.Add(1) == 1 {
			return outOfMemory()
		}
		return commit(ctx, s)
	})

	res := factory.Run(context.Background(), m.opts)

	if len(res.Closed) != 1 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want the Ticket closed after its retry", res)
	}
	if res.Memory == nil || res.Memory.PeakEstimate <= 2*gib {
		t.Errorf("memory = %+v, want the estimate raised above 2 GiB", res.Memory)
	}
}

func TestASecondOOMHandsTheTicketBackWithPeakAndAvailableMemory(t *testing.T) {
	f := newFixture(t)
	var attempts atomic.Int32
	m := newMemoryRun(f, 8*gib, []string{"Cart"}, func(context.Context, factory.Step) harness.Outcome {
		attempts.Add(1)
		return outOfMemory()
	})
	m.sandboxes.use = func(string) int64 { return 3 * gib }

	res := factory.Run(context.Background(), m.opts)

	if attempts.Load() != 2 {
		t.Errorf("the Step ran %d times, want 2", attempts.Load())
	}
	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindOutOfMemory {
		t.Fatalf("failed = %+v, want one of kind %q", res.Failed, factory.KindOutOfMemory)
	}
	i := issue(t, m.tr, 2)
	if i.Closed || len(i.Comments) != 1 {
		t.Fatalf("issue = %+v, want it open with one comment", i)
	}
	for _, want := range []string{"3072 MB", "8192 MB"} {
		if !strings.Contains(i.Comments[0].Body, want) {
			t.Errorf("the comment lacks %q:\n%s", want, i.Comments[0].Body)
		}
	}
}

func TestAnOOMRetryWaitsUntilThereIsRoom(t *testing.T) {
	f := newFixture(t)
	var free atomic.Int64
	free.Store(16 * gib)
	var attempts atomic.Int32
	commit := commitFile("cart.go", "package cart\n")
	m := newMemoryRun(f, 0, []string{"Cart", "Totals"}, nil)
	m.opts.FreeMemory = func(context.Context) (int64, error) { return free.Load(), nil }
	holding := make(chan struct{})
	finish := make(chan struct{})
	m.opts.RunStep = byTicket(map[string]func(context.Context, factory.Step) harness.Outcome{
		"#2": func(ctx context.Context, s factory.Step) harness.Outcome {
			if attempts.Add(1) == 1 {
				<-holding
				free.Store(gib)
				go func() {
					for !m.log.has("waiting for memory") {
						time.Sleep(time.Millisecond)
					}
					close(finish)
				}()
				return outOfMemory()
			}
			return commit(ctx, s)
		},
		"#3": func(ctx context.Context, s factory.Step) harness.Outcome {
			close(holding)
			<-finish
			return commitFile("totals.go", "package x\n")(ctx, s)
		},
	})

	res := factory.Run(context.Background(), m.opts)

	if len(res.Closed) != 2 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want both Tickets closed", res)
	}
	if attempts.Load() != 2 {
		t.Errorf("#2 ran %d times, want 2", attempts.Load())
	}
}

func TestCtrlCLeavesAWaitingTicketAsItWas(t *testing.T) {
	f := newFixture(t)
	drain := make(chan struct{})
	var started atomic.Int32
	commit := commitFile("cart.go", "package cart\n")
	m := newMemoryRun(f, 3*gib, []string{"Cart", "Totals"}, nil)
	m.opts.Drain = drain
	m.opts.RunStep = func(ctx context.Context, s factory.Step) harness.Outcome {
		started.Add(1)
		for !m.log.has("waiting for memory") {
			time.Sleep(time.Millisecond)
		}
		close(drain)
		time.Sleep(20 * time.Millisecond)
		return commit(ctx, s)
	}

	res := factory.Run(context.Background(), m.opts)

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "Ctrl-C") {
		t.Errorf("stopped = %v, want the drain", res.Stopped)
	}
	if started.Load() != 1 || len(res.Closed) != 1 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, started %d; want one Ticket closed and the other left", res, started.Load())
	}
	waiting := 2
	if res.Closed[0].ID.Display == "#2" {
		waiting = 3
	}
	if i := issue(t, m.tr, waiting); i.Closed || len(i.Comments) != 0 {
		t.Errorf("the waiting Ticket = %+v, want it untouched", i)
	}
	noWorkLeft(t, f)
}

func TestCtrlCLeavesATicketThatHasNotGotItsSandboxYet(t *testing.T) {
	f := newFixture(t)
	drain := make(chan struct{})
	m := newMemoryRun(f, 16*gib, []string{"Cart", "Totals"}, nil)
	var once sync.Once
	create := m.sandboxes.create
	m.opts.Drain = drain
	m.opts.Sandboxes = func(ctx context.Context, req sandbox.Request) (sandbox.Sandbox, error) {
		once.Do(func() { close(drain) })
		return create(ctx, req)
	}
	m.opts.RunStep = func(ctx context.Context, s factory.Step) harness.Outcome {
		return commitFile("t"+s.Ticket.ID.Branch+".go", "package x\n")(ctx, s)
	}

	res := factory.Run(context.Background(), m.opts)

	if got := len(m.sandboxes.names()); got != 1 {
		t.Errorf("%d Sandboxes started after Ctrl-C's first, want only the one in flight", got)
	}
	if len(res.Closed) != 1 || len(res.Failed) != 0 {
		t.Errorf("result = %+v, want one Ticket closed and the other left", res)
	}
}

// tinyPeak is what a Sandbox whose Harness never started peaks at.
const tinyPeak = int64(4) << 20

func noSession() harness.Outcome {
	return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "exited with code 1 before the end of the session", NoSession: true}
}

func TestStepsThatNeverStartTheirSessionLeaveTheEstimate(t *testing.T) {
	f := newFixture(t)
	m := newMemoryRun(f, 16*gib, []string{"Cart"}, func(context.Context, factory.Step) harness.Outcome { return noSession() })
	m.sandboxes.use = func(string) int64 { return tinyPeak }

	res := factory.Run(context.Background(), m.opts)

	if len(res.Failed) != 1 {
		t.Fatalf("result = %+v, want the Ticket failed", res)
	}
	if res.Memory == nil || res.Memory.PeakEstimate != memory.DefaultEstimate {
		t.Errorf("memory = %+v, want the default estimate", res.Memory)
	}
	if m.log.has("Sandbox peak") {
		t.Errorf("a peak was logged for a Sandbox nothing ran in: %q", m.log.lines)
	}
	if stored, err := memory.Load(filepath.Join(m.opts.Root, factory.MemoryFile)); err != nil || stored.Measured() {
		t.Errorf("the stored estimate is measured (err %v), want it left at the default", err)
	}
}

func TestAStepThatFailsInItsSessionCountsItsPeak(t *testing.T) {
	f := newFixture(t)
	m := newMemoryRun(f, 16*gib, []string{"Cart"}, func(context.Context, factory.Step) harness.Outcome {
		return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "boom"}
	})
	m.sandboxes.use = func(string) int64 { return gib }

	res := factory.Run(context.Background(), m.opts)

	if len(res.Failed) != 1 {
		t.Fatalf("result = %+v, want the Ticket failed", res)
	}
	if res.Memory == nil || res.Memory.PeakEstimate != gib {
		t.Errorf("memory = %+v, want the measured 1 GiB", res.Memory)
	}
	if !m.log.has("Sandbox peak 1024 MB") {
		t.Errorf("no peak in the log: %q", m.log.lines)
	}
}

func TestAFreshRetryThatStartsItsSessionCountsItsPeak(t *testing.T) {
	f := newFixture(t)
	var attempts atomic.Int32
	commit := commitFile("cart.go", "package cart\n")
	m := newMemoryRun(f, 16*gib, []string{"Cart"}, func(ctx context.Context, s factory.Step) harness.Outcome {
		if attempts.Add(1) == 1 {
			return noSession()
		}
		return commit(ctx, s)
	})
	m.sandboxes.use = func(string) int64 { return gib }

	res := factory.Run(context.Background(), m.opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want the Ticket closed after its retry", res)
	}
	if res.Memory == nil || res.Memory.PeakEstimate != gib {
		t.Errorf("memory = %+v, want the measured 1 GiB", res.Memory)
	}
}

func TestVerifyCountsItsSandboxsPeak(t *testing.T) {
	f := newFixture(t)
	commit := commitFile("cart.go", "package cart\n")
	// The Step's work is done but counts for nothing, so that only verify
	// does.
	m := newMemoryRun(f, 16*gib, []string{"Cart"}, func(ctx context.Context, s factory.Step) harness.Outcome {
		out := commit(ctx, s)
		out.NoSession = true
		return out
	})
	m.opts.Verify = []verify.Command{{Name: "test", Line: "exit 0"}}
	m.sandboxes.use = func(string) int64 { return gib }

	res := factory.Run(context.Background(), m.opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want the Ticket closed", res)
	}
	if res.Memory == nil || res.Memory.PeakEstimate != gib {
		t.Errorf("memory = %+v, want the measured 1 GiB", res.Memory)
	}
}
