package sandbox_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

// network is a Sandbox whose curl answers from a table: the first entry whose
// key is part of the command line is what it prints, then what it exits with.
type network struct {
	answers []answer
}

type answer struct {
	url, prints string
	exit        string
}

func (n network) Exec(argv, _ []string) (hostArgv, hostEnv []string) {
	line := argv[len(argv)-1]
	for _, a := range n.answers {
		if strings.Contains(line, a.url) {
			return []string{"/bin/sh", "-c", "printf '%s' '" + a.prints + "'; exit " + a.exit}, nil
		}
	}
	return []string{"/bin/sh", "-c", "exit 7"}, nil
}
func (network) KillAll(context.Context) error { return nil }
func (network) Services() []sandbox.Service {
	return []sandbox.Service{
		{Name: "db", Addresses: []string{"db:5432", "localhost:5432"}},
	}
}
func (network) MemoryUse(context.Context) (int64, error) { return 0, nil }
func (network) Destroy(context.Context) error            { return nil }

func probe(t *testing.T, n network, allowed string) sandbox.NetworkReport {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake curl is a shell command")
	}
	report, err := sandbox.ProbeNetwork(context.Background(), n, allowed)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

var healthy = []answer{
	{url: "http://localhost:5432/", prints: "0.000000", exit: "7"},
	{url: "http://db:5432/", prints: "0.001200", exit: "52"},
	{url: "http://1.1.1.1/", prints: "0.000000", exit: "7"},
	{url: "https://api.anthropic.com/", prints: "200", exit: "0"},
}

func holds(lines []string, want string) bool {
	return strings.Contains(strings.Join(lines, "\n"), want)
}

func TestAHealthySandboxPassesEveryProbe(t *testing.T) {
	report := probe(t, network{healthy}, "api.anthropic.com")

	if len(report.Problems) != 0 || len(report.Warnings) != 0 {
		t.Fatalf("report = %+v, want no problems or warnings", report)
	}
	for _, want := range []string{"db", "direct connections are blocked", "api.anthropic.com"} {
		if !holds(report.Passed, want) {
			t.Errorf("passed = %q, want it to mention %q", report.Passed, want)
		}
	}
}

func TestAServiceThatAcceptsButStaysSilentCountsAsReachable(t *testing.T) {
	answers := append([]answer{{url: "http://db:5432/", prints: "0.000900", exit: "28"}}, healthy...)

	if report := probe(t, network{answers}, "api.anthropic.com"); len(report.Problems) != 0 {
		t.Errorf("problems = %q", report.Problems)
	}
}

func TestAnUnreachableServiceIsAProblemThatNamesIt(t *testing.T) {
	answers := append([]answer{{url: "http://db:5432/", prints: "0.000000", exit: "7"}}, healthy...)

	report := probe(t, network{answers}, "api.anthropic.com")

	if !holds(report.Problems, "db") || !holds(report.Problems, "db:5432") {
		t.Errorf("problems = %q, want the Service and where it was tried", report.Problems)
	}
}

func TestOpenDirectEgressIsAProblem(t *testing.T) {
	answers := append([]answer{{url: "http://1.1.1.1/", prints: "0.010000", exit: "0"}}, healthy...)

	report := probe(t, network{answers}, "api.anthropic.com")

	if !holds(report.Problems, "without going through the egress proxy") {
		t.Errorf("problems = %q", report.Problems)
	}
}

func TestAnAllowedHostMustBeReachableThroughTheProxy(t *testing.T) {
	for _, c := range []struct {
		name, code, want string
	}{
		{"the proxy refuses it", "403", "refused"},
		{"the proxy can't get there", "502", "couldn't reach"},
		{"the proxy doesn't answer", "000", "couldn't get to the egress proxy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			answers := append([]answer{{url: "https://api.anthropic.com/", prints: c.code, exit: "56"}}, healthy...)

			report := probe(t, network{answers}, "api.anthropic.com")

			if !holds(report.Problems, c.want) || !holds(report.Problems, "api.anthropic.com") {
				t.Errorf("problems = %q, want %q", report.Problems, c.want)
			}
		})
	}
}

func TestATunnelThatFailsLaterStillReachedTheHost(t *testing.T) {
	answers := append([]answer{{url: "https://api.anthropic.com/", prints: "200", exit: "60"}}, healthy...)

	if report := probe(t, network{answers}, "api.anthropic.com"); len(report.Problems) != 0 {
		t.Errorf("problems = %q; an untrusted certificate is the interception check's business", report.Problems)
	}
}

func TestNoAllowedHostSaysThatPartWasNotChecked(t *testing.T) {
	report := probe(t, network{healthy}, "")

	if len(report.Problems) != 0 || !holds(report.Warnings, "wasn't checked") {
		t.Errorf("report = %+v", report)
	}
}

func TestASandboxWithoutCurlFailsLoudlyAndSaysHowToGetIt(t *testing.T) {
	report := probe(t, network{[]answer{{url: "curl", exit: "127"}}}, "api.anthropic.com")

	if !holds(report.Problems, "no curl") || len(report.Passed) != 0 {
		t.Errorf("report = %+v", report)
	}
}

func TestACurlThatNeverRanProvesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake curl is a shell command")
	}
	n := network{[]answer{{url: "curl", exit: "125"}}}

	report, err := sandbox.ProbeNetwork(context.Background(), n, "")

	if err == nil {
		t.Fatalf("report = %+v, want an error: nothing was asked", report)
	}
	if holds(report.Passed, "blocked") {
		t.Errorf("claimed egress is blocked: %+v", report)
	}
}
