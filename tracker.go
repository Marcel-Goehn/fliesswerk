package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/github"
)

// Defaults for keys the config may leave out.
const (
	defaultReadyLabel  = "ready-for-agent"
	defaultMaxParallel = 1
)

// testTracker, when set and returning a Tracker, replaces the configured
// one. Only a test build (the `kwkfake` tag) sets it.
var testTracker func() (tracker.Tracker, error)

// newTracker builds the Tracker the config names. The config has already
// been validated, so every value has the right type.
func newTracker(values map[string]any) (tracker.Tracker, error) {
	if testTracker != nil {
		if tr, err := testTracker(); tr != nil || err != nil {
			return tr, err
		}
	}
	token, err := trackerToken(values)
	if err != nil {
		return nil, err
	}
	// "github" is the only kind, and the default.
	return github.New(github.Options{
		Repo:       stringAt(values, "", "tracker", "repo"),
		ReadyLabel: readyLabel(values),
		Runner:     github.ExecRunner{Token: token},
		GOOS:       runtime.GOOS,
	}), nil
}

func readyLabel(values map[string]any) string {
	return stringAt(values, defaultReadyLabel, "tracker", "ready_label")
}

func maxParallel(values map[string]any) int {
	return intAt(values, defaultMaxParallel, "max_parallel")
}

// trackerToken resolves `tracker.token`, empty when it isn't set.
func trackerToken(values map[string]any) (string, error) {
	ref, ok := valueAt(values, "tracker", "token").(map[string]any)
	if !ok {
		return "", nil
	}
	if name, ok := ref["env"].(string); ok {
		v := os.Getenv(name)
		if v == "" {
			return "", fmt.Errorf("tracker.token: the environment variable %s is not set", name)
		}
		return v, nil
	}
	for source := range ref {
		return "", fmt.Errorf("tracker.token: the %q source isn't supported by this kraftwerk yet; use { env = \"NAME\" }, or leave tracker.token out to use your gh login", source)
	}
	return "", nil
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

func intAt(values map[string]any, def int, path ...string) int {
	if v, ok := valueAt(values, path...).(int64); ok {
		return int(v)
	}
	return def
}

func boolAt(values map[string]any, def bool, path ...string) bool {
	if v, ok := valueAt(values, path...).(bool); ok {
		return v
	}
	return def
}
