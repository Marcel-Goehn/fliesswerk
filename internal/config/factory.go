package config

import (
	"fmt"
	"runtime"

	"github.com/Marcel-Goehn/kraftwerk/internal/version"
)

// CheckFactory judges the merged config's `factory` pin against this binary's
// version, which is empty in a development build.
func CheckFactory(values map[string]any, current string) []Diagnostic {
	pin, ok := values["factory"].(string)
	if !ok {
		return nil
	}
	finding := func(sev Severity, format string, args ...any) []Diagnostic {
		return []Diagnostic{{Severity: sev, Key: "factory", Message: fmt.Sprintf(format, args...)}}
	}
	r, err := version.ParseRange(pin)
	if err != nil {
		return finding(Error, "%q is not a semver range (%v); write it like \"1\", \"~1.2\" or \">=1.2 <2\"", pin, err)
	}
	if current == "" {
		return finding(Warning, "not checked: this is a development build, which has no version to compare with %q", pin)
	}
	v, _, ok := version.Parse(current)
	if !ok {
		return finding(Warning, "not checked: this binary's version %q can't be compared with %q", current, pin)
	}
	if r.Contains(v) {
		return nil
	}
	return finding(Error, "this project needs kraftwerk %q, but this is %s; %s", pin, v, fixFor(r, v))
}

// fixFor names the command that gets from v to a version r allows.
func fixFor(r version.Range, v version.Version) string {
	if next, ok := r.Above(v); ok {
		if next.Major() == v.Major() {
			return "run `kraftwerk update`"
		}
		return "run `kraftwerk update --major`"
	}
	release := "<a release that matches>"
	if floor := r.Floor(); floor != (version.Version{}) {
		release = "v" + floor.String()
	}
	return "install a release it allows: `" + installCommand(release) + "`"
}

// installCommand is the one-line install of a given release on this OS.
func installCommand(release string) string {
	const raw = "https://raw.githubusercontent.com/Marcel-Goehn/kraftwerk/main/"
	if runtime.GOOS == "windows" {
		return "$env:KRAFTWERK_VERSION='" + release + "'; irm " + raw + "install.ps1 | iex"
	}
	return "curl -fsSL " + raw + "install.sh | KRAFTWERK_VERSION=" + release + " sh"
}
