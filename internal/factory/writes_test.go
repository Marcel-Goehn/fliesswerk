package factory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

// failingCloses is a Tracker whose next closes fail with the errors in
// errs.
type failingCloses struct {
	*fake.Tracker
	errs []error
}

func (f *failingCloses) Close(ctx context.Context, id tracker.ID, text string) error {
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return f.Tracker.Close(ctx, id, text)
}

func runWithCloses(t *testing.T, errs ...error) (*fake.Tracker, factory.Result) {
	t.Helper()
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	opts := options(f, &failingCloses{Tracker: tr, errs: errs}, commits(map[string]string{"cart.go": "package cart\n"}, "Done."))
	opts.WriteBackoff = func(int) time.Duration { return time.Millisecond }
	return tr, factory.Run(context.Background(), opts)
}

func TestAFailedWriteIsRetriedWithBackoff(t *testing.T) {
	boom := errors.New("HTTP 502")
	tr, res := runWithCloses(t, boom, boom, boom)

	if len(res.Closed) != 1 || len(res.QueuedWrites) != 0 {
		t.Fatalf("closed = %v, queued = %v, want #2 closed on the third retry", res.Closed, res.QueuedWrites)
	}
	if !tr.Issues()[1].Closed {
		t.Error("#2 is still open")
	}
}

func TestAWriteThatKeepsFailingIsQueued(t *testing.T) {
	boom := errors.New("HTTP 502")
	tr, res := runWithCloses(t, boom, boom, boom, boom)

	if len(res.QueuedWrites) != 1 || res.QueuedWrites[0] != "close #2" {
		t.Fatalf("queued = %v, want the close of #2", res.QueuedWrites)
	}
	if tr.Issues()[1].Closed {
		t.Error("#2 was closed")
	}
}

func TestARetryWaitsAsLongAsTheRateLimitAsks(t *testing.T) {
	const asked = 150 * time.Millisecond
	begin := time.Now()

	_, res := runWithCloses(t, &tracker.RateLimitError{Wait: asked, Err: errors.New("rate limited")})

	if len(res.Closed) != 1 {
		t.Fatalf("closed = %v, want #2 closed after the wait", res.Closed)
	}
	if took := time.Since(begin); took < asked {
		t.Errorf("the Run took %s, less than the %s the Tracker asked for", took, asked)
	}
}

func TestARateLimitTooLongToWaitForQueuesTheWriteAtOnce(t *testing.T) {
	limited := &tracker.RateLimitError{Wait: time.Hour, Err: errors.New("rate limited")}

	_, res := runWithCloses(t, limited)

	if len(res.QueuedWrites) != 1 {
		t.Fatalf("queued = %v, want the close queued rather than waited for", res.QueuedWrites)
	}
}

// journalReadingCloses records what the replay queue held while a close
// was under way.
type journalReadingCloses struct {
	*fake.Tracker
	path string
	seen string
}

func (j *journalReadingCloses) Close(ctx context.Context, id tracker.ID, text string) error {
	data, _ := os.ReadFile(j.path) //nolint:gosec // a path in the test's own checkout
	j.seen = string(data)
	return j.Tracker.Close(ctx, id, text)
}

func TestAWriteIsOnDiskWhileItIsTriedSoACrashCannotLoseIt(t *testing.T) {
	f := newFixture(t)
	queue := filepath.Join(f.work, factory.StateDir, "replay.json")
	tr := &journalReadingCloses{Tracker: fake.New(ready, cartSpec()...), path: queue}

	res := factory.Run(context.Background(), options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Done.")))

	if !strings.Contains(tr.seen, `"close"`) {
		t.Errorf("the replay queue held %q during the close, want the close in it", tr.seen)
	}
	if len(res.QueuedWrites) != 0 || exists(queue) {
		t.Errorf("queued = %v: a write that landed must leave the queue", res.QueuedWrites)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// killingCloses fails every close, and kills the Run at the first.
type killingCloses struct {
	*fake.Tracker
	kill context.CancelFunc
}

func (k *killingCloses) Close(context.Context, tracker.ID, string) error {
	k.kill()
	return errors.New("HTTP 502")
}

func TestAKillEndsTheWaitForAWritesRetryAndQueuesIt(t *testing.T) {
	f := newFixture(t)
	ctx, kill := context.WithCancel(context.Background())
	defer kill()
	tr := &killingCloses{Tracker: fake.New(ready, cartSpec()...), kill: kill}
	opts := options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Done."))
	opts.WriteBackoff = func(int) time.Duration { return time.Hour }

	ended := make(chan factory.Result, 1)
	go func() { ended <- factory.Run(ctx, opts) }()

	select {
	case res := <-ended:
		if len(res.QueuedWrites) != 1 || res.QueuedWrites[0] != "close #2" {
			t.Errorf("queued = %v, want the close of #2", res.QueuedWrites)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the Run is still waiting to retry the write after the kill")
	}
}

// failingClaims is a Tracker whose every claim fails.
type failingClaims struct{ *fake.Tracker }

func (failingClaims) Claim(context.Context, tracker.ID) error { return errors.New("HTTP 502") }

func TestAQueuedClaimIsDroppedOnceItsSpecIsReleased(t *testing.T) {
	f := newFixture(t)
	tr := failingClaims{fake.New(ready, cartSpec()...)}
	opts := options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Done."))
	opts.WriteBackoff = func(int) time.Duration { return time.Millisecond }

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || len(res.QueuedWrites) != 0 {
		t.Errorf("closed = %v, queued = %v: replaying the claim would claim a finished Spec", res.Closed, res.QueuedWrites)
	}
}

func TestAPushThatFailsWithoutBeingRejectedDoesNotStopTheSpec(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	implement := commits(map[string]string{"cart.go": "package cart\n"}, "Done.")
	runStep := func(ctx context.Context, s factory.Step) harness.Outcome {
		if err := os.Rename(f.remote, f.remote+".gone"); err != nil { // the connection is lost
			t.Error(err)
		}
		return implement(ctx, s)
	}

	res := factory.Run(context.Background(), options(f, tr, runStep))

	if len(res.StoppedSpecs) != 0 {
		t.Errorf("stopped specs = %+v, want no rejected push reported", res.StoppedSpecs)
	}
	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindFactory {
		t.Fatalf("failed = %+v, want #2 failed by the Factory", res.Failed)
	}
	if w := ticketWrites(tr); len(w) != 0 {
		t.Errorf("tracker writes = %+v, want #2 left open", w)
	}
}
