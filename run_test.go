package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
)

// ghCall is one call the fake gh logged.
type ghCall struct {
	Args    []string `json:"args"`
	GhToken string   `json:"gh_token"`
}

func readGhLog(t *testing.T, path string) []ghCall {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // a test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var calls []ghCall
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c ghCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}

// tree lists every file under root.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// isRead reports whether a gh call only reads: `issue list`, `issue view`
// or a `gh api` GET.
func isRead(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "issue":
		return args[1] == "list" || args[1] == "view"
	case "api":
		return !slices.ContainsFunc(args, func(a string) bool {
			return slices.Contains([]string{"-X", "--method", "-f", "-F", "--field", "--raw-field", "--input"}, a)
		})
	}
	return false
}

const dryRunProject = "version = 1\n[tracker]\nrepo = \"Marcel-Goehn/kraftwerk\"\n"

func TestRunDryRun(t *testing.T) {
	bin := buildKraftwerk(t, "")

	t.Run("shows Iteration 1 and every skipped Ticket with its reason", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": dryRunProject})
		stdout, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun"), "run", "--dry-run")

		if code != 1 {
			t.Fatalf("exit code = %d, want 1 (a Ticket is skipped and needs a fix); stderr: %s", code, stderr)
		}
		for _, want := range []string{
			"Iteration 1 would take 1 Ticket (max_parallel = 1):\n  #35  Tracker boundary, GitHub adapter and `run --dry-run`  (Spec #31)\n",
			"Ready, for a later Iteration:\n  #36  Harness boundary: Claude Code adapter and scripted fake Harness  (Spec #31)\n",
			"  #31  Spec: Factory v1 (Kraftwerk): is a Spec; its Tickets are worked instead\n",
			"  #37  First Run: git, branches and loop, `kind = \"none\"`: waits for #36, #35 (open Blockers)\n",
			"  #64  Nightly jobs: names #36, #41, #40, acme/lib#5 as Blockers in its text, with no native blocking link\n",
			"       fix: gh issue edit 64 --repo Marcel-Goehn/kraftwerk --add-blocked-by 36\n",
			"       fix: gh issue edit 64 --repo Marcel-Goehn/kraftwerk --add-blocked-by https://github.com/acme/lib/issues/5\n",
			"Dry run: nothing was written and nothing was started.\n",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
		if want := "warning: #64: Blocker acme/lib#9 is outside its Spec #31"; !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	})

	t.Run("max_parallel bounds Iteration 1", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": dryRunProject})
		stdout, stderr, _ := runEnv(t, bin, root, ghEnv(t, "dryrun"), "run", "--dry-run", "--max-parallel", "2")
		want := "Iteration 1 would take 2 Tickets (max_parallel = 2):\n  #35  "
		if !strings.Contains(stdout, want) || !strings.Contains(stdout, "\n  #36  Harness boundary") || strings.Contains(stdout, "later Iteration") {
			t.Fatalf("stdout = %s\nstderr: %s", stdout, stderr)
		}
	})

	t.Run("only reads, and hands the token to gh in the env, never in argv", func(t *testing.T) {
		const token = "s3cret-token-value"
		root := projectDir(t, map[string]string{
			"config.toml":       dryRunProject,
			"config.local.toml": "[tracker]\ntoken = { env = \"KWK_TEST_GH_TOKEN\" }\n",
		})
		before := tree(t, root)
		log := filepath.Join(t.TempDir(), "gh.log")

		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun", "FAKEGH_LOG="+log, "KWK_TEST_GH_TOKEN="+token), "run", "--dry-run")
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr)
		}

		calls := readGhLog(t, log)
		if len(calls) == 0 {
			t.Fatal("gh was never called")
		}
		for _, c := range calls {
			if !isRead(c.Args) {
				t.Errorf("gh %s is not a read", strings.Join(c.Args, " "))
			}
			if c.GhToken != token {
				t.Errorf("gh %s ran with GH_TOKEN = %q, want the configured token", strings.Join(c.Args, " "), c.GhToken)
			}
			for _, a := range c.Args {
				if strings.Contains(a, token) {
					t.Errorf("the token is on gh's command line: %q", c.Args)
				}
			}
		}
		if after := tree(t, root); !slices.Equal(before, after) {
			t.Errorf("files changed:\nbefore %v\nafter  %v", before, after)
		}
	})

	t.Run("a token from an unset variable exits 3", func(t *testing.T) {
		root := projectDir(t, map[string]string{
			"config.toml":       dryRunProject,
			"config.local.toml": "[tracker]\ntoken = { env = \"KWK_TEST_UNSET\" }\n",
		})
		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun"), "run", "--dry-run")
		if code != 3 || !strings.Contains(stderr, "KWK_TEST_UNSET") {
			t.Fatalf("exit code = %d, want 3 naming the variable; stderr: %s", code, stderr)
		}
	})

	t.Run("a failing Snapshot stops with exit 2", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n[tracker]\nrepo = \"someone/else\"\n"})
		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun"), "run", "--dry-run")
		if code != 2 || !strings.Contains(stderr, "no fixture") {
			t.Fatalf("exit code = %d, want 2 carrying gh's error; stderr: %s", code, stderr)
		}
	})

	t.Run("an open network warns on the Run, and only a local layer may open it", func(t *testing.T) {
		const open = "[sandbox.network]\nallow = [\"*\"]\n"
		for name, tt := range map[string]struct {
			files map[string]string
			args  []string
		}{
			"config.local.toml": {map[string]string{"config.toml": dryRunProject, "config.local.toml": open}, []string{"run", "--dry-run"}},
			"a CLI flag":        {map[string]string{"config.toml": dryRunProject}, []string{"run", "--dry-run", "--allow", "*"}},
		} {
			_, stderr, code := runEnv(t, bin, projectDir(t, tt.files), ghEnv(t, "dryrun"), tt.args...)
			if code == 3 || !strings.Contains(stderr, "warning: sandbox.network.allow: `allow = [\"*\"]` lets the Sandbox reach any host") {
				t.Fatalf("%s: exit code = %d, want the Run to go on with a warning; stderr: %s", name, code, stderr)
			}
		}

		root := projectDir(t, map[string]string{"config.toml": dryRunProject + open})
		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun"), "run", "--dry-run")
		if code != 3 || !strings.Contains(stderr, config.LocalFile) {
			t.Fatalf("exit code = %d, want 3 naming %s; stderr: %s", code, config.LocalFile, stderr)
		}
	})

	t.Run("an invalid config exits 3 before gh runs", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\nmax_paralel = 2\n"})
		log := filepath.Join(t.TempDir(), "gh.log")
		_, _, code := runEnv(t, bin, root, ghEnv(t, "dryrun", "FAKEGH_LOG="+log), "run", "--dry-run")
		if code != 3 {
			t.Fatalf("exit code = %d, want 3", code)
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatal("gh ran although the config is invalid")
		}
	})
}

