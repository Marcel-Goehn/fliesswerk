package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withFakeDocker adds a docker to env that reports the given platform.
func withFakeDocker(t *testing.T, env []string, platform string) []string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  "context show") echo default ;;
  *SecurityOptions*) echo '[]' ;;
  *OSType*) echo '` + platform + `' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil { //nolint:gosec // an executable the test runs
		t.Fatal(err)
	}
	path := dir
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = dir + string(os.PathListSeparator) + p
		}
	}
	return append(env, "PATH="+path, "KRAFTWERK_ENGINE=docker")
}

func TestCheckExplainsThatWindowsContainersCannotRunTheSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})

	_, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "windows x86_64"), "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
	}
	if !strings.Contains(stderr, "Windows-containers mode") || !strings.Contains(stderr, "Switch to Linux containers") {
		t.Errorf("stderr doesn't explain it: %s", stderr)
	}
}

func TestCheckAcceptsAnEngineThatRunsLinuxContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})

	if _, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check"); code != exitOK {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
}

// composeProjectDir is a project with one Service.
func composeProjectDir(t *testing.T) string {
	t.Helper()
	return projectDir(t, map[string]string{"config.toml": "version = 1\n[services]\ncompose_file = \"compose.yaml\"\n[services.db]\nshared = false\n"})
}

// withFakeCompose adds a docker to env whose compose plugin has version and
// reads the project's compose file as config.
func withFakeCompose(t *testing.T, env []string, version, config string) []string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  "context show") echo default ;;
  *SecurityOptions*) echo '[]' ;;
  *OSType*) echo 'linux x86_64' ;;
  *"compose version"*) echo '` + version + `' ;;
  *compose*config*) echo '` + config + `' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil { //nolint:gosec // an executable the test runs
		t.Fatal(err)
	}
	path := dir
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = dir + string(os.PathListSeparator) + p
		}
	}
	return append(env, "PATH="+path, "KRAFTWERK_ENGINE=docker")
}

func TestCheckEnforcesTheComposeRules(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	const fine = `{"services": {"db": {"image": "postgres"}}}`
	bin := buildKraftwerk(t, "")
	for _, c := range []struct {
		name, version, config string
		want                  []string
	}{
		{"old compose", "2.24.3", fine, []string{"2.24.4"}},
		{"external volume", "2.40.0", `{"services": {"db": {"volumes": [{"type": "volume", "source": "d", "target": "/d"}]}}, "volumes": {"d": {"name": "real", "external": true}}}`, []string{"external"}},
		{"writable absolute bind", "2.40.0", `{"services": {"db": {"volumes": [{"type": "bind", "source": "/srv/real", "target": "/d"}]}}}`, []string{"/srv/real", ":ro"}},
		{"a Service the file lacks", "2.40.0", `{"services": {"other": {"image": "x"}}}`, []string{"services.db"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, stderr, code := runEnv(t, bin, composeProjectDir(t), withFakeCompose(t, ghEnv(t, "ok"), c.version, c.config), "check")
			if code != exitConfigError {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr doesn't say %q: %s", want, stderr)
				}
			}
		})
	}
	t.Run("a usable file", func(t *testing.T) {
		if _, stderr, code := runEnv(t, bin, composeProjectDir(t), withFakeCompose(t, ghEnv(t, "ok"), "v2.24.4", fine), "check"); code != exitOK {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
	})
}
