package factory_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/hook"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// stepFunc is what a test runs for a Step.
type stepFunc = func(context.Context, factory.Step) harness.Outcome

// mergeSeen is what the merge Step found when it started.
type mergeSeen struct {
	dir, status, head, mergeHead, specTip string
}

// conflict is a Run in which #2 and #3 write shared.txt their own ways and
// #3 reaches the merge queue second, so it is the one that conflicts.
type conflict struct {
	f      fixture
	tr     *fake.Tracker
	opts   factory.Options
	merges []factory.Step
	seen   []mergeSeen
	// implements counts #3's implement Steps.
	implements int
}

// newConflict sets up that Run, with merge as its merge Step. #3 commits
// its own way only the first time; implemented afresh, it adds a file of
// its own.
func newConflict(t *testing.T, merge stepFunc) *conflict {
	t.Helper()
	f := newFixture(t)
	c := &conflict{f: f, tr: fake.New(ready, ticketsOf("Write it one way", "Write it another way")...)}
	first := afterClosed(t, c.tr, 2, commitFile("shared.txt", "from 3\n"))
	steps := byTicket(map[string]stepFunc{
		"#2": commitFile("shared.txt", "from 2\n"),
		"#3": func(ctx context.Context, s factory.Step) harness.Outcome {
			c.implements++
			if c.implements == 1 {
				return first(ctx, s)
			}
			return commitFile("other.txt", "from 3\n")(ctx, s)
		},
	})
	c.opts = failOptions(f, c.tr, func(ctx context.Context, s factory.Step) harness.Outcome {
		if s.Name != factory.Merge {
			return steps(ctx, s)
		}
		c.merges = append(c.merges, s)
		c.seen = append(c.seen, mergeSeen{
			dir:       s.Dir,
			status:    git(t, s.Dir, "status", "--porcelain"),
			head:      git(t, s.Dir, "rev-parse", "HEAD"),
			mergeHead: git(t, s.Dir, "rev-parse", "MERGE_HEAD"),
			specTip:   git(t, f.work, "rev-parse", specBranch),
		})
		return merge(ctx, s)
	})
	c.opts.MaxParallel = 2
	return c
}

// resolves ends the merge with shared.txt holding content.
func resolves(t *testing.T, content string) stepFunc {
	t.Helper()
	return func(_ context.Context, s factory.Step) harness.Outcome {
		write(t, filepath.Join(s.Dir, "shared.txt"), content)
		git(t, s.Dir, "add", "shared.txt")
		git(t, s.Dir, "commit", "--no-edit")
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: "Kept both."}}
	}
}

