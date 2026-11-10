package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The deprecated keys these tests use exist only in the kwkfake build; see
// internal/config/fakedeprecations.go.

const noSandboxLocal = "[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n"

func readConfig(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".kraftwerk", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFactoryPinMismatchFailsValidationAndNamesTheFix(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\nfactory = \"2\"\n"})

	stdout, stderr, code := runIn(t, bin, root, checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, stdout, stderr)
	}
	mustContain(t, "stderr", stderr, "factory", "1.2.0", "`kraftwerk update --major`")
}

func TestARenamedKeyKeepsWorkingWithADeprecationWarning(t *testing.T) {
	bin := buildTestKraftwerk(t)
	root := projectDir(t, map[string]string{
		"config.toml":       "version = 1\n",
		"config.local.toml": "[sandbox]\ntype = \"none\"\ni_accept_unsandboxed_risk = true\n",
	})

	stdout, stderr, code := runIn(t, bin, root, "check")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, "no Sandbox to probe")
	mustContain(t, "stderr", stderr, "warning: .kraftwerk/config.local.toml: sandbox.type", "`sandbox.kind`", "`kraftwerk migrate`")
}

func TestARemovedKeyIsIgnoredWithADeprecationWarning(t *testing.T) {
	bin := buildTestKraftwerk(t)
	root := projectDir(t, map[string]string{"config.toml": "version = 1\nlegacy_flag = true\n", "config.local.toml": noSandboxLocal})

	stdout, stderr, code := runIn(t, bin, root, "check")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stderr", stderr, "warning: .kraftwerk/config.toml: legacy_flag", "it has done nothing since 1.1", "`kraftwerk migrate`")
}

func TestARenamedKeySetAlongsideItsNewNameLosesToIt(t *testing.T) {
	bin := buildTestKraftwerk(t)
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": "[sandbox]\nkind = \"none\"\ntype = \"native\"\ni_accept_unsandboxed_risk = true\n"})

	stdout, stderr, code := runIn(t, bin, root, "check")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, "no Sandbox to probe")
	mustContain(t, "stderr", stderr, "sandbox.type", "ignored")
}

