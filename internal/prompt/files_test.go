package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// withBuiltins replaces the embedded built-ins for one test.
func withBuiltins(t *testing.T, fsys fstest.MapFS) {
	t.Helper()
	old := builtins
	builtins = loadBuiltins(fsys)
	t.Cleanup(func() { builtins = old })
}

func writeFile(t *testing.T, root, step, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(Dir), step+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAMissingFileMeansTheBuiltInPart(t *testing.T) {
	parts, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range Steps {
		p := parts[step]
		if p.Exists || p.Text == "" || p.Text != Builtin(step, Latest(step)) {
			t.Errorf("%s = %+v, want the built-in part", step, p)
		}
		if p.Path != ".fliesswerk/prompts/"+step+".md" {
			t.Errorf("%s path = %q", step, p.Path)
		}
	}
	if !strings.Contains(parts["review"].Text, "{{standards}}") || !strings.Contains(parts["implement"].Text, "{{commands}}") {
		t.Error("the built-in parts lack their placeholders")
	}
}

func TestEveryBuiltInPartUsesOnlyKnownPlaceholders(t *testing.T) {
	for _, step := range Steps {
		for v := 1; v <= Latest(step); v++ {
			if u := Unknown(Builtin(step, v)); len(u) > 0 {
				t.Errorf("built-in %s v%d has unknown placeholders %q", step, v, u)
			}
		}
	}
}

func TestAProjectFileReplacesTheBuiltInAndItsMarkerLineIsNotSent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "review", "<!-- fliesswerk: based on the built-in review prompt v1; this line isn't sent to the agent -->\n\n# Our review\n")

	parts, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := parts["review"]
	if !p.Exists || p.Based != 1 || strings.TrimSpace(p.Text) != "# Our review" {
		t.Errorf("review = %+v, want the file's text based on v1", p)
	}
}

func TestAFileBasedOnAnOlderBuiltInIsImproved(t *testing.T) {
	withBuiltins(t, fstest.MapFS{
		"builtin/implement.md":            {Data: []byte("Explore.\nTest first.\nCommit.\n")},
		"builtin/history/implement.v1.md": {Data: []byte("Explore.\nCommit.\n")},
		"builtin/review.md":               {Data: []byte("Review.\n")},
		"builtin/merge.md":                {Data: []byte("Merge.\n")},
	})
	if Latest("implement") != 2 || Builtin("implement", 1) != "Explore.\nCommit.\n" {
		t.Fatalf("the history isn't read: latest %d, v1 %q", Latest("implement"), Builtin("implement", 1))
	}
	root := t.TempDir()
	writeFile(t, root, "implement", "<!-- fliesswerk: based on the built-in implement prompt v1; this line isn't sent to the agent -->\nExplore.\nOur rule.\nCommit.\n")
	writeFile(t, root, "review", "<!-- fliesswerk: based on the built-in review prompt v1; this line isn't sent to the agent -->\nOur review.\n")
	writeFile(t, root, "merge", "Our merge, no marker.\n")

	parts, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !parts["implement"].Improved() {
		t.Error("implement, based on v1 of 2, isn't improved")
	}
	if parts["review"].Improved() || parts["merge"].Improved() {
		t.Error("an up-to-date file, or one without a marker, counts as improved")
	}
}

func TestUnifiedDiffShowsTheChangedLinesWithContext(t *testing.T) {
	got := UnifiedDiff("old", "a\nb\nc\nd\ne\nf\ng\nh\n", "new", "a\nb\nc\nd\nE\nf\ng\nh\ni\n")
	want := `--- old
+++ new
@@ -2,7 +2,8 @@
 b
 c
 d
-e
+E
 f
 g
 h
+i
`
	if got != want {
		t.Errorf("diff =\n%s\nwant\n%s", got, want)
	}
	if UnifiedDiff("old", "same\n", "new", "same\n") != "" {
		t.Error("equal texts have a diff")
	}
}

func TestAMarkerNamingAnotherStepIsNoMarker(t *testing.T) {
	root := t.TempDir()
	marked := "<!-- fliesswerk: based on the built-in review prompt v1; this line isn't sent to the agent -->\n# Ours\n"
	writeFile(t, root, "implement", marked)

	parts, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if p := parts["implement"]; p.Based != 0 || p.Text != marked {
		t.Errorf("implement = %+v, want no marker and the whole file", p)
	}
}

// builtinHashes pin every built-in version's text. A version, once out,
// never changes: to improve a built-in, move builtin/<step>.md to
// builtin/history/<step>.v<N>.md, write the new one, and add its hash
// here. That is how `check` knows that projects on the old one should
// look at it.
var builtinHashes = map[string][]string{
	"implement": {"ddaa8f6d83620fd2bf954360e7cb4927e8f63175211a7a92ccbff18b49901bd2"},
	"review":    {"98f04b4d8ea55b6d02ea09658ef99fd41cab6fa2751afb50047543b0f5d793c8"},
	"merge":     {"a2565db573e3c00c3b0fb1e0611eec731171b1bbe80927fecf59f3f4807b2b26"},
}

func TestNoBuiltInVersionChangesOnceItIsOut(t *testing.T) {
	for _, step := range Steps {
		if Latest(step) != len(builtinHashes[step]) {
			t.Errorf("%s has %d built-in versions, but %d are pinned", step, Latest(step), len(builtinHashes[step]))
			continue
		}
		for v := 1; v <= Latest(step); v++ {
			sum := sha256.Sum256([]byte(Builtin(step, v)))
			if got := hex.EncodeToString(sum[:]); got != builtinHashes[step][v-1] {
				t.Errorf("built-in %s v%d changed (sha256 %s); move the old text to builtin/history/%s.v%d.md and make the change v%d instead",
					step, v, got, step, v, v+1)
			}
		}
	}
}
