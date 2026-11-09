package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// deprecation is a key that was renamed or removed. A renamed key keeps
// working until the next major, under a warning; `kraftwerk migrate` rewrites
// it. A key may name a whole table.
type deprecation struct {
	old string
	new string // empty when the key was removed
	// note says why a removed key is gone, or what replaces it.
	note string
}

// deprecations lists them in the order they happened, oldest first.
var deprecations []deprecation

// change is what one deprecation did to one layer.
type change struct {
	deprecation
	// shadowed is true when the layer also set the new name, which wins.
	shadowed bool
}

func (c change) warning() string {
	switch {
	case c.new == "":
		return fmt.Sprintf("no longer has any effect and is ignored (%s); `kraftwerk migrate` removes it", c.note)
	case c.shadowed:
		return fmt.Sprintf("is deprecated and ignored, because `%s` is set as well; `kraftwerk migrate` removes it", c.new)
	}
	return fmt.Sprintf("is deprecated; use `%s`, which `kraftwerk migrate` rewrites it to", c.new)
}

func (c change) report() string {
	switch {
	case c.new == "":
		return fmt.Sprintf("removed `%s` (%s)", c.old, c.note)
	case c.shadowed:
		return fmt.Sprintf("removed `%s`, because `%s` is set as well", c.old, c.new)
	}
	return fmt.Sprintf("renamed `%s` to `%s`", c.old, c.new)
}

// applyDeprecations rewrites the deprecated keys of a decoded layer in place.
func applyDeprecations(values map[string]any) []change {
	var changes []change
	for _, d := range deprecations {
		oldPath := strings.Split(d.old, ".")
		v, ok := lookup(values, oldPath)
		if !ok {
			continue
		}
		remove(values, oldPath)
		c := change{deprecation: d}
		if d.new != "" {
			newPath := strings.Split(d.new, ".")
			_, c.shadowed = lookup(values, newPath)
			if !c.shadowed && !assign(values, newPath, v) {
				// The new name's place holds something else: leave the old
				// key for validation to report.
				assign(values, oldPath, v)
				continue
			}
		}
		changes = append(changes, c)
	}
	return changes
}

func lookup(t map[string]any, path []string) (any, bool) {
	for _, k := range path[:len(path)-1] {
		next, ok := t[k].(map[string]any)
		if !ok {
			return nil, false
		}
		t = next
	}
	v, ok := t[path[len(path)-1]]
	return v, ok
}

func remove(t map[string]any, path []string) {
	for _, k := range path[:len(path)-1] {
		t, _ = t[k].(map[string]any)
	}
	delete(t, path[len(path)-1])
}

// assign sets the value at path, making the tables on the way. It reports
// false if something other than a table is on the way.
func assign(t map[string]any, path []string, v any) bool {
	for _, k := range path[:len(path)-1] {
		if _, set := t[k]; !set {
			t[k] = map[string]any{}
		}
		next, ok := t[k].(map[string]any)
		if !ok {
			return false
		}
		t = next
	}
	t[path[len(path)-1]] = v
	return true
}

// Migration is what `kraftwerk migrate` did to one file.
type Migration struct {
	File    string
	Changes []string
}

// Migrate rewrites the deprecated keys of the project's config files, keeping
// everything else in them as written. Any file it can't read or rewrite stops
// it before the first write. Should a write itself fail, it returns the
// files it has already rewritten along with the finding.
func Migrate(root string) ([]Migration, []Diagnostic) {
	type pending struct {
		path string
		out  []byte
		m    Migration
	}
	var todo []pending
	var r Result
	for _, f := range []struct {
		base, name string
		required   bool
	}{{"config.toml", ProjectFile, true}, {"config.local.toml", LocalFile, false}} {
		path := filepath.Join(root, Dir, f.base)
		values, ok := readLayer(&r, path, f.name, f.required)
		if !ok {
			return nil, r.Diagnostics
		}
		if d, newer := checkVersion(f.name, values); newer {
			return nil, []Diagnostic{d}
		}
		data, err := os.ReadFile(path) //nolint:gosec // the path is the project's own config file
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			r.add(Error, f.name, "", fmt.Sprintf("can't be read: %v", err))
			return nil, r.Diagnostics
		}
		out, changes, err := rewrite(data)
		if err != nil {
			r.add(Error, f.name, "", err.Error())
			return nil, r.Diagnostics
		}
		p := pending{path: path, out: out, m: Migration{File: f.name}}
		for _, c := range changes {
			p.m.Changes = append(p.m.Changes, c.report())
		}
		todo = append(todo, p)
	}

	var done []Migration
	for _, p := range todo {
		if len(p.m.Changes) > 0 {
			if err := replaceFile(p.path, p.out); err != nil {
				r.add(Error, p.m.File, "", fmt.Sprintf("can't be written: %v", err))
				return done, r.Diagnostics
			}
		}
		done = append(done, p.m)
	}
	return done, nil
}