func TestARunThatCannotBuildANativeSandboxStopsWithTheFix(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": dryRunProject})
	log := filepath.Join(t.TempDir(), "gh.log")

	// The tests' minimal PATH has no bwrap.
	_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun", "FAKEGH_LOG="+log), "run", "--sandbox", "native")

	want := "apt install bubblewrap"
	if runtime.GOOS != "linux" {
		want = "--sandbox none"
	}
	if code != 3 || !strings.Contains(stderr, want) {
		t.Fatalf("exit code = %d, want 3 saying %q; stderr: %s", code, want, stderr)
	}
}

func TestANativeSandboxRefusesAPathItMustNeverExpose(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": dryRunProject + "\n[sandbox]\nkind = \"native\"\nread_only_paths = [\"/var/run/docker.sock\"]\n"})
	log := filepath.Join(t.TempDir(), "gh.log")

	_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun", "FAKEGH_LOG="+log), "run")

	if code != 3 || !strings.Contains(stderr, "sandbox.read_only_paths") || !strings.Contains(stderr, "refused") {
		t.Fatalf("exit code = %d, want 3 refusing the path; stderr: %s", code, stderr)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("gh ran although the Run can't start")
	}
}

func TestCheckWarnsAboutANativeSandboxFootgun(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n[sandbox]\nkind = \"native\"\nwritable_paths = [\"~/.bashrc\"]\n"})

	_, stderr, _ := runEnv(t, bin, root, ghEnv(t, "ok"), "check")

	if !strings.Contains(stderr, "warning: sandbox.writable_paths") || !strings.Contains(stderr, "shell rc file") {
		t.Fatalf("stderr doesn't warn: %s", stderr)
	}
}

func TestARunWithoutAContainerEngineStopsWithTheFix(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": dryRunProject})

	// Whichever engine CI picks for its own job, this Run must choose itself.
	_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun", "KRAFTWERK_ENGINE="), "run")

	if code != 3 || !strings.Contains(stderr, "Docker or Podman") {
		t.Fatalf("exit code = %d, want 3 naming the engines; stderr: %s", code, stderr)
	}
}

func TestARefusedRunArgStopsTheRunBeforeItStarts(t *testing.T) {
	bin := buildKraftwerk(t, "")
	for _, arg := range []string{"--network=host", "--env=GH_TOKEN"} {
		root := projectDir(t, map[string]string{"config.toml": dryRunProject + "\n[sandbox]\nrun_args = [\"" + arg + "\"]\n"})
		log := filepath.Join(t.TempDir(), "gh.log")

		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "dryrun", "FAKEGH_LOG="+log), "run")

		if code != 3 || !strings.Contains(stderr, strings.Split(arg, "=")[0]) {
			t.Fatalf("%s: exit code = %d, want 3 naming the flag; stderr: %s", arg, code, stderr)
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatalf("%s: gh ran although the Run can't start", arg)
		}
	}
}
