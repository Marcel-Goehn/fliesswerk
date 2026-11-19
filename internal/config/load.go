// Package config loads the Factory's configuration from .fliesswerk/ and
// validates it strictly, before any token is spent.
//
// Three layers are merged key by key, later ones winning: config.toml (the
// team's, committed), config.local.toml (personal, git-ignored) and CLI
// flags. Tables merge key by key; any other value, lists included, is
// replaced as a whole.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// SupportedVersion is the newest config `version` this binary understands.
const SupportedVersion = 1

// Dir is the Factory's directory at the project root.
const Dir = ".fliesswerk"

// The names under which each layer is reported.
const (
	ProjectFile = Dir + "/config.toml"
	LocalFile   = Dir + "/config.local.toml"
	FlagsLayer  = "command line"
)

// Severity tells an error, which fails validation, from a warning, which
// doesn't.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic is one plain-language finding about the config.
type Diagnostic struct {
	Severity Severity
	// Layer is the file (or FlagsLayer) the finding is about, empty when it
	// is about the merged config.
	Layer string
	// Key is the dotted key, empty when the finding is about a whole layer.
	Key     string
	Message string
}

func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(d.Severity.String())
	b.WriteString(": ")
	if d.Layer != "" {
		b.WriteString(d.Layer)
		b.WriteString(": ")
	}
	if d.Key != "" {
		b.WriteString(d.Key)
		b.WriteString(": ")
	}
	b.WriteString(d.Message)
	return b.String()
}

// Override is one value set from the command line, by dotted key.
type Override struct {
	Key   string
	Value any
}

// Result is the outcome of Load.
type Result struct {
	// Values is the merged config, as decoded TOML (tables are
	// map[string]any, lists []any, whole numbers int64). It is nil when a
	// layer could not be read.
	Values      map[string]any
	Diagnostics []Diagnostic
}

// OK reports whether the config is valid: no diagnostic is an error.
func (r Result) OK() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == Error {
			return false
		}
	}
	return true
}

// Load reads the config of the project rooted at root, merges in the CLI
// overrides and validates the result.
func Load(root string, overrides []Override) Result {
	var r Result
	project, ok := readLayer(&r, filepath.Join(root, Dir, "config.toml"), ProjectFile, true)
	if !ok {
		return r
	}
	local, ok := readLayer(&r, filepath.Join(root, Dir, "config.local.toml"), LocalFile, false)
	if !ok {
		return r
	}
	flags := overridesToTree(overrides)

	layers := []struct {
		name   string
		values map[string]any
	}{{ProjectFile, project}, {LocalFile, local}, {FlagsLayer, flags}}

	// A newer version means keys this binary can't know: refuse before
	// judging any of them.
	for _, l := range layers {
		if d, newer := checkVersion(l.name, l.values); newer {
			r.Diagnostics = append(r.Diagnostics, d)
			return r
		}
	}
	if _, has := project["version"]; !has {
		r.add(Error, ProjectFile, "", fmt.Sprintf("`version = %d` is required at the top of the file", SupportedVersion))
	}

	merged := map[string]any{}
	for _, l := range layers {
		for _, c := range applyDeprecations(l.values) {
			r.add(Warning, l.name, c.old, c.warning())
		}
		r.Diagnostics = append(r.Diagnostics, validateLayer(l.name, l.values)...)
		mergeInto(merged, l.values)
	}
	r.Values = merged
	if r.OK() {
		r.Diagnostics = append(r.Diagnostics, validateMerged(merged)...)
	}
	return r
}

// LoadLocal is config.local.toml of the project rooted at root on its own,
// unvalidated; nil if it's missing or can't be read.
func LoadLocal(root string) map[string]any {
	var r Result
	values, ok := readLayer(&r, filepath.Join(root, filepath.FromSlash(LocalFile)), LocalFile, true)
	if !ok {
		return nil
	}
	return values
}

// add records a diagnostic about a whole layer or key.
func (r *Result) add(sev Severity, layer, key, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Severity: sev, Layer: layer, Key: key, Message: msg})
}

// readLayer reads and parses one TOML file. A missing optional file is an
// empty layer.
func readLayer(r *Result, path, name string, required bool) (map[string]any, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the project's own config file
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			r.add(Error, name, "", "not found; run `fliesswerk init` to set up this project")
			return nil, false
		}
		return map[string]any{}, true
	}
	if err != nil {
		r.add(Error, name, "", fmt.Sprintf("can't be read: %v", err))
		return nil, false
	}
	values := map[string]any{}
	if err := toml.Unmarshal(data, &values); err != nil {
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			r.add(Error, name, "", fmt.Sprintf("line %d, column %d: %s", row, col, de.Error()))
		} else {
			r.add(Error, name, "", fmt.Sprintf("is not valid TOML: %v", err))
		}
		return nil, false
	}
	return values, true
}

// checkVersion reports a version newer than this binary understands.
func checkVersion(layer string, values map[string]any) (Diagnostic, bool) {
	v, ok := values["version"].(int64)
	if !ok || v <= SupportedVersion {
		return Diagnostic{}, false
	}
	return Diagnostic{
		Severity: Error, Layer: layer, Key: "version",
		Message: fmt.Sprintf("this config is version %d, but this fliesswerk only understands version %d; run `fliesswerk update --major`, or ask whoever raised it", v, SupportedVersion),
	}, true
}

// overridesToTree turns dotted-key overrides into a nested table.
func overridesToTree(overrides []Override) map[string]any {
	tree := map[string]any{}
	for _, o := range overrides {
		parts := strings.Split(o.Key, ".")
		t := tree
		for _, p := range parts[:len(parts)-1] {
			next, ok := t[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				t[p] = next
			}
			t = next
		}
		t[parts[len(parts)-1]] = o.Value
	}
	return tree
}

// mergeInto merges src into dst key by key: tables on both sides merge,
// anything else (lists included) replaces.
func mergeInto(dst, src map[string]any) {
	for k, sv := range src {
		sm, sIsTable := sv.(map[string]any)
		dm, dIsTable := dst[k].(map[string]any)
		if sIsTable && dIsTable {
			mergeInto(dm, sm)
			continue
		}
		if sIsTable {
			cp := map[string]any{}
			mergeInto(cp, sm)
			dst[k] = cp
			continue
		}
		dst[k] = sv
	}
}
