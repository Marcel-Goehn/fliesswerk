package main

import (
	"runtime"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/github"
)

// Defaults for keys the config may leave out.
const (
	defaultReadyLabel      = "ready-for-agent"
	defaultInProgressLabel = "in-progress"
	defaultMaxParallel     = 1
)

// testTracker, when set and returning a Tracker, replaces the configured
// one. Only a test build (the `fwkfake` tag) sets it.
var testTracker func() (tracker.Tracker, error)

// newTracker builds the Tracker the config names, with token (empty for
// the Tracker's own login). The config has already been validated, so
// every value has the right type.
func newTracker(values map[string]any, token string) (tracker.Tracker, error) {
	if testTracker != nil {
		if tr, err := testTracker(); tr != nil || err != nil {
			return tr, err
		}
	}
	// "github" is the only kind, and the default.
	return github.New(github.Options{
		Repo:            stringAt(values, "", "tracker", "repo"),
		ReadyLabel:      readyLabel(values),
		InProgressLabel: stringAt(values, defaultInProgressLabel, "tracker", "in_progress_label"),
		Runner:          github.ExecRunner{Token: token},
		GOOS:            runtime.GOOS,
	}), nil
}

func readyLabel(values map[string]any) string {
	return stringAt(values, defaultReadyLabel, "tracker", "ready_label")
}

func maxParallel(values map[string]any) int {
	return intAt(values, defaultMaxParallel, "max_parallel")
}

// valueAt is the value at a key path in decoded TOML, nil if there is none.
func valueAt(values map[string]any, path ...string) any {
	var v any = values
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func stringAt(values map[string]any, def string, path ...string) string {
	if s, ok := valueAt(values, path...).(string); ok {
		return s
	}
	return def
}

// intAt is the whole number at a key path, def if there is none.
func intAt(values map[string]any, def int, path ...string) int {
	if v, ok := valueAt(values, path...).(int64); ok {
		return int(v)
	}
	return def
}

// boolAt is the bool at a key path, def if there is none.
func boolAt(values map[string]any, def bool, path ...string) bool {
	if v, ok := valueAt(values, path...).(bool); ok {
		return v
	}
	return def
}
