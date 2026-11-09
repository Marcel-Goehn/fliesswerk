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

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
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

func TestCheckRefusesAServiceANativeSandboxCouldNotReach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n[sandbox]\nkind = \"native\"\n[services.db]\nshared = false\n"})

	_, stderr, code := runEnv(t, bin, root, withFakeCompose(t, ghEnv(t, "ok"), "2.40.0", `{"services": {"db": {"image": "postgres", "expose": ["5432"]}}}`), "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
	}
	if !strings.Contains(stderr, "db") || !strings.Contains(stderr, `ports: ["127.0.0.1:<H>:<C>"]`) {
		t.Errorf("stderr doesn't name the fix: %s", stderr)
	}
}

func TestCheckReportsThePeakAndTheWaitsOfTheLastRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "state/memory.json": `{"peak_bytes": 3221225472, "last_run_waits": 2}`})

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
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})

	stdout, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check")

	if code != exitOK {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr)
	}
	if want := "memory: peak estimate 2048 MB per Sandbox (not measured yet)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}

func TestSummaryCarriesTheMemoryOfTheRun(t *testing.T) {
	s := summaryOf(factory.Result{Memory: &factory.MemoryReport{PeakEstimate: 3 << 30, Waits: 4}}, "run", time.Now(), exitOK)

	if s.Memory == nil || s.Memory.PeakEstimateMB != 3072 || s.Memory.Waits != 4 {
		t.Errorf("memory = %+v, want 3072 MB and 4 waits", s.Memory)
	}
	if got := s.Text(); !strings.Contains(got, "Memory: peak estimate 3072 MB, 4 waits") {
		t.Errorf("the summary lacks the memory:\n%s", got)
	}
}

func TestCheckRefusesACACertsFileItCannotUse(t *testing.T) {
	bin := buildKraftwerk(t, "")
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
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": gatewayConfig(srv.URL)})
	lock := filepath.Join(root, factory.StateDir, "lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	home := configHome(t)
	authSet(t, bin, home, "gw", "good-token")
	env := withFakeDocker(t, append(append(ghEnv(t, "ok"), home...), "SSL_CERT_FILE="+trusted), "linux x86_64")

	_, stderr, code := runEnv(t, bin, root, env, "check")

	if code != exitOK || !strings.Contains(stderr, "a Run is in progress") {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
}
