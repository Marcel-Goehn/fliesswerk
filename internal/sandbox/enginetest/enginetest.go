// Package enginetest is how the integration tests find the container engine
// to run on: `sandbox.engine` in this repo's own .fliesswerk/config.local.toml,
// as a Run finds it in a project's. CI writes that file for each engine.
// Nothing that ships imports it.
package enginetest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
)

// Lookup is the configured engine, docker or podman, or why there is none.
func Lookup() (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, filepath.FromSlash(config.LocalFile))
	data, err := os.ReadFile(path) //nolint:gosec // the repo's own test config
	if err != nil {
		return "", fmt.Errorf("the integration tests run on the engine set in %s; write `[sandbox]` and `engine = \"docker\"` (or \"podman\") there: %w", path, err)
	}
	var c struct {
		Sandbox struct{ Engine string } `toml:"sandbox"`
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if c.Sandbox.Engine == "" {
		return "", fmt.Errorf("%s sets no `engine` under [sandbox]; set it to docker or podman", path)
	}
	return c.Sandbox.Engine, nil
}

// Engine is the configured engine; without one the test fails.
func Engine(t testing.TB) string {
	t.Helper()
	engine, err := Lookup()
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// repoRoot is the nearest directory at or above the current one with a go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the current directory")
		}
		dir = parent
	}
}
