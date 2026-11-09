package factory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/hook"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// recordedSandbox notes the commands it is asked to run. Each just passes,
// or fails if it says `exit 1`, through a git call that does so on every OS.
type recordedSandbox struct {
	req      sandbox.Request
	services []sandbox.Service

	mu        sync.Mutex
	lines     []string
	kills     int
	destroyed int
	killedCtx bool
}

func (s *recordedSandbox) Exec(argv, env []string) ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, argv[len(argv)-1])
	result := "--version"
	if strings.Contains(argv[len(argv)-1], "exit 1") {
		result = "--no-such-flag"
	}
	return []string{"git", result}, append(os.Environ(), env...)
}

func (s *recordedSandbox) KillAll(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kills++
	return nil
}

func (s *recordedSandbox) Services() []sandbox.Service { return s.services }

func (s *recordedSandbox) Destroy(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyed++
	s.killedCtx = ctx.Err() != nil
	return nil
}

// sandboxes hands out recordedSandboxes and remembers them.
type sandboxes struct {
	mu   sync.Mutex
	made []*recordedSandbox
	// services, if set, says what each Sandbox can reach, by its name.
	services func(name string) []sandbox.Service
}

func (m *sandboxes) create(_ context.Context, req sandbox.Request) (sandbox.Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &recordedSandbox{req: req}
	if m.services != nil {
		s.services = m.services(req.Name)
	}
	m.made = append(m.made, s)
	return s, nil
}

func (m *sandboxes) names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for _, s := range m.made {
		names = append(names, s.req.Name)
	}
	return names
}

func TestATicketsStepsVerifyAndSandboxHooksRunInItsOneSandbox(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var m sandboxes
	var stepIn []sandbox.Sandbox
	commit := commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")
	opts := options(f, tr, func(ctx context.Context, s factory.Step) harness.Outcome {
		stepIn = append(stepIn, s.Sandbox)
		return commit(ctx, s)
	})
	opts.Sandboxes = m.create
	opts.Verify = []verify.Command{{Name: "test", Line: "test -f cart.go"}}
	opts.Hooks.SandboxCreated = []hook.Entry{
		{Commands: []string{"echo in-the-sandbox"}, Where: hook.Sandbox},
		{Commands: []string{"echo on-the-host"}, Where: hook.Host},
	}

	res := factory.Run(context.Background(), opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if len(m.made) != 1 {
		t.Fatalf("made %d Sandboxes (%q), want 1", len(m.made), m.names())
	}
	sb := m.made[0]
	if !strings.Contains(sb.req.Worktree, "ticket-2") {
		t.Errorf("the Sandbox holds %s, want the Ticket's worktree", sb.req.Worktree)
	}
	if len(stepIn) == 0 || stepIn[0] != sandbox.Sandbox(sb) {
		t.Errorf("the Steps ran in %v, want the Ticket's Sandbox", stepIn)
	}
	want := []string{"echo in-the-sandbox", "test -f cart.go"}
	if !slices.Equal(sb.lines, want) {
		t.Errorf("the Sandbox ran %q, want %q", sb.lines, want)
	}
	if sb.destroyed != 1 {
		t.Errorf("Destroy ran %d times, want 1", sb.destroyed)
	}
	if !issue(t, tr, 2).Closed {
		t.Error("#2 wasn't closed")
	}
}

func TestASandboxIsDestroyedWhenTheRunIsKilled(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var m sandboxes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := options(f, tr, func(context.Context, factory.Step) harness.Outcome {
		cancel()
		return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "killed"}
	})
	opts.Sandboxes = m.create

	factory.Run(ctx, opts)

	if len(m.made) != 1 || m.made[0].destroyed != 1 {
		t.Fatalf("made %q, want one destroyed Sandbox", m.names())
	}
	if m.made[0].killedCtx {
		t.Error("Destroy got the Run's dead context, so it can't reach the engine")
	}
}

func TestASandboxThatCannotBeCreatedFailsTheTicketAlone(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	ran := false
	opts := options(f, tr, func(context.Context, factory.Step) harness.Outcome {
		ran = true
		return harness.Outcome{}
	})
	opts.Sandboxes = func(context.Context, sandbox.Request) (sandbox.Sandbox, error) {
		return nil, errors.New("the engine went away")
	}

	res := factory.Run(context.Background(), opts)

	if ran {
		t.Error("a Step ran without its Sandbox")
	}
	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindFactory || !strings.Contains(res.Failed[0].Reason, "the engine went away") {
		t.Errorf("Failed = %+v, want the Factory's own failure", res.Failed)
	}
	if got := issue(t, tr, 2); got.Closed || !slices.Contains(got.Labels, ready) {
		t.Errorf("#2 = %+v, want it left as it was", got)
	}
}

func TestAFailedVerifyClearsTheSandbox(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var m sandboxes
	opts := options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."))
	opts.Sandboxes = m.create
	opts.CannotDoLabel = "needs-investigation"
	opts.Verify = []verify.Command{{Name: "test", Line: "exit 1"}}

	factory.Run(context.Background(), opts)

	if len(m.made) != 1 || m.made[0].kills == 0 {
		t.Fatalf("made %q; verify's failure must clear the Sandbox", m.names())
	}
}

