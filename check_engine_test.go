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
