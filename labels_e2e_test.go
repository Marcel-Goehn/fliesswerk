package main

import (
	"slices"
	"strings"
	"testing"
)

// withLabels gives the fake Tracker's repository exactly labels.
func (p *runProject) withLabels(t *testing.T, labels ...string) {
	t.Helper()
	p.patchTracker(t, map[string]any{"labels": append([]string{}, labels...)})
}

func TestCheckFailsNamingEveryMissingLabelAndBothFixes(t *testing.T) {
	p := checkProject(t, "")
	p.withLabels(t, "Ready-For-Agent")

	code := p.fwk(t, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "in-progress", "needs-investigation", "fliesswerk init",
		"create the label in-progress (Claimed by a Fliesswerk Run)",
		"create the label needs-investigation (Fliesswerk handed it back to a person)")
	if strings.Contains(p.stderr, "create the label ready-for-agent") {
		t.Errorf("stderr names the ready label, which exists in another case:\n%s", p.stderr)
	}
}

func TestCheckPassesWithEveryLabel(t *testing.T) {
	p := checkProject(t, "")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	if code := p.fwk(t, "check"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "fliesswerk check: labels ok")
}

func TestARunWithAMissingLabelStopsBeforeClaimingAnything(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), `{"sessions": [`+approves+`]}`)
	p.withLabels(t, "ready-for-agent", "needs-investigation")

	code := p.run(t)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	if w := p.writes(t); len(w) > 0 {
		t.Errorf("writes = %+v, want none: the Run should stop before its first claim", w)
	}
	mustContain(t, "stderr", p.stderr, "in-progress", "fliesswerk init", "create the label in-progress (Claimed by a Fliesswerk Run)")
}

// createdLabels are the labels the fake Tracker was asked to create, each
// as "name: description", in order.
func (p *runProject) createdLabels(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, w := range p.writes(t) {
		if w.Op == "create_label" {
			out = append(out, strings.Join(w.Add, ",")+": "+w.Text)
		}
	}
	return out
}

func TestInitCreatesEveryMissingLabelAndSaysSo(t *testing.T) {
	p := initProject(t, nodePostgres())
	p.withLabels(t)

	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	want := []string{
		"ready-for-agent: Fliesswerk may work on it",
		"in-progress: Claimed by a Fliesswerk Run",
		"needs-investigation: Fliesswerk handed it back to a person",
	}
	got := p.createdLabels(t)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("created labels = %q, want %q", got, want)
	}
	mustContain(t, "stdout", p.stdout, "+ label ready-for-agent", "+ label in-progress", "+ label needs-investigation")
	mustContain(t, "stdout", p.stdout, "fliesswerk check: labels ok")
}

func TestInitRerunCreatesOnlyTheMissingLabelsByTheirConfiguredNames(t *testing.T) {
	config := teamConfig + "ready_label = \"agent-ready\"\nin_progress_label = \"wip\"\ncannot_do_label = \"stuck\"\n"
	p := teamProject(t, map[string]string{"config.toml": config, "config.local.toml": myDocker})
	p.withLabels(t, "AGENT-READY", "Stuck")

	p.init(t, p.initEnv(t))

	if got, want := p.createdLabels(t), []string{"wip: Claimed by a Fliesswerk Run"}; !slices.Equal(got, want) {
		t.Errorf("created labels = %q, want %q", got, want)
	}
	mustContain(t, "stdout", p.stdout, "+ label wip", "fliesswerk check: labels ok")
	if strings.Contains(p.stdout, "Already set up") {
		t.Errorf("said Already set up after creating a label:\n%s", p.stdout)
	}
}

func TestInitFinishesAndNamesTheGhLinesWhenALabelCantBeCreated(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	p.withLabels(t, "ready-for-agent")
	p.patchTracker(t, map[string]any{"fail_writes": 2})

	code := p.init(t, p.initEnv(t), mine(viaGateway(fakeGateway(t))...)...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	if !p.hasFliesswerkFile("config.local.toml") || !p.hasFliesswerkFile(".gitignore") {
		t.Error("init didn't write its files")
	}
	mustContain(t, "stderr", p.stderr, "in-progress", "needs-investigation",
		"create the label in-progress (Claimed by a Fliesswerk Run)",
		"create the label needs-investigation (Fliesswerk handed it back to a person)",
		"fake tracker: the write failed")
	if got := p.createdLabels(t); len(got) > 0 {
		t.Errorf("created labels = %q, want none", got)
	}
}