func TestARealConflictIsResolvedByTheMergeStepInItsOwnWorktreeAndTheTicketCloses(t *testing.T) {
	c := newConflict(t, resolves(t, "from both\n"))

	res := factory.Run(context.Background(), c.opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if len(c.merges) != 1 || c.merges[0].Ticket.ID.Display != "#3" {
		t.Fatalf("merge Steps = %+v, want one, for #3", c.merges)
	}
	if c.implements != 1 {
		t.Errorf("#3 was implemented %d times, want once: the merge Step resolved it", c.implements)
	}
	seen := c.seen[0]
	if !strings.Contains(seen.status, "AA shared.txt") {
		t.Errorf("the merge Step found status %q, want shared.txt in conflict", seen.status)
	}
	if seen.head != seen.specTip {
		t.Errorf("the merge Step's HEAD = %s, want the Spec branch's tip %s", seen.head, seen.specTip)
	}
	if strings.Contains(seen.dir, "ticket-3") || strings.Contains(seen.dir, "spec-1") {
		t.Errorf("the merge Step ran in %s, want a worktree of its own", seen.dir)
	}
	i := issue(t, c.tr, 3)
	if !i.Closed || len(i.Comments) != 1 || !strings.Contains(i.Comments[0].Body, "Kept both.") {
		t.Errorf("#3 = %+v, want it closed, with the merge Step's summary in the closing comment", i)
	}
	tip := c.f.remoteRef(t, specBranch)
	if got := git(t, c.f.remote, "show", tip+":shared.txt"); got != "from both" {
		t.Errorf("shared.txt on the Spec branch = %q, want the resolution", got)
	}
	if parents := strings.Fields(git(t, c.f.remote, "rev-list", "--parents", "-n", "1", tip)); len(parents) != 3 || parents[2] != seen.mergeHead {
		t.Errorf("the Spec branch's tip and parents = %v, want a merge of #3's tip %s", parents, seen.mergeHead)
	}
	noWorkLeft(t, c.f)
}

func TestTheMergePromptHasTheConflictTheTicketsBehindItAndTheSpec(t *testing.T) {
	f := newFixture(t)
	issues := ticketsOf("Write it one way", "Write it another way", "Add an unrelated file")
	issues[1].Body, issues[2].Body, issues[3].Body = "Say one.", "Say another.", "Say nothing."
	tr := fake.New(ready, issues...)
	var got string
	steps := byTicket(map[string]stepFunc{
		"#2": commitFile("shared.txt", "from 2\n"),
		"#3": afterClosed(t, tr, 2, afterClosed(t, tr, 4, commitFile("shared.txt", "from 3\n"))),
		"#4": commitFile("unrelated.txt", "x\n"),
	})
	resolve := resolves(t, "from both\n")
	opts := failOptions(f, tr, func(ctx context.Context, s factory.Step) harness.Outcome {
		if s.Name == factory.Merge {
			got = s.Prompt
			return resolve(ctx, s)
		}
		return steps(ctx, s)
	})
	opts.MaxParallel = 3
	opts.Commands = []verify.Command{{Name: "test", Line: "go test ./..."}}

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 3 {
		t.Fatalf("result = %+v, want all three closed", res)
	}
	inOrder(t, got, "Resolve the merge of Ticket #3: Write it another way", "Say another.",
		"# The Spec", "A cart.", "- #2 Write it one way (closed)", "- #3 Write it another way (this Ticket)", "- #4 Add an unrelated file (closed)",
		"# Conflicting files", "`shared.txt`",
		"# Tickets merged since this Ticket started", "#2 Write it one way", "Say one.", "Add shared.txt",
		"# Resolve", "After resolving, run these", "go test ./...",
		"# Do not close the Ticket", "# Rules", "`.kraftwerk/`", "# Done", "cannot_do")
	if strings.Contains(got, "Say nothing.") {
		t.Errorf("the prompt names #4, whose commits didn't touch the conflicting file:\n%s", got)
	}
	if strings.Contains(got, "You are on the branch") {
		t.Errorf("the prompt says it is on a branch, but the merge Step's worktree is detached:\n%s", got)
	}
}

func TestAMergeStepThatDoesNotResolveTheConflictReQueuesTheTicket(t *testing.T) {
	done := harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: "Merged."}}
	for name, tc := range map[string]struct {
		step func(t *testing.T, s factory.Step) harness.Outcome
		want string
	}{
		"it reports cannot_do": {
			step: func(*testing.T, factory.Step) harness.Outcome {
				return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.CannotDo, Reason: "They exclude each other."}}
			},
			want: "They exclude each other.",
		},
		"it leaves the conflict as it was": {
			step: func(*testing.T, factory.Step) harness.Outcome { return done },
			want: "uncommitted",
		},
		"it stages the resolution without committing": {
			step: func(t *testing.T, s factory.Step) harness.Outcome {
				write(t, filepath.Join(s.Dir, "shared.txt"), "from both\n")
				git(t, s.Dir, "add", "shared.txt")
				return done
			},
			want: "uncommitted",
		},
		"it aborts the merge": {
			step: func(t *testing.T, s factory.Step) harness.Outcome {
				git(t, s.Dir, "merge", "--abort")
				return done
			},
			want: "didn't commit the merge",
		},
		"it commits conflict markers": {
			step: func(t *testing.T, s factory.Step) harness.Outcome {
				git(t, s.Dir, "add", "shared.txt")
				git(t, s.Dir, "commit", "--no-edit")
				return done
			},
			want: "conflict markers in shared.txt",
		},
		"it leaves an untracked file": {
			step: func(t *testing.T, s factory.Step) harness.Outcome {
				write(t, filepath.Join(s.Dir, "shared.txt"), "from both\n")
				git(t, s.Dir, "add", "shared.txt")
				git(t, s.Dir, "commit", "--no-edit")
				write(t, filepath.Join(s.Dir, "stray.txt"), "x\n")
				return done
			},
			want: "uncommitted",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := newConflict(t, func(_ context.Context, s factory.Step) harness.Outcome { return tc.step(t, s) })
			var log strings.Builder
			c.opts.Log = logTo(&log)

			res := factory.Run(context.Background(), c.opts)

			if res.Stopped != nil || len(res.Failed) != 0 {
				t.Fatalf("result = %+v, want no failures", res)
			}
			if c.implements != 2 {
				t.Errorf("#3 was implemented %d times, want twice", c.implements)
			}
			if !strings.Contains(log.String(), "re-queued") || !strings.Contains(log.String(), tc.want) {
				t.Errorf("the log doesn't say #3 was re-queued because of %q:\n%s", tc.want, log.String())
			}
			if !issue(t, c.tr, 3).Closed {
				t.Error("#3 wasn't closed")
			}
			if got := git(t, c.f.remote, "show", c.f.remoteRef(t, specBranch)+":shared.txt"); got != "from 2" {
				t.Errorf("shared.txt on the Spec branch = %q, want #2's", got)
			}
			noWorkLeft(t, c.f)
		})
	}
}

