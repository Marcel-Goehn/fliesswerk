package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"testing"
)

// costProject is a Run of the cart Ticket signed in with method, whose
// secret comes from the env.
func costProject(t *testing.T, method string) *runProject {
	t.Helper()
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+
		"[harnesses.claude_code]\nmethod = \""+method+"\"\nsecret = { env = \"A_CLAUDE_CREDENTIAL\" }\n")
	p.extraEnv = append(configHome(t), "A_CLAUDE_CREDENTIAL=a-claude-credential")
	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	return p
}

// costBasis is summary.json's schema and cost basis.
func (p *runProject) costBasis(t *testing.T) (schema int, basis string, closed []map[string]any) {
	t.Helper()
	var s struct {
		Schema    int              `json:"schema"`
		CostBasis string           `json:"cost_basis"`
		Closed    []map[string]any `json:"closed"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(p.logDir(t), "summary.json"))), &s); err != nil {
		t.Fatal(err)
	}
	return s.Schema, s.CostBasis, s.Closed
}

func TestASubscriptionShowsItsCostAsAnAPIPriceEstimate(t *testing.T) {
	p := costProject(t, "oauth_token")

	// Five model calls at $0.001 each on implement, one on review.
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)  #2  implement  finished in \d+s, tokens 500 in, 100 out, context 100, ≈\$0\.0050 API-price estimate \(covered by your subscription\)$`),
		regexp.MustCompile(`(?m)^  #2  Add the cart  \(Spec #1\).*≈\$0\.\d+ API-price estimate \(covered by your subscription\)$`),
		regexp.MustCompile(`(?m)^  Run  .*≈\$0\.\d+ API-price estimate \(covered by your subscription\)$`),
	} {
		if !want.MatchString(p.stdout) {
			t.Errorf("stdout doesn't match %s:\n%s", want, p.stdout)
		}
	}
	schema, basis, closed := p.costBasis(t)
	if schema != 1 || basis != "subscription_estimate" {
		t.Errorf("schema = %d, cost_basis = %q, want 1 and subscription_estimate", schema, basis)
	}
	if len(closed) != 1 || closed[0]["cost_display"] == nil || closed[0]["commits"] == nil {
		t.Errorf("closed = %v, want its existing fields", closed)
	}
}

func TestAnAPIKeyShowsItsCostAsBilled(t *testing.T) {
	p := costProject(t, "api_key")

	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)  #2  implement  finished in \d+s, tokens 500 in, 100 out, context 100, \$0\.0050$`),
		regexp.MustCompile(`(?m)^  Run  \d+ in, \d+ out  \$0\.\d+$`),
	} {
		if !want.MatchString(p.stdout) {
			t.Errorf("stdout doesn't match %s:\n%s", want, p.stdout)
		}
	}
	if regexp.MustCompile(`estimate|≈`).MatchString(p.stdout) {
		t.Errorf("an API key's cost reads as an estimate:\n%s", p.stdout)
	}
	if schema, basis, _ := p.costBasis(t); schema != 1 || basis != "billed" {
		t.Errorf("schema = %d, cost_basis = %q, want 1 and billed", schema, basis)
	}
}