// replaceFile swaps in the new contents at once, keeping the file's mode.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".migrating"
	if err := os.WriteFile(tmp, data, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// rewrite applies the deprecations to the text of a TOML file, so that its
// comments and layout survive. It then proves the result means what
// applyDeprecations says it should, and refuses it otherwise: forms it
// doesn't edit, like a deprecated key inside an inline table, end up there.
func rewrite(data []byte) ([]byte, []change, error) {
	var before, want map[string]any
	if err := toml.Unmarshal(data, &before); err != nil {
		return nil, nil, err
	}
	if err := toml.Unmarshal(data, &want); err != nil {
		return nil, nil, err
	}
	changes := applyDeprecations(want)
	if len(changes) == 0 {
		return data, nil, nil
	}

	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(slices.Clip(data), eol...)
	}
	entries, err := scan(data)
	if err != nil {
		return nil, nil, err
	}
	out := applyEdits(data, plan(entries, before, len(data), eol))

	var got map[string]any
	if err := toml.Unmarshal(out, &got); err != nil || !reflect.DeepEqual(withoutEmptyTables(got), withoutEmptyTables(want)) {
		var names []string
		for _, c := range changes {
			names = append(names, "`"+c.old+"`")
		}
		return nil, nil, fmt.Errorf("can't be rewritten automatically; change %s by hand, as `kraftwerk check` describes", strings.Join(names, ", "))
	}
	return out, changes, nil
}

// withoutEmptyTables is a copy of t minus tables with nothing in them, which
// mean the same as no table at all.
func withoutEmptyTables(t map[string]any) map[string]any {
	out := maps.Clone(t)
	for k, v := range out {
		if sub, ok := v.(map[string]any); ok {
			if sub = withoutEmptyTables(sub); len(sub) == 0 {
				delete(out, k)
			} else {
				out[k] = sub
			}
		}
	}
	return out
}

// edit replaces the bytes from start up to end.
type edit struct {
	start, end int
	text       string
}

func applyEdits(data []byte, edits []edit) []byte {
	slices.SortStableFunc(edits, func(a, b edit) int { return a.start - b.start })
	var out bytes.Buffer
	pos := 0
	for _, e := range edits {
		out.Write(data[pos:e.start])
		out.WriteString(e.text)
		pos = e.end
	}
	out.Write(data[pos:])
	return out.Bytes()
}

// entry is one table header or key/value in a TOML file.
type entry struct {
	start, end       int // its whole lines
	keyStart, keyEnd int // the text of its key
	segments         []string
	path             []string // the key's full path
	header           bool
	array            bool   // a [[header]], or a key under one
	value            string // as written, trailing comment included
}

// match finds the deprecation path falls under, and the rest of path below it.
func match(path []string) (*deprecation, []string) {
	for i := range deprecations {
		old := strings.Split(deprecations[i].old, ".")
		if len(path) >= len(old) && slices.Equal(path[:len(old)], old) {
			return &deprecations[i], path[len(old):]
		}
	}
	return nil, nil
}

// plan turns the deprecations into edits of the scanned file.
func plan(entries []entry, before map[string]any, size int, eol string) []edit {
	var edits []edit
	replaceKey := func(e entry, segments []string) {
		edits = append(edits, edit{e.keyStart, e.keyEnd, dotted(segments)})
	}
	deleteLines := func(e entry) { edits = append(edits, edit{e.start, e.end, ""}) }

	var newTables []string
	newTableLines := map[string]string{}
	move := func(e entry, target []string) {
		deleteLines(e)
		parent, line := target[:len(target)-1], dotted(target[len(target)-1:])+" = "+e.value+eol
		for _, h := range entries {
			if h.header && !h.array && slices.Equal(h.path, parent) {
				edits = append(edits, edit{h.end, h.end, line})
				return
			}
		}
		if len(parent) == 0 {
			at := size
			if i := slices.IndexFunc(entries, func(h entry) bool { return h.header }); i >= 0 {
				at = entries[i].start
			}
			edits = append(edits, edit{at, at, line})
			return
		}
		name := dotted(parent)
		if _, seen := newTableLines[name]; !seen {
			newTables = append(newTables, name)
		}
		newTableLines[name] += line
	}

	covered, dropping := false, false
	for _, e := range entries {
		if e.array {
			covered, dropping = false, false
			continue
		}
		d, rest := match(e.path)
		if e.header {
			covered, dropping = false, false
			if d == nil {
				continue
			}
			if isGone(d, before) {
				dropping = true
				deleteLines(e)
				continue
			}
			covered = true
			replaceKey(e, append(strings.Split(d.new, "."), rest...))
			continue
		}
		switch {
		case dropping:
			deleteLines(e)
		case covered || d == nil:
		case isGone(d, before):
			deleteLines(e)
		default:
			target := append(strings.Split(d.new, "."), rest...)
			if slices.Equal(target[:len(target)-1], e.path[:len(e.path)-1]) {
				segments := slices.Clone(e.segments)
				segments[len(segments)-1] = target[len(target)-1]
				replaceKey(e, segments)
			} else {
				move(e, target)
			}
		}
	}
	for _, name := range newTables {
		edits = append(edits, edit{size, size, eol + "[" + name + "]" + eol + newTableLines[name]})
	}
	return edits
}

