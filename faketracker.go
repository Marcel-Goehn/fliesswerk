//go:build kwkfake

package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

// In a test build, $KWK_FAKE_TRACKER names a JSON file that holds a fake
// Tracker's issues, used instead of the configured Tracker:
//
//	{"ready_label": "ready-for-agent", "issues": [{"Number": 1, …}], "writes": []}
//
// Every write is applied to the file, and recorded in "writes".
func init() {
	testTracker = func() (tracker.Tracker, error) {
		path := os.Getenv("KWK_FAKE_TRACKER")
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
		return &fileTracker{Tracker: fake.New(st.ReadyLabel, st.Issues...), path: path, readyLabel: st.ReadyLabel}, nil
	}
}

type fakeState struct {
	ReadyLabel string       `json:"ready_label"`
	Issues     []fake.Issue `json:"issues"`
	Writes     []fake.Write `json:"writes"`
}

// fileTracker is a fake Tracker that saves itself after every write.
type fileTracker struct {
	*fake.Tracker
	path       string
	readyLabel string
}

func (f *fileTracker) save(err error) error {
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(fakeState{ReadyLabel: f.readyLabel, Issues: f.Issues(), Writes: f.Writes()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0o600)
}

func (f *fileTracker) Comment(ctx context.Context, ticket tracker.ID, text string) error {
	return f.save(f.Tracker.Comment(ctx, ticket, text))
}

func (f *fileTracker) Close(ctx context.Context, ticket tracker.ID, text string) error {
	return f.save(f.Tracker.Close(ctx, ticket, text))
}

func (f *fileTracker) SetLabels(ctx context.Context, ticket tracker.ID, add, remove []string) error {
	return f.save(f.Tracker.SetLabels(ctx, ticket, add, remove))
}
