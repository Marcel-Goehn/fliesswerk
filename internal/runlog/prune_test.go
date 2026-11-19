package runlog_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/runlog"
)

func logsWith(t *testing.T, names ...string) (root, logs string) {
	t.Helper()
	root = t.TempDir()
	logs = filepath.Join(root, ".fliesswerk", "logs")
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(logs, n), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return root, logs
}

func left(t *testing.T, logs string) []string {
	t.Helper()
	entries, err := os.ReadDir(logs)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPruningRemovesOnlyRunsOlderThanKeepDays(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.Local)
	root, logs := logsWith(t,
		"2026-10-08T11-59-59",   // 31 days and a moment: too old
		"2026-10-08T11-59-59-2", // the second Run of that second
		"2026-10-09T12-00-01",   // just inside the 30 days
		"2026-11-08T11-00-00",
		"my-notes",             // not a Run's: never touched
		"2020-01-01T00-00-00x", // nor this
	)

	if err := runlog.Prune(root, 30, now); err != nil {
		t.Fatal(err)
	}

	want := []string{"2020-01-01T00-00-00x", "2026-10-09T12-00-01", "2026-11-08T11-00-00", "my-notes"}
	if got := left(t, logs); !slices.Equal(got, want) {
		t.Errorf("left %v, want %v", got, want)
	}
}

func TestKeepingForeverPrunesNothing(t *testing.T) {
	root, logs := logsWith(t, "2001-01-01T00-00-00")

	if err := runlog.Prune(root, runlog.Forever, time.Now()); err != nil {
		t.Fatal(err)
	}

	if got := left(t, logs); len(got) != 1 {
		t.Errorf("left %v, want the old Run kept", got)
	}
}

func TestPruningAProjectWithoutLogsIsFine(t *testing.T) {
	if err := runlog.Prune(t.TempDir(), 30, time.Now()); err != nil {
		t.Error(err)
	}
}

func TestLatestIsTheNewestRunDirectory(t *testing.T) {
	root, logs := logsWith(t, "2026-11-07T14-03-12", "2026-11-07T14-03-12-2", "2026-11-01T00-00-00", "zzz-notes")

	got, ok := runlog.Latest(root)

	if want := filepath.Join(logs, "2026-11-07T14-03-12-2"); !ok || got != want {
		t.Errorf("Latest = %q, %v; want %q", got, ok, want)
	}
}

func TestLatestWithoutRuns(t *testing.T) {
	if got, ok := runlog.Latest(t.TempDir()); ok {
		t.Errorf("Latest = %q, want none", got)
	}
}