// isGone reports whether a deprecated key simply disappears: it was removed,
// or its new name is already set.
func isGone(d *deprecation, before map[string]any) bool {
	if d.new == "" {
		return true
	}
	_, set := lookup(before, strings.Split(d.new, "."))
	return set
}

func dotted(segments []string) string {
	keys := make([]string, len(segments))
	for i, s := range segments {
		keys[i] = bareKey(s)
	}
	return strings.Join(keys, ".")
}

// scan finds the headers and key/values of a file that is known to be valid
// TOML and to end in a newline.
func scan(data []byte) ([]entry, error) {
	var entries []entry
	var table []string
	inArray := false
	for pos := 0; pos < len(data); {
		lineEnd := pos + bytes.IndexByte(data[pos:], '\n') + 1
		trimmed := bytes.TrimLeft(data[pos:lineEnd], " \t")
		if t := bytes.TrimSpace(trimmed); len(t) == 0 || t[0] == '#' {
			pos = lineEnd
			continue
		}
		start := lineEnd - len(trimmed)
		if trimmed[0] == '[' {
			open := 1
			if bytes.HasPrefix(trimmed, []byte("[[")) {
				open = 2
			}
			e := entry{start: pos, end: lineEnd, header: true, array: open == 2}
			e.keyStart = start + open
			e.keyEnd = e.keyStart + outsideQuotes(trimmed[open:], ']')
			var err error
			if e.path, err = keySegments(data[e.keyStart:e.keyEnd]); err != nil {
				return nil, err
			}
			e.segments = e.path
			table, inArray = e.path, e.array
			entries = append(entries, e)
			pos = lineEnd
			continue
		}
		equals := outsideQuotes(trimmed, '=')
		e := entry{start: pos, keyStart: start, keyEnd: start + len(bytes.TrimRight(trimmed[:equals], " \t")), array: inArray}
		var err error
		if e.segments, err = keySegments(data[e.keyStart:e.keyEnd]); err != nil {
			return nil, err
		}
		e.path = append(slices.Clone(table), e.segments...)
		e.end = expressionEnd(data, pos)
		e.value = string(bytes.TrimSpace(data[start+equals+1 : e.end]))
		entries = append(entries, e)
		pos = e.end
	}
	return entries, nil
}

// outsideQuotes finds the first c in b that isn't inside a quoted key.
func outsideQuotes(b []byte, c byte) int {
	var quote byte
	for i := 0; i < len(b); i++ {
		switch {
		case quote == 0 && b[i] == c:
			return i
		case quote == 0 && (b[i] == '"' || b[i] == '\''):
			quote = b[i]
		case quote == '"' && b[i] == '\\':
			i++
		case b[i] == quote:
			quote = 0
		}
	}
	return len(b)
}

// keySegments lets the TOML parser split a key, quotes and all.
func keySegments(key []byte) ([]string, error) {
	var t map[string]any
	if err := toml.Unmarshal(append(slices.Clone(key), " = 0"...), &t); err != nil {
		return nil, err
	}
	var path []string
	for {
		k := sortedKeys(t)[0]
		path = append(path, k)
		next, ok := t[k].(map[string]any)
		if !ok {
			return path, nil
		}
		t = next
	}
}

// expressionEnd is where the key/value starting at pos ends: after the first
// line that completes a valid document. It is what tells a multi-line string
// or list from the lines around it, and config files are small enough to
// parse this way.
func expressionEnd(data []byte, pos int) int {
	for end := pos; end < len(data); {
		end += bytes.IndexByte(data[end:], '\n') + 1
		var t map[string]any
		if toml.Unmarshal(data[pos:end], &t) == nil {
			return end
		}
	}
	return len(data)
}
