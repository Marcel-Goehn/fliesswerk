package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/factory"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
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
	return append(env, "PATH="+path)
}

// holdRunLock has a Run in progress in the project, as far as `check` can
// tell, so that it stops short of starting a Sandbox on the fake engine.
func holdRunLock(t *testing.T, root string) {
	t.Helper()
	lock := filepath.Join(root, factory.StateDir, "lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckExplainsThatWindowsContainersCannotRunTheSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildFliesswerk(t, "")
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
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	holdRunLock(t, root)

	stdout, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check")

	if code != exitOK {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	mustContain(t, "stdout", stdout, "fliesswerk check: container engine docker (default) ok")
}

func TestCheckAndRunStopWithoutAConfigLocalAndNameInit(t *testing.T) {
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	if err := os.Remove(filepath.Join(root, ".fliesswerk", "config.local.toml")); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"check", "run"} {
		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "ok"), cmd)

		if code != exitConfigError {
			t.Fatalf("%s: exit code = %d, want %d; stderr: %s", cmd, code, exitConfigError, stderr)
		}
		mustContain(t, cmd+" stderr", stderr, ".fliesswerk/config.local.toml isn't there", "run `fliesswerk init`")
	}
}

func TestCheckAndRunStopWithoutTheEngineAndNameTheKey(t *testing.T) {
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": "[sandbox]\nkind = \"container\"\n"})
	for _, cmd := range []string{"check", "run"} {
		_, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), cmd)

		if code != exitConfigError {
			t.Fatalf("%s: exit code = %d, want %d; stderr: %s", cmd, code, exitConfigError, stderr)
		}
		mustContain(t, cmd+" stderr", stderr, "sandbox.engine: isn't set", `engine = "docker"`, "config.local.toml")
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
	return append(env, "PATH="+path)
}

func TestCheckEnforcesTheComposeRules(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	const fine = `{"services": {"db": {"image": "postgres"}}}`
	bin := buildFliesswerk(t, "")
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
		root := composeProjectDir(t)
		holdRunLock(t, root)
		if _, stderr, code := runEnv(t, bin, root, withFakeCompose(t, ghEnv(t, "ok"), "v2.24.4", fine), "check"); code != exitOK {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
	})
}

func TestCheckRefusesAServiceANativeSandboxCouldNotReach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n[services.db]\nshared = false\n", "config.local.toml": "[sandbox]\nkind = \"native\"\nengine = \"docker\"\n"})

	stdout, stderr, code := runEnv(t, bin, root, withFakeCompose(t, ghEnv(t, "ok"), "2.40.0", `{"services": {"db": {"image": "postgres", "expose": ["5432"]}}}`), "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
	}
	if !strings.Contains(stdout, "container engine docker (default) (for the Services) ok") {
		t.Errorf("stdout doesn't name the engine: %s", stdout)
	}
	if !strings.Contains(stderr, "db") || !strings.Contains(stderr, `ports: ["127.0.0.1:<H>:<C>"]`) {
		t.Errorf("stderr doesn't name the fix: %s", stderr)
	}
}

func TestCheckReportsThePeakAndTheWaitsOfTheLastRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "state/memory.json": `{"peak_bytes": 3221225472, "last_run_waits": 2}`})
	holdRunLock(t, root)

	stdout, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check")

	if code != exitOK {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	if want := "memory: peak estimate 3072 MB per Sandbox (measured), 2 waits for memory in the last Run"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}

func TestCheckSaysWhenNoPeakHasBeenMeasured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	holdRunLock(t, root)

	stdout, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check")

	if code != exitOK {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	if want := "memory: peak estimate 2048 MB per Sandbox (not measured yet)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}

func TestSummaryCarriesTheMemoryOfTheRun(t *testing.T) {
	s := summaryOf(factory.Result{Memory: &factory.MemoryReport{PeakEstimate: 3 << 30, Waits: 4}}, "run", time.Now(), exitOK, harness.Billed)

	if s.Memory == nil || s.Memory.PeakEstimateMB != 3072 || s.Memory.Waits != 4 {
		t.Errorf("memory = %+v, want 3072 MB and 4 waits", s.Memory)
	}
	if got := s.Text(); !strings.Contains(got, "Memory: peak estimate 3072 MB, 4 waits") {
		t.Errorf("the summary lacks the memory:\n%s", got)
	}
}

func TestCheckRefusesACACertsFileItCannotUse(t *testing.T) {
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n[network]\nca_certs = [\"missing.pem\"]\n"})

	_, stderr, code := runEnv(t, bin, root, ghEnv(t, "ok"), "check")

	if code != exitConfigError || !strings.Contains(stderr, "ca_certs") || !strings.Contains(stderr, "missing.pem") {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
}

// The gateway has to speak TLS, or there is nothing to check; and Go only
// lets SSL_CERT_FILE decide what the host trusts on Linux.
func TestCheckLeavesARunningRunAloneAndSaysItSkippedTheTrustCheck(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the host's trust is set through SSL_CERT_FILE")
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	t.Cleanup(srv.Close)
	trusted := filepath.Join(t.TempDir(), "gateway.pem")
	if err := os.WriteFile(trusted, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": gatewayConfig(srv.URL)})
	holdRunLock(t, root)
	home := configHome(t)
	authSet(t, bin, home, "gw", "good-token")
	env := withFakeDocker(t, append(append(ghEnv(t, "ok"), home...), "SSL_CERT_FILE="+trusted), "linux x86_64")

	_, stderr, code := runEnv(t, bin, root, env, "check")

	if code != exitOK || !strings.Contains(stderr, "a Run is in progress") {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
}