func TestTheMergeStepAndTheMergedVerifyEachGetTheirOwnSandbox(t *testing.T) {
	var m sandboxes
	var mergeIn sandbox.Sandbox
	var c *conflict
	resolve := resolves(t, "from both\n")
	c = newConflict(t, func(ctx context.Context, s factory.Step) harness.Outcome {
		mergeIn = s.Sandbox
		return resolve(ctx, s)
	})
	c.opts.Sandboxes = m.create
	c.opts.Verify = []verify.Command{{Name: "test", Line: "exit 0"}}

	res := factory.Run(context.Background(), c.opts)

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	got := m.names()
	slices.Sort(got)
	want := []string{"build-2", "build-3", "merge-3", "verify-3"}
	if !slices.Equal(got, want) {
		t.Errorf("Sandboxes = %q, want %q", got, want)
	}
	for _, s := range m.made {
		if s.destroyed != 1 {
			t.Errorf("%s was destroyed %d times, want once", s.req.Name, s.destroyed)
		}
		if s.req.Name == "merge-3" && sandbox.Sandbox(s) != mergeIn {
			t.Errorf("the merge Step ran in %v, want its own Sandbox", mergeIn)
		}
	}
}

func TestOldSandboxesAreClearedBeforeWorktreesAndCredentials(t *testing.T) {
	f := newFixture(t)
	leftover := filepath.Join(f.work, factory.StateDir, "worktrees", "ticket-9")
	write(t, filepath.Join(leftover, "file.txt"), "from a crashed Run\n")
	tr := fake.New(ready, cartSpec()...)
	opts := options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."))
	var order []string
	opts.ClearSandboxes = func(context.Context) error {
		if _, err := os.Stat(leftover); err != nil {
			t.Error("the crashed Run's worktree was removed while its Sandbox still held it")
		}
		order = append(order, "clear")
		return nil
	}
	opts.Preflight = func(context.Context) error {
		order = append(order, "preflight")
		return errors.New("the credential is bad")
	}

	res := factory.Run(context.Background(), opts)

	if res.Stopped == nil {
		t.Fatal("the Run didn't stop")
	}
	if want := []string{"clear", "preflight"}; !slices.Equal(order, want) {
		t.Errorf("order = %q, want %q", order, want)
	}
}

func TestSandboxesThatCannotBeClearedStopTheRun(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	opts := options(f, tr, func(context.Context, factory.Step) harness.Outcome {
		t.Error("a Step ran")
		return harness.Outcome{}
	})
	opts.ClearSandboxes = func(context.Context) error { return errors.New("no engine") }

	res := factory.Run(context.Background(), opts)

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "no engine") {
		t.Errorf("Stopped = %v, want the engine's error", res.Stopped)
	}
}

func TestAGitDirectoryPlantedInTheProjectIsNeverUsedByTheHost(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(f.work, "app")
	write(t, filepath.Join(sub, ".kraftwerk", "config.toml"), "version = 1\n")
	write(t, filepath.Join(sub, ".kraftwerk", ".gitignore"), "state/\nlogs/\n.env\n")
	git(t, f.work, "add", ".")
	git(t, f.work, "commit", "-q", "-m", "A project in a subdirectory")
	git(t, f.work, "push", "-q", "origin", "main")
	marker := filepath.Join(t.TempDir(), "ran-on-the-host")
	tr := fake.New(ready, cartSpec()...)
	commit := commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")
	opts := options(f, tr, func(ctx context.Context, s factory.Step) harness.Outcome {
		out := commit(ctx, s)
		planted := filepath.Join(s.Dir, ".git")
		git(t, t.TempDir(), "init", "-q", "--bare", planted)
		git(t, planted, "config", "core.fsmonitor", "touch "+marker+" #")
		return out
	})
	opts.Root = sub
	opts.CannotDoLabel = "needs-investigation"

	res := factory.Run(context.Background(), opts)

	if _, err := os.Stat(marker); err == nil {
		t.Error("the host ran what the planted .git says")
	}
	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindProtectedPath {
		t.Errorf("Failed = %+v, want a rejected Protected path", res.Failed)
	}
}

func TestEachStepIsToldTheServicesOfTheSandboxItRunsIn(t *testing.T) {
	var m sandboxes
	m.services = func(name string) []sandbox.Service {
		return []sandbox.Service{{Name: "postgres", Addresses: []string{"addr-of-" + name}}}
	}
	prompts := map[string]string{}
	var c *conflict
	resolve := resolves(t, "from both\n")
	c = newConflict(t, func(ctx context.Context, s factory.Step) harness.Outcome {
		prompts[s.Name] = s.Prompt
		return resolve(ctx, s)
	})
	c.opts.Sandboxes = m.create

	if res := factory.Run(context.Background(), c.opts); res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if p := prompts[factory.Merge]; !strings.Contains(p, "addr-of-merge-3") {
		t.Errorf("the merge prompt doesn't name its own Sandbox's Service:\n%s", p)
	}
}

func TestImplementAndReviewAreToldTheirSandboxesServices(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var m sandboxes
	m.services = func(string) []sandbox.Service {
		return []sandbox.Service{{Name: "postgres", Addresses: []string{"postgres:5432"}}}
	}
	var got []string
	commit := commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")
	opts := options(f, tr, func(ctx context.Context, s factory.Step) harness.Outcome {
		got = append(got, s.Name)
		if !strings.Contains(s.Prompt, "postgres:5432") {
			t.Errorf("the %s prompt doesn't list the Service:\n%s", s.Name, s.Prompt)
		}
		return commit(ctx, s)
	})
	opts.Sandboxes = m.create

	if res := factory.Run(context.Background(), opts); res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	if !slices.Contains(got, factory.Implement) {
		t.Errorf("Steps run: %q", got)
	}
}