func TestAMergeStepThatChangesAProtectedPathHandsTheTicketBackWithoutARetry(t *testing.T) {
	c := newConflict(t, func(_ context.Context, s factory.Step) harness.Outcome {
		write(t, filepath.Join(s.Dir, "shared.txt"), "from both\n")
		write(t, filepath.Join(s.Dir, ".kraftwerk", "config.toml"), "max_parallel = 9\n")
		git(t, s.Dir, "add", "shared.txt", ".kraftwerk/config.toml")
		git(t, s.Dir, "commit", "--no-edit")
		// A merge that is unresolved as well still gets no retry.
		write(t, filepath.Join(s.Dir, "stray.txt"), "x\n")
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: "Merged."}}
	})

	res := factory.Run(context.Background(), c.opts)

	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindProtectedPath {
		t.Fatalf("failed = %+v, want #3's protected path", res.Failed)
	}
	if c.implements != 1 {
		t.Errorf("#3 was implemented %d times, want once: a Protected path gets no retry", c.implements)
	}
	handedBack(t, c.tr, 3, ".kraftwerk/config.toml")
	if got := git(t, c.f.remote, "show", c.f.remoteRef(t, specBranch)+":shared.txt"); got != "from 2" {
		t.Errorf("shared.txt on the Spec branch = %q, want #2's", got)
	}
	noWorkLeft(t, c.f)
}

func TestSandboxCreatedRunsInTheMergeStepsWorktreeBeforeTheStep(t *testing.T) {
	var ranBefore bool
	resolve := resolves(t, "from both\n")
	c := newConflict(t, func(ctx context.Context, s factory.Step) harness.Outcome {
		_, err := os.Stat(filepath.Join(s.Dir, "hook-ran"))
		ranBefore = err == nil
		return resolve(ctx, s)
	})
	// Each worktree gets its own marker: Hooks of Tickets running at once
	// can't share a file, since cmd.exe's >> holds it open exclusively. It is
	// ignored, so that it doesn't count against the merge's clean tree.
	write(t, filepath.Join(c.f.work, ".git", "info", "exclude"), "hook-ran\n")
	c.opts.Hooks.SandboxCreated = []hook.Entry{{Where: hook.Sandbox, Commands: []string{"git rev-parse --show-toplevel > hook-ran"}, Env: os.Environ()}}

	res := factory.Run(context.Background(), c.opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if !ranBefore {
		t.Error("sandbox_created hadn't run in the merge Step's worktree when the Step started")
	}
}

func TestAnAuthErrorInTheMergeStepStopsTheRun(t *testing.T) {
	c := newConflict(t, func(context.Context, factory.Step) harness.Outcome {
		return harness.Outcome{Kind: harness.Failed, Class: harness.Auth, Message: "bad key"}
	})

	res := factory.Run(context.Background(), c.opts)

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "#3 merge") || !strings.Contains(res.Stopped.Error(), "auth error") {
		t.Fatalf("stopped = %v, want #3's merge's auth error", res.Stopped)
	}
	if c.implements != 1 || issue(t, c.tr, 3).Closed || len(issue(t, c.tr, 3).Comments) != 0 {
		t.Errorf("implemented %d times, #3 = %+v, want it left as it was", c.implements, issue(t, c.tr, 3))
	}
	if got := git(t, c.f.work, "worktree", "list", "--porcelain"); strings.Contains(got, "merge-3") {
		t.Errorf("the merge worktree is left:\n%s", got)
	}
}

// inSubdirectory makes the conflict's project live in sub/ of its checkout.
func inSubdirectory(t *testing.T, c *conflict) {
	t.Helper()
	write(t, filepath.Join(c.f.work, "sub", ".kraftwerk", ".gitignore"), "state/\n")
	git(t, c.f.work, "add", ".")
	git(t, c.f.work, "commit", "-q", "-m", "Add the project in sub/")
	git(t, c.f.work, "push", "-q", "origin", "main")
	c.opts.Root = filepath.Join(c.f.work, "sub")
}

func TestConflictMarkersAreFoundInAProjectInASubdirectory(t *testing.T) {
	c := newConflict(t, func(_ context.Context, s factory.Step) harness.Outcome {
		git(t, s.Dir, "add", "shared.txt")
		git(t, s.Dir, "commit", "--no-edit")
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: "Merged."}}
	})
	inSubdirectory(t, c)
	var log strings.Builder
	c.opts.Log = logTo(&log)

	factory.Run(context.Background(), c.opts)

	if c.implements != 2 || !strings.Contains(log.String(), "conflict markers in sub/shared.txt") {
		t.Errorf("#3 was implemented %d times, want twice, for its conflict markers:\n%s", c.implements, log.String())
	}
}

func TestTheMergePromptNamesTheOtherTicketInAProjectInASubdirectory(t *testing.T) {
	c := newConflict(t, resolves(t, "from both\n"))
	inSubdirectory(t, c)

	res := factory.Run(context.Background(), c.opts)

	if res.Stopped != nil || len(res.Failed) != 0 || len(c.merges) != 1 {
		t.Fatalf("result = %+v, merge Steps = %d, want one and no failures", res, len(c.merges))
	}
	inOrder(t, c.merges[0].Prompt, "`sub/shared.txt`", "## #2 Write it one way")
}
