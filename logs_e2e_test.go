package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cartScript implements #2 with a word of agent text, and review approves.
func cartScript() string {
	return `{"sessions": [` + approves + `, {"match": "Ticket #2", "actions": [
		{"text": "Thinking about the cart."},
		{"write": "cart/cart.go", "content": "package cart\n"},
		{"run": ["git", "add", "cart/cart.go"]},
		{"run": ["git", "commit", "-q", "-m", "Add the cart package"]}
	], "report": {"outcome": "done", "summary": "Added the cart package."}}]}`
}

// logDir is the Run directory `kraftwerk logs` names.
func (p *runProject) logDir(t *testing.T) string {
	t.Helper()
	if code := p.kwk(t, "logs"); code != 0 {
		t.Fatalf("kraftwerk logs: exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	return strings.TrimSpace(p.stdout)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheDefaultViewShowsEventsAndToolCallsButNotTheAgentsText(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d  Iteration 1: taking 1 Ticket$`),
		regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d  #2  implement  ▸ Bash git add cart/cart\.go$`),
		regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d  #2  closed$`),
		// Five model calls of 100 in and 20 out, $0.001 each.
		regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d  #2  implement  finished in \d+s, tokens 500 in, 100 out, context 100, \$0\.0050$`),
	} {
		if !want.MatchString(p.stdout) {
			t.Errorf("stdout doesn't match %s:\n%s", want, p.stdout)
		}
	}
	if strings.Contains(p.stdout, "Thinking about the cart.") {
		t.Errorf("the agent's text is on the terminal:\n%s", p.stdout)
	}
	if strings.Contains(p.stdout, "\x1b") {
		t.Errorf("colour on a pipe:\n%q", p.stdout)
	}
}

func TestVerboseShowsTheAgentsText(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())

	if code := p.run(t, "--verbose"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if !regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d  #2  implement  Thinking about the cart\.$`).MatchString(p.stdout) {
		t.Errorf("stdout lacks the agent's text:\n%s", p.stdout)
	}
}

func TestTheBannerShowsWhatTheRunIsAndWhereItsLogsGo(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	id := filepath.Base(func() string { out := p.stdout; dir := p.logDir(t); p.stdout = out; return dir }())
	for _, want := range []string{
		"kraftwerk dev", "Run " + id, ".kraftwerk/logs/" + id, "Harness claude_code", "Sandbox none",
		"max_iterations 10", "max_parallel 1", "idle 10m0s", "Step 1h0m0s",
	} {
		if !strings.Contains(p.stdout, want) {
			t.Errorf("the banner lacks %q:\n%s", want, p.stdout)
		}
	}
}

func TestARunLeavesAFullRecordOnDisk(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())
	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	dir := p.logDir(t)

	if want := filepath.Join(p.work, ".kraftwerk", "logs"); filepath.Dir(dir) != want && !strings.HasSuffix(filepath.Dir(dir), filepath.Join(".kraftwerk", "logs")) {
		t.Errorf("logs printed %q, want a directory under %s", dir, want)
	}
	log := readFile(t, filepath.Join(dir, "run.log"))
	for _, want := range []string{"Thinking about the cart.", "Iteration 1", "▸ Write", "Tokens and cost:"} {
		if !strings.Contains(log, want) {
			t.Errorf("run.log lacks %q:\n%s", want, log)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "2", "implement-1.prompt.md")), "Make a cart package.") {
		t.Error("the implement prompt wasn't saved")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "2", "implement-1.jsonl")), `"type":"result"`) {
		t.Error("implement's raw output wasn't saved")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "2", "review-1.jsonl")), `"type":"result"`) {
		t.Error("review's raw output wasn't saved")
	}

	var s struct {
		Schema   int    `json:"schema"`
		RunID    string `json:"run_id"`
		ExitCode int    `json:"exit_code"`
		Closed   []struct {
			ID      string `json:"id"`
			Spec    string `json:"spec"`
			Commits int    `json:"commits"`
			Context int64  `json:"largest_context_window"`
		} `json:"closed"`
		SpecsFinished []struct {
			ID string `json:"id"`
		} `json:"specs_finished"`
		Usage struct {
			Output int64              `json:"output"`
			Cost   map[string]float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "summary.json"))), &s); err != nil {
		t.Fatal(err)
	}
	if s.Schema != 1 || s.RunID != filepath.Base(dir) || s.ExitCode != 0 {
		t.Errorf("summary = %+v", s)
	}
	if len(s.Closed) != 1 || s.Closed[0].ID != "#2" || s.Closed[0].Spec != "#1" || s.Closed[0].Commits != 1 || s.Closed[0].Context != 100 {
		t.Errorf("closed = %+v, want #2 of #1 with one commit", s.Closed)
	}
	if len(s.SpecsFinished) != 1 || s.SpecsFinished[0].ID != "#1" {
		t.Errorf("specs finished = %+v", s.SpecsFinished)
	}
	if s.Usage.Output == 0 || s.Usage.Cost["usd"] == 0 {
		t.Errorf("usage = %+v", s.Usage)
	}
}

