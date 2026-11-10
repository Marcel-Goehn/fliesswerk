package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
)

func TestFactoryPinAcceptsAVersionItsRangeAllows(t *testing.T) {
	cases := []struct{ pin, version string }{
		{"1", "1.0.0"},
		{"1", "1.9.3"},
		{"1.x", "1.4.0"},
		{"*", "3.1.4"},
		{"1.2", "1.2.9"},
		{"1.2.3", "1.2.3"},
		{"^1.2", "1.9.0"},
		{"^0.2.1", "0.2.5"},
		{"~1.2", "1.2.7"},
		{">=1.2 <2", "1.5.0"},
		{">1.2.3", "1.2.4"},
		{"<=1.2", "1.2.8"},
		{"1 || 3", "3.0.1"},
		{"1", "1.3.0-rc.1"},
	}
	for _, c := range cases {
		if d := config.CheckFactory(pinned(t, c.pin), c.version); len(d) != 0 {
			t.Errorf("factory = %q on %s: got %v, want no finding", c.pin, c.version, d)
		}
	}
}

func TestFactoryPinNamesTheCommandThatFixesAMismatch(t *testing.T) {
	cases := []struct {
		name, pin, version string
		want               []string
		dontWant           string
	}{
		{name: "older within the major", pin: "^1.5", version: "1.2.0", want: []string{"1.2.0", "`fliesswerk update`"}},
		{name: "older major", pin: "2", version: "1.9.0", want: []string{"`fliesswerk update --major`"}},
		{name: "several ranges, the nearest is in the major", pin: "1.8 || 2", version: "1.2.0", want: []string{"`fliesswerk update`"}},
		{name: "newer", pin: "~1.2", version: "1.5.0", want: []string{"FLIESSWERK_VERSION", "v1.2.0"}, dontWant: "update --major"},
		{name: "newer with no lower bound", pin: "<1.4", version: "1.5.0", want: []string{"FLIESSWERK_VERSION", "a release that matches"}, dontWant: "v0.0.0"},
		{name: "caret on zero stops at the minor", pin: "^0.2", version: "0.3.0", want: []string{"FLIESSWERK_VERSION", "v0.2.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := config.CheckFactory(pinned(t, c.pin), c.version)
			if len(got) != 1 || got[0].Severity != config.Error || got[0].Key != "factory" {
				t.Fatalf("got %v, want one error about factory", got)
			}
			mustHave(t, got[0].Message, c.want...)
			if c.dontWant != "" && strings.Contains(got[0].Message, c.dontWant) {
				t.Errorf("message %q contains %q", got[0].Message, c.dontWant)
			}
		})
	}
}

func TestFactoryPinThatIsNotARangeIsAnError(t *testing.T) {
	for _, pin := range []string{"", "banana", "1.2.3.4", ">=", "1 ||", "^*x"} {
		got := config.CheckFactory(pinned(t, pin), "1.0.0")
		if len(got) != 1 || got[0].Severity != config.Error {
			t.Errorf("factory = %q: got %v, want one error", pin, got)
			continue
		}
		mustHave(t, got[0].Message, "semver range")
	}
}

func TestFactoryPinIsNotCheckedOnADevBuildButSaysSo(t *testing.T) {
	got := config.CheckFactory(pinned(t, "1"), "")
	if len(got) != 1 || got[0].Severity != config.Warning {
		t.Fatalf("got %v, want one warning", got)
	}
	mustHave(t, got[0].Message, "development build")
}

func TestNoFactoryPinMeansNoFinding(t *testing.T) {
	values := load(t, "version = 1\n", "").Values
	if got := config.CheckFactory(values, "1.0.0"); got != nil {
		t.Fatalf("got %v, want nothing", got)
	}
	if got := config.CheckFactory(values, ""); got != nil {
		t.Fatalf("got %v, want nothing on a dev build either", got)
	}
}

// pinned is what Load makes of a config.toml with this pin.
func pinned(t *testing.T, pin string) map[string]any {
	t.Helper()
	return load(t, fmt.Sprintf("version = 1\nfactory = %q\n", pin), "").Values
}

func mustHave(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}
