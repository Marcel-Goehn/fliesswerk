package step

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolve finds argv's binary on PATH. A Windows `.cmd` or `.bat` shim
// (what npm installs) is replaced by the command it runs, since passing
// arguments through cmd.exe mangles them (quotes in the report schema)
// and is a known injection hazard.
func resolve(argv []string) ([]string, error) {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(bin)) {
	case ".cmd", ".bat":
	default:
		return append([]string{bin}, argv[1:]...), nil
	}
	prefix, err := readShim(bin)
	if err != nil {
		return nil, fmt.Errorf("%s is a shim kraftwerk can't follow (%w); put the real executable on PATH before it", bin, err)
	}
	return append(prefix, argv[1:]...), nil
}

// readShim reads the command line an npm-style shim runs: its last line
// that passes on all arguments (`%*`), with the shim's own directory
// (`%~dp0`, `%dp0%`) filled in and `node` looked up.
func readShim(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the shim LookPath found for the Harness
	if err != nil {
		return nil, err
	}
	var line string
	for l := range strings.Lines(string(data)) {
		if before, _, ok := strings.Cut(l, "%*"); ok {
			line = before
		}
	}
	if line == "" {
		return nil, errors.New("it passes no arguments on")
	}
	// What follows the last `&` (npm's `endLocal & goto … & "%_prog%" …`)
	// is the command.
	if i := strings.LastIndex(line, "&"); i >= 0 {
		line = line[i+1:]
	}
	dir := filepath.Dir(path)
	var argv []string
	for _, tok := range shimFields(line) {
		tok = strings.NewReplacer("%~dp0", dir+`\`, "%dp0%", dir+`\`).Replace(tok)
		switch strings.ToLower(tok) {
		case "%_prog%", "node":
			node, err := findNode(dir)
			if err != nil {
				return nil, err
			}
			tok = node
		}
		if strings.Contains(tok, "%") {
			return nil, fmt.Errorf("it uses the variable in %q", tok)
		}
		if strings.HasPrefix(tok, dir) {
			tok = filepath.Clean(tok)
		}
		argv = append(argv, tok)
	}
	if len(argv) == 0 {
		return nil, errors.New("it runs no command")
	}
	return argv, nil
}

// findNode is node next to the shim, as npm's shim prefers, or on PATH.
func findNode(dir string) (string, error) {
	local := filepath.Join(dir, "node.exe")
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	return exec.LookPath("node")
}

// shimFields splits a cmd.exe command line into words, a double-quoted
// part being one word without its quotes. Leading `@` and parentheses are
// dropped.
func shimFields(s string) []string {
	var fields []string
	var cur strings.Builder
	quoted, inWord := false, false
	for _, c := range strings.TrimSpace(s) {
		switch {
		case c == '"':
			quoted, inWord = !quoted, true
		case !quoted && (c == ' ' || c == '\t' || c == '\r' || c == '\n'):
			if inWord {
				fields = append(fields, cur.String())
				cur.Reset()
				inWord = false
			}
		case !quoted && !inWord && (c == '@' || c == '(' || c == ')'):
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		fields = append(fields, cur.String())
	}
	return fields
}
