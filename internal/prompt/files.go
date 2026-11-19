package prompt

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Dir holds a project's prompt files, relative to the project root.
const Dir = ".fliesswerk/prompts"

// The built-in editable parts: builtin/<step>.md is the latest, and
// builtin/history/<step>.v<N>.md each one before it. Changing a built-in
// means moving the old text into the history first, which makes it the
// next version; `check` then tells projects based on the old one.
//
//go:embed builtin
var builtinFS embed.FS

// builtins are every Step's built-in parts.
var builtins = loadBuiltins(builtinFS)

// versions are a Step's built-in parts, oldest first: version v is
// versions[v-1].
type versions []string

// loadBuiltins reads every Step's built-in parts, the history's and the
// latest, from fsys, with "\n" line ends however git checked them out.
func loadBuiltins(fsys fs.FS) map[string]versions {
	out := map[string]versions{}
	for _, step := range Steps {
		var vs versions
		for v := 1; ; v++ {
			old, err := fs.ReadFile(fsys, fmt.Sprintf("builtin/history/%s.v%d.md", step, v))
			if err != nil {
				break
			}
			vs = append(vs, lf(old))
		}
		latest, err := fs.ReadFile(fsys, "builtin/"+step+".md")
		if err != nil {
			panic(err) // the built-ins are compiled in
		}
		out[step] = append(vs, lf(latest))
	}
	return out
}

// lf is text with "\r\n" line ends made "\n".
func lf(text []byte) string { return strings.ReplaceAll(string(text), "\r\n", "\n") }

// Latest is the version of step's latest built-in part.
func Latest(step string) int { return len(builtins[step]) }

// Builtin is version v of step's built-in part, "" if there is none.
func Builtin(step string, v int) string {
	if v < 1 || v > Latest(step) {
		return ""
	}
	return builtins[step][v-1]
}

// Part is the editable part of one Step's prompt.
type Part struct {
	Step string
	// Path is the project's file, relative to the root, with forward
	// slashes.
	Path string
	// Exists reports whether the project has the file. Without it, Text is
	// the latest built-in part.
	Exists bool
	// Text is the part, without the file's marker line.
	Text string
	// Based is the built-in version the file's marker line names, 0 if it
	// has none.
	Based int
}

// Improved reports whether the built-in part has improved since the
// version the project's file is based on.
func (p Part) Improved() bool {
	return p.Exists && p.Based > 0 && p.Based < Latest(p.Step)
}

// markerLine is the first line of a prompt file `init` writes. It names
// the Step and the built-in version the file starts from, and isn't sent
// to the agent.
var markerLine = regexp.MustCompile(`^<!-- fliesswerk: based on the built-in ([a-z]+) prompt v([0-9]+)\b.*-->[ \t]*(\r?\n|$)`)

// Marker is the marker line of a file based on step's latest built-in.
func Marker(step string) string {
	return fmt.Sprintf("<!-- fliesswerk: based on the built-in %s prompt v%d; this line isn't sent to the agent -->", step, Latest(step))
}

// Load reads every Step's part from the project at root.
func Load(root string) (map[string]Part, error) {
	parts := map[string]Part{}
	for _, step := range Steps {
		p := Part{Step: step, Path: path.Join(Dir, step+".md")}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Path)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			p.Text = Builtin(step, Latest(step))
		case err != nil:
			return nil, err
		default:
			p.Exists, p.Text = true, string(data)
			if m := markerLine.FindStringSubmatch(p.Text); m != nil && m[1] == step {
				p.Based, _ = strconv.Atoi(m[2])
				p.Text = strings.TrimLeft(p.Text[len(m[0]):], "\r\n")
			}
		}
		parts[step] = p
	}
	return parts, nil
}
