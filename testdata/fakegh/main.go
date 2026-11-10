// Command fakegh stands in for gh in end-to-end tests. It lives under
// testdata, so `go build ./...` skips it and no release ever ships it.
//
// It answers from $FAKEGH_DIR/calls.json, a list of
// {"args": "issue list …", "stdout_file": "x.json", "stdout": "…", "exit": 0}
// matched by the exact argv joined with spaces, and appends every call to
// $FAKEGH_LOG as a JSON line {"args": […], "gh_token": "…"}.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type response struct {
	Args       string `json:"args"`
	Stdout     string `json:"stdout"`
	StdoutFile string `json:"stdout_file"`
	Stderr     string `json:"stderr"`
	Exit       int    `json:"exit"`
}

func main() {
	args := os.Args[1:]
	if log := os.Getenv("FAKEGH_LOG"); log != "" {
		line, _ := json.Marshal(map[string]any{"args": args, "gh_token": os.Getenv("GH_TOKEN")})
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fail("fakegh: %v", err)
		}
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	dir := os.Getenv("FAKEGH_DIR")
	data, err := os.ReadFile(filepath.Join(dir, "calls.json"))
	if err != nil {
		fail("fakegh: %v", err)
	}
	var responses []response
	if err := json.Unmarshal(data, &responses); err != nil {
		fail("fakegh: calls.json: %v", err)
	}
	key := strings.Join(args, " ")
	for _, r := range responses {
		if r.Args != key {
			continue
		}
		out := []byte(r.Stdout)
		if r.StdoutFile != "" {
			if out, err = os.ReadFile(filepath.Join(dir, r.StdoutFile)); err != nil {
				fail("fakegh: %v", err)
			}
		}
		_, _ = os.Stdout.Write(out)
		_, _ = fmt.Fprint(os.Stderr, r.Stderr)
		os.Exit(r.Exit)
	}
	fail("fakegh: no fixture for: %s", key)
}

func fail(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
