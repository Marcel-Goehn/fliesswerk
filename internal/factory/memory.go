package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/memory"
	"github.com/Marcel-Goehn/fliesswerk/internal/runlog"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// DefaultMemoryPoll is how often memory is looked at while a Sandbox waits,
// and each running Sandbox is measured.
const DefaultMemoryPoll = 5 * time.Second

// memoryReadTimeout bounds one look at a Sandbox's memory.
const memoryReadTimeout = 30 * time.Second

// MemoryFile is the memory estimate, relative to the project root.
var MemoryFile = filepath.Join(StateDir, "memory.json")

// MemoryReport is what the Run's memory admission saw.
type MemoryReport struct {
	// PeakEstimate is the estimate for one Sandbox at the end of the Run, in
	// bytes.
	PeakEstimate int64
	// Waits is how often a Sandbox had to wait for memory.
	Waits int
}

// admission decides when a Sandbox may start. Holding turn is the right to
// start one, so that Sandboxes start one after another. Each is measured while
// it lives, for the estimate of those after it.
type admission struct {
	r     *run
	store *memory.Store
	free  func(context.Context) (int64, error)
	poll  time.Duration
	turn  chan struct{}

	mu     sync.Mutex
	meters map[sandbox.Sandbox]*meter
	waits  int
	warned map[string]bool
}

// meter is what is known about one running Sandbox's memory. Its fields are
// guarded by the admission's mu.
type meter struct {
	sb sandbox.Sandbox
	// now is the last reading, and peak the highest.
	now, peak int64
	// worked says a Step or verify ran in it: an idle Sandbox's peak says
	// nothing about what one needs.
	worked bool
	// parked says it is waiting for its turn, and so can't free memory or
	// make room for the one holding the turn.
	parked bool
	stop   chan struct{}
	done   chan struct{}
}

// newAdmission is the Run's admission, nil if memory isn't being watched.
func newAdmission(r *run, opts Options) *admission {
	if opts.FreeMemory == nil {
		return nil
	}
	store, err := memory.Load(filepath.Join(opts.Root, MemoryFile))
	if err != nil {
		r.warnf("warning: starting from the default memory estimate: %v", err)
	}
	poll := opts.MemoryPoll
	if poll == 0 {
		poll = DefaultMemoryPoll
	}
	return &admission{
		r: r, store: store, free: opts.FreeMemory, poll: poll, turn: make(chan struct{}, 1),
		meters: map[sandbox.Sandbox]*meter{}, warned: map[string]bool{},
	}
}

// warnOnce warns about what would otherwise repeat at every look.
func (a *admission) warnOnce(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	a.mu.Lock()
	seen := a.warned[format]
	a.warned[format] = true
	a.mu.Unlock()
	if !seen {
		a.r.warnf("%s", text)
	}
}

// announce logs what the Run starts from.
func (a *admission) announce() {
	how := "measured"
	if !a.store.Measured() {
		how = "not measured yet"
	}
	a.r.logf("Memory: peak estimate %d MB per Sandbox (%s), plus a margin of %d MB", memory.MB(a.store.Estimate()), how, memory.MB(memory.Margin))
}

// look is what deciding on a Sandbox needs to know right now.
type look struct {
	free, reserved, estimate, self int64
	others                         int
}

