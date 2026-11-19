package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// The Tracker writes a Run can queue.
const (
	opComment = "comment"
	opClose   = "close"
	opLabels  = "labels"
	opClaim   = "claim"
	opRelease = "release"
)

const (
	// writeRetries is how often a failed write is tried again before it is
	// queued.
	writeRetries = 3
	// maxWriteWait is the longest a Run waits for a rate limit to pass. A
	// write that must wait longer is queued at once.
	maxWriteWait = 5 * time.Minute
	// writeTimeout bounds one attempt at a write.
	writeTimeout = time.Minute
)

// DefaultWriteBackoff waits 5s before a failed write's first retry, and
// twice as long before each next one.
func DefaultWriteBackoff(retry int) time.Duration {
	return 5 * time.Second << (retry - 1)
}

// queuedWrite is one Tracker write, as the replay queue stores it.
type queuedWrite struct {
	Op     string     `json:"op"`
	ID     tracker.ID `json:"id"`
	Text   string     `json:"text,omitempty"`
	Add    []string   `json:"add,omitempty"`
	Remove []string   `json:"remove,omitempty"`
	By     string     `json:"by,omitempty"`
}

func (w queuedWrite) String() string { return w.Op + " " + w.ID.Display }

// holdsTicket reports whether the write records how a Ticket's work
// ended, so that the Ticket must not be worked again until it lands.
func (w queuedWrite) holdsTicket() bool {
	return w.Op == opClose || w.Op == opComment || w.Op == opLabels
}

// durableTracker makes the Tracker's writes survive failure: each is kept
// in a replay queue in the runtime state until it has landed, and retried
// with backoff. One that still fails stays for the next Run. To its caller
// a queued write has succeeded.
type durableTracker struct {
	tracker.Tracker
	// path is the replay queue's file.
	path    string
	backoff func(retry int) time.Duration
	redact  func(string) string
	warnf   func(format string, a ...any)
	logf    func(format string, a ...any)

	mu    sync.Mutex
	queue []*queuedWrite
}

func (d *durableTracker) Comment(ctx context.Context, ticket tracker.ID, text string) error {
	return d.write(ctx, queuedWrite{Op: opComment, ID: ticket, Text: text})
}

func (d *durableTracker) Close(ctx context.Context, ticket tracker.ID, text string) error {
	return d.write(ctx, queuedWrite{Op: opClose, ID: ticket, Text: text})
}

func (d *durableTracker) SetLabels(ctx context.Context, ticket tracker.ID, add, remove []string) error {
	return d.write(ctx, queuedWrite{Op: opLabels, ID: ticket, Add: add, Remove: remove})
}

func (d *durableTracker) Claim(ctx context.Context, id tracker.ID) error {
	return d.write(ctx, queuedWrite{Op: opClaim, ID: id})
}

func (d *durableTracker) Release(ctx context.Context, id tracker.ID, by string) error {
	return d.write(ctx, queuedWrite{Op: opRelease, ID: id, By: by})
}

// write sends w, which stays in the queue on disk until it has landed, so
// that not even a crash during its retries loses it. A write that keeps
// failing simply stays. If not even the queue can keep it, the caller
// gets the error.
func (d *durableTracker) write(ctx context.Context, w queuedWrite) error {
	w.Text = d.redact(w.Text) // the queue is on disk
	queued, journalErr := d.enqueue(w)
	err := d.attempt(ctx, w)
	if err == nil {
		if derr := d.dequeue(queued); derr != nil {
			d.warnf("warning: %s landed, but it can't leave the replay queue: %v", w, derr)
		}
		return nil
	}
	if journalErr != nil {
		return errors.Join(err, journalErr)
	}
	d.warnf("warning: %s failed, and is queued for the next Run to replay: %v", w, err)
	return nil
}

// enqueue adds w to the queue. A release drops the queued claims of its
// issue: replayed later, they would claim it again.
func (d *durableTracker) enqueue(w queuedWrite) (*queuedWrite, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w.Op == opRelease {
		d.queue = slices.DeleteFunc(d.queue, func(q *queuedWrite) bool { return q.Op == opClaim && q.ID.Key == w.ID.Key })
	}
	d.queue = append(d.queue, &w)
	return &w, d.save()
}

// dequeue takes a write that landed out of the queue.
func (d *durableTracker) dequeue(w *queuedWrite) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queue = slices.DeleteFunc(d.queue, func(q *queuedWrite) bool { return q == w })
	return d.save()
}

// attempt tries w, and retries it with backoff, waiting at least as long
// as a rate limit asks. A kill of the Run ends the waiting.
func (d *durableTracker) attempt(ctx context.Context, w queuedWrite) error {
	for retry := 1; ; retry++ {
		err := d.apply(ctx, w)
		if err == nil || retry > writeRetries {
			return err
		}
		wait := d.backoff(retry)
		var limited *tracker.RateLimitError
		if errors.As(err, &limited) {
			if limited.Wait > maxWriteWait {
				return err
			}
			wait = max(wait, limited.Wait)
		}
		if sleep(ctx, wait) != nil {
			return err
		}
	}
}

// apply sends w once. A write that has begun isn't killed with the Run:
// half of one is worse than a moment's wait.
func (d *durableTracker) apply(ctx context.Context, w queuedWrite) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	switch w.Op {
	case opComment:
		return d.Tracker.Comment(ctx, w.ID, w.Text)
	case opClose:
		return d.Tracker.Close(ctx, w.ID, w.Text)
	case opLabels:
		return d.Tracker.SetLabels(ctx, w.ID, w.Add, w.Remove)
	case opClaim:
		return d.Tracker.Claim(ctx, w.ID)
	case opRelease:
		return d.Tracker.Release(ctx, w.ID, w.By)
	}
	return fmt.Errorf("unknown write %q in the replay queue", w.Op)
}

// replay sends every write an earlier Run queued, once each and in order.
// Those that fail again stay queued.
func (d *durableTracker) replay(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	data, err := os.ReadFile(d.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the replay queue: %w", err)
	}
	if err := json.Unmarshal(data, &d.queue); err != nil {
		return fmt.Errorf("the replay queue %s can't be read; fix or delete it: %w", d.path, err)
	}
	for i := 0; i < len(d.queue) && ctx.Err() == nil; {
		w := *d.queue[i]
		if err := d.apply(ctx, w); err != nil {
			d.warnf("warning: replaying %s failed, and it stays queued: %v", w, err)
			i++
			continue
		}
		d.queue = slices.Delete(d.queue, i, i+1)
		if err := d.save(); err != nil {
			return err
		}
		d.logf("Replayed %s, which an earlier Run couldn't write.", w)
	}
	return nil
}

// save writes the queue atomically, or removes its file when it is empty.
func (d *durableTracker) save() error {
	if len(d.queue) == 0 {
		if err := os.Remove(d.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(d.queue, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o750); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// pending is what is queued.
func (d *durableTracker) pending() []queuedWrite {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]queuedWrite, len(d.queue))
	for i, w := range d.queue {
		out[i] = *w
	}
	return out
}
