package tracker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

func TestARedactedTrackerNeverWritesASecret(t *testing.T) {
	inner := fake.New("ready", fake.Issue{Number: 1, Title: "A", Labels: []string{"ready"}}, fake.Issue{Number: 2, Title: "B", Labels: []string{"ready"}})
	tr := tracker.Redacted(inner, func(s string) string { return strings.ReplaceAll(s, "hunter2", "***") })
	ctx := context.Background()

	if err := tr.Comment(ctx, fake.ID(1), "the password is hunter2"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(ctx, fake.ID(2), "done with hunter2"); err != nil {
		t.Fatal(err)
	}

	for _, w := range inner.Writes() {
		if strings.Contains(w.Text, "hunter2") || !strings.Contains(w.Text, "***") {
			t.Errorf("write %+v, want the secret redacted", w)
		}
	}
	if len(inner.Writes()) != 2 {
		t.Errorf("writes = %+v, want the comment and the close", inner.Writes())
	}
}