// lookNow reads the free memory; known is false if it can't be read.
func (a *admission) lookNow(ctx context.Context, self *meter) (l look, known bool) {
	free, err := a.free(ctx)
	if err != nil {
		if ctx.Err() == nil {
			a.warnOnce("warning: can't read the free memory, so Sandboxes start without waiting for it: %v", err)
		}
		return look{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	l = look{free: free, estimate: a.store.Estimate()}
	for _, m := range a.meters {
		l.estimate = max(l.estimate, m.peak)
	}
	for _, m := range a.meters {
		if m == self {
			l.self = m.now
			continue
		}
		if !m.parked {
			l.others++
			l.reserved += memory.Reserved(l.estimate, m.now)
		}
	}
	return l, true
}

// admit waits for the turn and then for the memory for one more Sandbox, or
// for room for self to retry in. What it returns ends the turn. It fails only
// when ctx ends, or with errDrained when drain closes while it waits: a Ticket
// still waiting hasn't started, so Ctrl-C leaves it as it was.
func (a *admission) admit(ctx context.Context, tk tracker.Ticket, self *meter, drain <-chan struct{}) (func(), error) {
	a.park(self, true)
	defer a.park(self, false)
	select {
	case a.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-drain:
		return nil, errDrained
	}
	release := sync.OnceFunc(func() { <-a.turn })
	select {
	case <-drain:
		release()
		return nil, errDrained
	default:
	}
	waited := false
	for {
		l, known := a.lookNow(ctx, self)
		if !known || memory.Fits(l.free, l.reserved, l.estimate, l.self) {
			return release, nil
		}
		need := memory.Needed(l.estimate, l.self)
		if l.others == 0 {
			a.r.line(tk, runlog.Line{Kind: runlog.Warning, Text: fmt.Sprintf(
				"warning: only %d MB of memory is free and a Sandbox may need %d MB; starting anyway, as at least one Sandbox always starts", memory.MB(l.free), memory.MB(need))})
			return release, nil
		}
		if !waited {
			waited = true
			a.mu.Lock()
			a.waits++
			a.mu.Unlock()
			a.r.tlogf(tk, "waiting for memory: a Sandbox needs %d MB, %d MB is free and %d MB of that is held for the %d running", memory.MB(need), memory.MB(l.free), memory.MB(l.reserved), l.others)
		}
		select {
		case <-time.After(a.poll):
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-drain:
			release()
			return nil, errDrained
		}
	}
}

// park says whether m is waiting for its turn.
func (a *admission) park(m *meter, parked bool) {
	if m == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m.parked = parked
}

// watch measures sb until finish.
func (a *admission) watch(ctx context.Context, sb sandbox.Sandbox) *meter {
	m := &meter{sb: sb, stop: make(chan struct{}), done: make(chan struct{})}
	a.mu.Lock()
	a.meters[sb] = m
	a.mu.Unlock()
	go func() {
		defer close(m.done)
		for {
			a.measure(ctx, m)
			select {
			case <-time.After(a.poll):
			case <-m.stop:
				return
			}
		}
	}()
	return m
}

// measure takes one reading, also of a Sandbox whose Run is being killed.
func (a *admission) measure(ctx context.Context, m *meter) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), memoryReadTimeout)
	defer cancel()
	use, err := m.sb.MemoryUse(ctx)
	if err != nil {
		a.warnOnce("warning: can't measure a Sandbox's memory, so its peak may be missed: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m.now, m.peak = use, max(m.peak, use)
}

// finish stops measuring a Sandbox about to go, and adds its peak to the
// estimate.
func (a *admission) finish(ctx context.Context, tk tracker.Ticket, m *meter) {
	close(m.stop)
	<-m.done
	a.measure(ctx, m)
	a.mu.Lock()
	delete(a.meters, m.sb)
	peak, worked := m.peak, m.worked
	a.mu.Unlock()
	if !worked || peak == 0 {
		return
	}
	if err := a.store.Observe(peak); err != nil {
		a.warnOnce("warning: can't save the memory estimate: %v", err)
	}
	a.r.tlogf(tk, "Sandbox peak %d MB; the estimate is %d MB", memory.MB(peak), memory.MB(a.store.Estimate()))
}

// noteWork says something real ran in the Sandbox. A nil admission or Sandbox
// has nothing to note.
func (a *admission) noteWork(sb sandbox.Sandbox) {
	if a == nil || sb == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if m := a.meters[sb]; m != nil {
		m.worked = true
	}
}

// outOfMemory raises the estimate after an OOM kill in sb, and says what the
// failure comment should about the memory.
func (a *admission) outOfMemory(ctx context.Context, tk tracker.Ticket, sb sandbox.Sandbox) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	var peak int64
	if m := a.meters[sb]; m != nil {
		peak = m.peak
	}
	a.mu.Unlock()
	raised, err := a.store.Raise(peak)
	if err != nil {
		a.warnOnce("warning: can't save the memory estimate: %v", err)
	}
	a.r.tlogf(tk, "the memory estimate is raised to %d MB", memory.MB(raised))
	if free, err := a.free(ctx); err == nil {
		return fmt.Sprintf(" (the Sandbox peaked at %d MB, with %d MB available)", memory.MB(peak), memory.MB(free))
	}
	return fmt.Sprintf(" (the Sandbox peaked at %d MB)", memory.MB(peak))
}

// roomToRetry waits until a Step killed for lack of memory in sb has room to
// run again.
func (a *admission) roomToRetry(ctx context.Context, tk tracker.Ticket, sb sandbox.Sandbox) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	self := a.meters[sb]
	a.mu.Unlock()
	release, err := a.admit(ctx, tk, self, nil)
	if err != nil {
		return err
	}
	release()
	return nil
}

// report is the Run's memory, as the summary and the next `check` show it.
func (a *admission) report() *MemoryReport {
	a.mu.Lock()
	waits := a.waits
	a.mu.Unlock()
	if err := a.store.SetLastRunWaits(waits); err != nil {
		a.r.warnf("warning: can't save the memory estimate: %v", err)
	}
	return &MemoryReport{PeakEstimate: a.store.Estimate(), Waits: waits}
}