func TestRunJSONPrintsTheSummaryToStdoutAndTheLiveLinesToStderr(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())

	if code := p.run(t, "--json"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	var s map[string]any
	if err := json.Unmarshal([]byte(p.stdout), &s); err != nil {
		t.Fatalf("stdout isn't one JSON document: %v\n%s", err, p.stdout)
	}
	if s["schema"] != float64(1) {
		t.Errorf("summary = %v", s)
	}
	if !strings.Contains(p.stderr, "#2  closed") {
		t.Errorf("stderr lacks the live lines:\n%s", p.stderr)
	}
}

func TestARunThatCannotStartStillLeavesASummary(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())
	writeFile(t, p.trackerPath, "not json")

	code := p.run(t, "--json")

	if code != 3 {
		t.Fatalf("exit code = %d, want 3\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	var s struct {
		ExitCode int `json:"exit_code"`
		Stopped  struct {
			Reason string `json:"reason"`
		} `json:"stopped"`
	}
	if err := json.Unmarshal([]byte(p.stdout), &s); err != nil {
		t.Fatalf("stdout isn't JSON: %v\n%s", err, p.stdout)
	}
	if s.ExitCode != 3 || s.Stopped.Reason == "" {
		t.Errorf("summary = %+v", s)
	}
}

func TestSecretsNeverReachTheRecord(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), `{"sessions": [{"match": "Ticket #2", "expand": true, "actions": [
		{"text": "the token is $KWK_TEST_SECRET"}
	], "report": {"outcome": "cannot_do", "reason": "saw $KWK_TEST_SECRET", "summary": "s"}}]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+"scrub_credentials = false\n[sandbox.secrets]\nKWK_TEST_SECRET = { env = \"KWK_SOURCE\" }\n")
	p.extraEnv = []string{"KWK_SOURCE=s3cr3t-hunter2"}

	code := p.run(t, "--verbose")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	out := p.stdout + p.stderr
	dir := p.logDir(t)

	for name, got := range map[string]string{
		"terminal":     out,
		"run.log":      readFile(t, filepath.Join(dir, "run.log")),
		"summary.json": readFile(t, filepath.Join(dir, "summary.json")),
		"jsonl":        readFile(t, filepath.Join(dir, "2", "implement-1.jsonl")),
	} {
		if strings.Contains(got, "hunter2") {
			t.Errorf("%s leaks the secret:\n%s", name, got)
		}
		if !strings.Contains(got, "***") {
			t.Errorf("%s doesn't show the mask:\n%s", name, got)
		}
	}
}

func oldRun(t *testing.T, p *runProject) string {
	t.Helper()
	dir := filepath.Join(p.work, ".kraftwerk", "logs", "2001-01-01T00-00-00")
	writeFile(t, filepath.Join(dir, "run.log"), "old\n")
	return dir
}

func TestOldLogsArePrunedAtRunStart(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())
	old := oldRun(t, p)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old Run's logs are still there (%v)", err)
	}
}

func TestKeepDaysForeverKeepsOldLogs(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())
	old := oldRun(t, p)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+"[logs]\nkeep_days = \"forever\"\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if _, err := os.Stat(old); err != nil {
		t.Errorf("the old Run's logs are gone: %v", err)
	}
}

func TestLogsWithoutARunSaysSo(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), cartIssues(), cartScript())

	if code := p.kwk(t, "logs"); code != 1 || !strings.Contains(p.stderr, "no Run") {
		t.Errorf("exit code = %d, want 1 and a message\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
}