func TestMigrateRewritesTheFileKeepingCommentsAndReportsWhatChanged(t *testing.T) {
	bin := buildTestKraftwerk(t)
	root := projectDir(t, map[string]string{
		"config.toml": `# The team's config.
version = 1

# Which model to use.
implement_model = "m" # trailing

legacy_flag = true

[sandbox] # how we isolate
# the kind
type = "container" # inline note
run_args = [
  "--init",
]

[auth]
# which region
region = "eu-west-1"

[steps.implement]
effort = "high"
`,
		"config.local.toml": "[sandbox]\ntype = \"none\"\ni_accept_unsandboxed_risk = true\n",
	})

	stdout, stderr, code := runIn(t, bin, root, "migrate")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	want := `# The team's config.
version = 1

# Which model to use.


[sandbox] # how we isolate
# the kind
kind = "container" # inline note
run_args = [
  "--init",
]

[harnesses.claude_code]
# which region
region = "eu-west-1"

[steps.implement]
model = "m" # trailing
effort = "high"
`
	if got := readConfig(t, root, "config.toml"); got != want {
		t.Errorf("config.toml:\n%s\nwant:\n%s", got, want)
	}
	if got, want := readConfig(t, root, "config.local.toml"), noSandboxLocal; got != want {
		t.Errorf("config.local.toml:\n%s\nwant:\n%s", got, want)
	}
	mustContain(t, "stdout", stdout,
		".kraftwerk/config.toml: renamed `implement_model` to `steps.implement.model`",
		".kraftwerk/config.toml: renamed `sandbox.type` to `sandbox.kind`",
		".kraftwerk/config.toml: renamed `auth` to `harnesses.claude_code`",
		".kraftwerk/config.toml: removed `legacy_flag` (it has done nothing since 1.1)",
		".kraftwerk/config.local.toml: renamed `sandbox.type` to `sandbox.kind`")

	stdout, stderr, code = runIn(t, bin, root, "migrate")
	if code != exitOK || strings.Contains(stdout, "renamed") {
		t.Fatalf("second migrate: exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, ".kraftwerk/config.toml: nothing to change")
}

func TestMigrateHandlesTheOtherWaysToWriteAKey(t *testing.T) {
	cases := []struct{ name, before, after string }{
		{"dotted key", "version = 1\nsandbox.type = \"native\"\n", "version = 1\nsandbox.kind = \"native\"\n"},
		{"new table for a moved key", "version = 1\nimplement_model = \"m\"\n", "version = 1\n\n[steps.implement]\nmodel = \"m\"\n"},
		{"new name already set", "version = 1\n[sandbox]\nkind = \"native\"\ntype = \"container\"\n", "version = 1\n[sandbox]\nkind = \"native\"\n"},
		{"file with no final newline", "version = 1\nlegacy_flag = true", "version = 1\n"},
		{"windows line endings", "version = 1\r\n[sandbox]\r\ntype = \"native\"\r\n", "version = 1\r\n[sandbox]\r\nkind = \"native\"\r\n"},
		{"multi-line value", "version = 1\nimplement_model = \"\"\"\nm\"\"\"\n[steps.implement]\neffort = \"high\"\n", "version = 1\n[steps.implement]\nmodel = \"\"\"\nm\"\"\"\neffort = \"high\"\n"},
		{"a look-alike inside a string stays", "version = 1\n[commands]\nlegacy_flag = \"legacy_flag = true\"\n", "version = 1\n[commands]\nlegacy_flag = \"legacy_flag = true\"\n"},
		{"a look-alike in a list of tables stays", "version = 1\n[[hooks.run_start]]\nrun = \"legacy_flag = true\"\n", "version = 1\n[[hooks.run_start]]\nrun = \"legacy_flag = true\"\n"},
	}
	bin := buildTestKraftwerk(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := projectDir(t, map[string]string{"config.toml": c.before})

			stdout, stderr, code := runIn(t, bin, root, "migrate")

			if code != exitOK {
				t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if got := readConfig(t, root, "config.toml"); got != c.after {
				t.Errorf("config.toml:\n%q\nwant:\n%q", got, c.after)
			}
		})
	}
}

func TestMigrateLeavesEveryFileAloneWhenOneCantBeRewritten(t *testing.T) {
	bin := buildTestKraftwerk(t)
	project := "version = 1\nlegacy_flag = true\n"
	local := "sandbox = { type = \"none\", i_accept_unsandboxed_risk = true }\n"
	root := projectDir(t, map[string]string{"config.toml": project, "config.local.toml": local})

	stdout, stderr, code := runIn(t, bin, root, "migrate")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, stdout, stderr)
	}
	mustContain(t, "stderr", stderr, ".kraftwerk/config.local.toml", "`sandbox.type`", "by hand")
	if got := readConfig(t, root, "config.toml"); got != project {
		t.Errorf("config.toml changed:\n%s", got)
	}
	if got := readConfig(t, root, "config.local.toml"); got != local {
		t.Errorf("config.local.toml changed:\n%s", got)
	}
}

func TestMigrateSaysWhatItChangedWhenALaterFileCantBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode doesn't stop writes on Windows")
	}
	bin := buildTestKraftwerk(t)
	root := projectDir(t, map[string]string{"config.toml": "version = 1\nlegacy_flag = true\n", "config.local.toml": "legacy_flag = true\n"})
	// A directory in the way of the second file's temporary name.
	if err := os.Mkdir(filepath.Join(root, ".kraftwerk", "config.local.toml.migrating"), 0o700); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runIn(t, bin, root, "migrate")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, ".kraftwerk/config.toml: removed `legacy_flag`")
	mustContain(t, "stderr", stderr, "config.local.toml", "can't be written")
	if strings.Contains(stderr, "nothing was changed") {
		t.Errorf("stderr claims nothing changed, but config.toml did: %s", stderr)
	}
}

func TestMigrateRefusesANewerConfigVersionAndABrokenFile(t *testing.T) {
	bin := buildTestKraftwerk(t)
	cases := []struct{ name, config, want string }{
		{"newer version", "version = 99\nlegacy_flag = true\n", "`kraftwerk update --major`"},
		{"broken TOML", "version = = 1\n", "line 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := projectDir(t, map[string]string{"config.toml": c.config})

			stdout, stderr, code := runIn(t, bin, root, "migrate")

			if code != exitConfigError {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, stdout, stderr)
			}
			mustContain(t, "stderr", stderr, c.want)
			if got := readConfig(t, root, "config.toml"); got != c.config {
				t.Errorf("config.toml changed:\n%s", got)
			}
		})
	}
}

func TestMigrateWithoutAProjectSaysToRunInit(t *testing.T) {
	bin := buildTestKraftwerk(t)

	_, stderr, code := runIn(t, bin, t.TempDir(), "migrate")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "`kraftwerk init`")
}
