package native

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mac fakes what the check on macOS looks at.
func mac(version string, probeErr error) seatbeltPrerequisites {
	return seatbeltPrerequisites{
		version: func() (string, error) { return version, nil },
		probe:   func(context.Context, string) (string, error) { return "what it printed", probeErr },
	}
}

func TestMacOS14AndLaterPass(t *testing.T) {
	for _, version := range []string{"14.0", "14.5.1", "15", "26.1"} {
		if _, err := mac(version, nil).check(context.Background()); err != nil {
			t.Errorf("macOS %s: %v", version, err)
		}
	}
}

func TestMacOSBefore14StopsWithTheFix(t *testing.T) {
	probed := false
	m := mac("13.6.1", nil)
	m.probe = func(context.Context, string) (string, error) { probed = true; return "", nil }
	_, err := m.check(context.Background())
	if err == nil {
		t.Fatal("macOS 13 passed")
	}
	for _, says := range []string{"13.6.1", "macOS 14", "Software Update", "sandbox.kind"} {
		if !strings.Contains(err.Error(), says) {
			t.Errorf("doesn't say %q: %v", says, err)
		}
	}
	if probed {
		t.Error("it went on to use Seatbelt")
	}
}

func TestAMacOSVersionThatCannotBeReadStopsTheRun(t *testing.T) {
	if _, err := mac("unknown", nil).check(context.Background()); err == nil || !strings.Contains(err.Error(), "sandbox.kind") {
		t.Fatalf("an unreadable version: err = %v", err)
	}
	m := mac("", nil)
	m.version = func() (string, error) { return "", errors.New("no sw_vers") }
	if _, err := m.check(context.Background()); err == nil || !strings.Contains(err.Error(), "no sw_vers") {
		t.Fatalf("err = %v", err)
	}
}

func TestAFailingSeatbeltProbeShowsWhatItSaid(t *testing.T) {
	_, err := mac("15.0", errors.New("exit status 71")).check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "what it printed") || !strings.Contains(err.Error(), "sandbox.kind") {
		t.Fatalf("err = %v", err)
	}
}
