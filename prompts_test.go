package main

import (
	"strings"
	"testing"
)

func TestCheckFailsOnAnUnknownPlaceholder(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{
		"config.toml":          "version = 1\n",
		"prompts/review.md":    "# Review\n\n{{commands.test}}\n\n{{standards}}\n",
		"prompts/implement.md": "# Implement\n\n{{commands}}\n",
	})

	_, stderr, code := runIn(t, bin, root, "check")

	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr: %s", code, stderr)
	}
	want := "error: .kraftwerk/prompts/review.md: unknown placeholder {{commands.test}}; only {{commands}} and {{standards}} exist"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "implement.md") {
		t.Errorf("stderr blames implement.md, which is fine:\n%s", stderr)
	}
}

func TestCheckWarnsAboutAPromptFileWithoutAMarker(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{
		"config.toml":       "version = 1\n",
		"prompts/merge.md":  "# Our merge\n",
		"prompts/review.md": "<!-- kraftwerk: based on the built-in review prompt v1; this line isn't sent to the agent -->\n# Ours\n",
	})

	_, stderr, code := runIn(t, bin, root, "check")

	want := "warning: .kraftwerk/prompts/merge.md: no marker line names the built-in it is based on"
	if code != 0 || !strings.Contains(stderr, want) {
		t.Fatalf("exit code = %d, want 0 with %q; stderr: %s", code, want, stderr)
	}
	if strings.Contains(stderr, "review.md") {
		t.Errorf("stderr warns about review.md, which is up to date:\n%s", stderr)
	}
}

func TestARunWithAnUnknownPlaceholderStopsBeforeAnyStep(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{
		"config.toml":          "version = 1\n",
		"config.local.toml":    "[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n",
		"prompts/implement.md": "{{tests}}\n",
	})

	_, stderr, code := runIn(t, bin, root, "run")

	if code != 3 || !strings.Contains(stderr, "implement.md: unknown placeholder {{tests}}") {
		t.Fatalf("exit code = %d, want 3 naming the placeholder; stderr: %s", code, stderr)
	}
}

func TestPromptsDiffShowsHowTheBuiltInChangedSinceTheFilesVersion(t *testing.T) {
	bin := buildKraftwerk(t, "")

	t.Run("without a file the built-in is used", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		stdout, stderr, code := runIn(t, bin, root, "prompts", "diff", "implement")
		if code != 0 || !strings.Contains(stdout, "no .kraftwerk/prompts/implement.md, so the built-in implement prompt (v1) is used as it is") {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
		}
	})

	t.Run("a file based on the latest built-in is up to date", func(t *testing.T) {
		root := projectDir(t, map[string]string{
			"config.toml":       "version = 1\n",
			"prompts/review.md": "<!-- kraftwerk: based on the built-in review prompt v1; this line isn't sent to the agent -->\n# Ours\n",
		})
		stdout, _, code := runIn(t, bin, root, "prompts", "diff", "review")
		if code != 0 || !strings.Contains(stdout, "is based on the latest built-in review prompt (v1)") {
			t.Fatalf("exit code = %d, stdout = %q", code, stdout)
		}
	})

	t.Run("a file without a marker is compared with the built-in", func(t *testing.T) {
		root := projectDir(t, map[string]string{
			"config.toml":      "version = 1\n",
			"prompts/merge.md": "# Resolve\n\nOur own way.\n",
		})
		stdout, _, code := runIn(t, bin, root, "prompts", "diff", "merge")
		if code != 0 {
			t.Fatalf("exit code = %d, stdout = %q", code, stdout)
		}
		for _, want := range []string{"--- .kraftwerk/prompts/merge.md", "+++ built-in merge prompt v1", "-Our own way.", "+{{commands}}", "<!-- kraftwerk: based on the built-in merge prompt v1"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("an unknown Step exits 3", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		_, stderr, code := runIn(t, bin, root, "prompts", "diff", "deploy")
		if code != 3 || !strings.Contains(stderr, `unknown Step "deploy"; it is one of implement, review, merge`) {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr)
		}
	})
}
