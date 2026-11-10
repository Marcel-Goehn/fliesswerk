//go:build fwkfake

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// In a test build, $FWK_FAKE_TRACKER names a JSON file that holds a fake
// Tracker's issues, used instead of the configured Tracker:
//
//	{"ready_label": "ready-for-agent", "in_progress_label": "in-progress", "user": "me",
//	 "issues": [{"Number": 1, …}], "writes": []}
//
// in_progress_label and user may be left out for the fake's defaults.
//
// Every write is applied to the file, and recorded in "writes". The next
// "fail_writes" writes fail instead, and with "fail_snapshot" so does
// every Snapshot.
func init() {
	testTracker = func() (tracker.Tracker, error) {
		path := os.Getenv("FWK_FAKE_TRACKER")
		if path == "" {
			return nil, nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // the test that runs the binary names the file
		if err != nil {
			return nil, err
		}
		var st fakeState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		tr := fake.New(st.ReadyLabel, st.Issues...)
		tr.InProgressLabel = cmp.Or(st.InProgressLabel, tr.InProgressLabel)
		tr.Me = cmp.Or(st.User, tr.Me)
		return &fileTracker{Tracker: tr, path: path, state: st}, nil
	}
}

type fakeState struct {
	ReadyLabel      string       `json:"ready_label"`
	InProgressLabel string       `json:"in_progress_label,omitempty"`
	User            string       `json:"user,omitempty"`
	Issues          []fake.Issue `json:"issues"`
	Writes          []fake.Write `json:"writes"`
	FailWrites      int          `json:"fail_writes,omitempty"`
	FailSnapshot    bool         `json:"fail_snapshot,omitempty"`
}

// fileTracker is a fake Tracker that saves itself after every write.
type fileTracker struct {
	*fake.Tracker
	path string
	// mu keeps the Tickets of an Iteration from writing the file at once.
	mu    sync.Mutex
	state fakeState
}

func (f *fileTracker) save(err error) error {
	if err != nil {
		return err
	}
	st := f.state
	st.Issues, st.Writes = f.Issues(), f.Writes()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0o600)
}

// write applies a write, unless it is one of those meant to fail.
func (f *fileTracker) write(apply func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state.FailWrites > 0 {
		f.state.FailWrites--
		return errors.Join(errors.New("fake tracker: the write failed"), f.save(nil))
	}
	return f.save(apply())
}

func (f *fileTracker) Snapshot(ctx context.Context) (tracker.Snapshot, error) {
	if f.state.FailSnapshot {
		return tracker.Snapshot{}, errors.New("fake tracker: the Snapshot failed")
	}
	return f.Tracker.Snapshot(ctx)
}

func (f *fileTracker) Comment(ctx context.Context, ticket tracker.ID, text string) error {
	return f.write(func() error { return f.Tracker.Comment(ctx, ticket, text) })
}

func (f *fileTracker) Close(ctx context.Context, ticket tracker.ID, text string) error {
	return f.write(func() error { return f.Tracker.Close(ctx, ticket, text) })
}

func (f *fileTracker) SetLabels(ctx context.Context, ticket tracker.ID, add, remove []string) error {
	return f.write(func() error { return f.Tracker.SetLabels(ctx, ticket, add, remove) })
}

func (f *fileTracker) Claim(ctx context.Context, id tracker.ID) error {
	return f.write(func() error { return f.Tracker.Claim(ctx, id) })
}

func (f *fileTracker) Release(ctx context.Context, id tracker.ID, by string) error {
	return f.write(func() error { return f.Tracker.Release(ctx, id, by) })
}
