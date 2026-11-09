//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox/container"
)

// probeScript has the fake Harness write what the Sandbox can reach to a
// file and commit it. The engine's binaries and socket must be out of
// reach; 1.1.1.1 is an address that answers from anywhere with a network.
const probeScript = `{"sessions": [` + approves + `, {"actions": [
	{"run": ["sh", "-c", "if env -u http_proxy -u https_proxy wget -q -T 3 -O /dev/null http://1.1.1.1/; then echo reached; else echo cut; fi > net.txt"]},
	{"run": ["sh", "-c", "if test -e /var/run/docker.sock || command -v docker; then echo engine; else echo no-engine; fi > engine.txt"]},
	{"run": ["sh", "-c", "if test -f /.dockerenv || test -f /run/.containerenv; then echo container; else echo host; fi > where.txt"]},
	{"run": ["git", "add", "net.txt", "engine.txt", "where.txt"]},
	{"run": ["git", "commit", "-q", "-m", "Probe the Sandbox"]}
], "report": {"outcome": "done", "summary": "Probed."}}]}`

// inAContainer passes only in a container.
const inAContainer = `test -f /.dockerenv || test -f /run/.containerenv`

func TestARunClosesATicketInsideASandboxThatCanReachNothing(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), probeScript)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "Containerfile"), "FROM docker.io/alpine/git:latest\n")
	gitIn(t, p.work, "add", ".")
	gitIn(t, p.work, "commit", "-q", "-m", "Add the Containerfile")
	gitIn(t, p.work, "push", "-q", "origin", "main")
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), `verify = ["inside"]

[commands]
inside = '`+inAContainer+`'

[sandbox]
kind = "container"
run_args = ["--mount", "type=bind,source=`+filepath.Join(fakeClaudeBinDir(t), "claude")+`,target=/usr/local/bin/claude,readonly", "--mount", "type=bind,source=`+filepath.Dir(p.scriptPath)+`,target=/fake"]

[sandbox.env]
KWK_FAKE_HARNESS_SCRIPT = "/fake/script.json"

[[hooks.sandbox_created]]
run = '`+inAContainer+`'
`)
	// Podman and Docker need more than the tests' minimal PATH offers.
	p.extraEnv = append(p.extraEnv, "PATH="+fakeClaudeBinDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))

	code := p.run(t)

	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	branch := "kraftwerk/spec-1-spec-shopping-cart"
	for file, want := range map[string]string{"net.txt": "cut", "engine.txt": "no-engine", "where.txt": "container"} {
		if got := gitIn(t, p.remote, "show", branch+":"+file); got != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
	}
	for _, w := range p.writes(t) {
		if w.Op == "close" && w.Ticket == 2 {
			assertNothingLeft(t, p.work)
			return
		}
	}
	t.Errorf("#2 wasn't closed; writes: %+v", p.writes(t))
}

// assertNothingLeft checks that the Run left nothing of its own on the
// engine.
func assertNothingLeft(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	dir, ok := runlog.Latest(root)
	if !ok {
		t.Fatal("the Run left no logs")
	}
	engine, err := container.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range [][]string{{"ps", "-a"}, {"network", "ls"}} {
		args := append(append([]string{engine.Binary}, kind...), "--filter", "label=kraftwerk.run="+filepath.Base(dir), "--format", "{{.Names}}")
		if kind[0] == "network" {
			args[len(args)-1] = "{{.Name}}"
		}
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput() //nolint:gosec // the test's own engine call
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "" {
			t.Errorf("%s left: %s", kind[0], got)
		}
	}
}

// A database that says when it is ready, for the live probe to find.
const checkCompose = `services:
  postgres:
    image: docker.io/library/postgres:17-alpine
    environment:
      POSTGRES_HOST_AUTH_METHOD: trust
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 1s
      timeout: 3s
      retries: 60
`

// checkContainerConfig is a container Sandbox with the fake Harness in it, a
// Service, a host to reach and commands to run; extra adds to [commands].
func checkContainerConfig(t *testing.T, extra string) string {
	t.Helper()
	return `[sandbox]
kind = "container"
run_args = ["--mount", "type=bind,source=` + filepath.Join(fakeClaudeBinDir(t), "claude") + `,target=/usr/local/bin/claude,readonly"]

[sandbox.network]
allow = ["github.com"]

[services]
compose_file = "compose.yaml"

[services.postgres]
shared = false

[commands]
inside = '` + inAContainer + `'
` + extra + `
[[hooks.sandbox_created]]
run = '` + inAContainer + `'
`
}

func TestCheckProvesAContainerSandboxAndRunsItsCommands(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "Containerfile"), "FROM docker.io/curlimages/curl:latest\n")
	writeFile(t, filepath.Join(p.work, "compose.yaml"), checkCompose)
	gitIn(t, p.work, "add", ".")
	gitIn(t, p.work, "commit", "-q", "-m", "Add the Containerfile and the Service")
	gitIn(t, p.work, "push", "-q", "origin", "main")
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), checkContainerConfig(t, ""))
	// Podman and Docker need more than the tests' minimal PATH offers.
	p.extraEnv = append(p.extraEnv, "PATH="+fakeClaudeBinDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code := p.kwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout,
		"the Service postgres is reachable at",
		"direct connections are blocked",
		"github.com is reachable through the egress proxy",
		"claude ok",
		"sandbox_created: `"+inAContainer+"` worked",
		"`inside` passes on main",
	)
	assertNoCheckLeftBehind(t)

	if code := p.kwk(t, "check"); code != exitOK {
		t.Fatalf("second check: exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "is present")

	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), checkContainerConfig(t, "broken = 'echo nope && exit 3'\n"))
	code := p.kwk(t, "check", "--commands")
	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "`broken` is already failing on main", "nope")
	mustContain(t, "stdout", p.stdout, "`inside` passes on main")
	assertNoCheckLeftBehind(t)
}

// assertNoCheckLeftBehind checks that `check` left nothing on the engine.
func assertNoCheckLeftBehind(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	engine, err := container.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range [][]string{{"ps", "-a"}, {"network", "ls"}} {
		args := append(append([]string{engine.Binary}, kind...), "--filter", "name=kraftwerk-check-", "--format", "{{.Names}}")
		if kind[0] == "network" {
			args[len(args)-1] = "{{.Name}}"
		}
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput() //nolint:gosec // the test's own engine call
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "" {
			t.Errorf("%s left: %s", kind[0], got)
		}
	}
}
