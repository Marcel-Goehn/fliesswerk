package factory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/factory"
	"github.com/Marcel-Goehn/fliesswerk/internal/hook"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

func TestSharedServicesRunOnceFromAWorktreeOfTheBaseBranch(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, ticketsOf("Cart", "Totals")...)
	var m sandboxes
	downs := 0
	var starts []sandbox.Request
	var checkedOut, base string
	opts := options(f, tr, commitFile("t.go", "package x\n"))
	opts.Sandboxes = m.create
	opts.SharedServices = func(_ context.Context, req sandbox.Request) (func(context.Context) error, error) {
		starts = append(starts, req)
		checkedOut = git(t, req.Worktree, "rev-parse", "HEAD")
		base = git(t, f.remote, "rev-parse", "main")
		return func(context.Context) error {
			downs++
			if _, err := os.Stat(req.Worktree); err != nil {
				t.Errorf("the worktree went before the Services did: %v", err)
			}
			return nil
		}, nil
	}

	res := factory.Run(context.Background(), opts)

	if res.Stopped != nil || len(res.Closed) == 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(starts) != 1 || starts[0].Name != "shared" {
		t.Fatalf("started %+v, want once, named shared", starts)
	}
	if checkedOut != base {
		t.Errorf("the worktree is at %s, want the base branch's %s", checkedOut, base)
	}
	if downs != 1 {
		t.Errorf("removed %d times, want 1", downs)
	}
	if _, err := os.Stat(starts[0].Worktree); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree is still there: %v", err)
	}
}

func TestSharedServicesStartAfterRunStartAndGoAfterRunEnd(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	lines := &lineLog{}
	opts := options(f, tr, commitFile("cart.go", "package cart\n"))
	opts.Hooks.RunStart = []hook.Entry{{Commands: []string{"echo start"}, Where: hook.Host}}
	opts.Hooks.RunEnd = []hook.Entry{{Commands: []string{"echo end"}, Where: hook.Host}}
	opts.Log = lines.add
	var startedHook, endedHook bool
	opts.SharedServices = func(context.Context, sandbox.Request) (func(context.Context) error, error) {
		startedHook = lines.has("run_start: started")
		return func(context.Context) error {
			endedHook = lines.has("run_end: started")
			return nil
		}, nil
	}

	factory.Run(context.Background(), opts)

	if !startedHook {
		t.Error("the shared Services started before the run_start Hook")
	}
	if !endedHook {
		t.Error("the shared Services went before the run_end Hook")
	}
}

func TestSharedServicesThatFailToStartStopTheRunAndAreCleanedUp(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	stopped := 0
	opts := options(f, tr, commitFile("cart.go", "package cart\n"))
	opts.SharedServices = func(context.Context, sandbox.Request) (func(context.Context) error, error) {
		return func(context.Context) error { stopped++; return nil }, errors.New("the image doesn't exist")
	}

	res := factory.Run(context.Background(), opts)

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "the image doesn't exist") {
		t.Errorf("stopped = %v, want the Services' error", res.Stopped)
	}
	if res.Iterations != 0 {
		t.Errorf("Iterations = %d, want none", res.Iterations)
	}
	if stopped != 1 {
		t.Errorf("stop ran %d times, want 1", stopped)
	}
	noWorkLeft(t, f)
}

func TestAHookOnTheHostIsToldItsSandbox(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var m sandboxes
	line := `echo sandbox is $FLIESSWERK_SANDBOX`
	if runtime.GOOS == "windows" {
		line = `echo sandbox is %FLIESSWERK_SANDBOX%`
	}
	lines := &lineLog{}
	opts := options(f, tr, commitFile("cart.go", "package cart\n"))
	opts.Sandboxes = m.create
	opts.Log = lines.add
	opts.Hooks.SandboxCreated = []hook.Entry{{Commands: []string{line}, Where: hook.Host, Env: os.Environ()}}

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !lines.has("sandbox is build-2") {
		t.Errorf("the Hook wasn't told: %q", lines.lines)
	}
}

// The worktree is where a project's compose file would be read from.
func TestTheSharedWorktreeHoldsTheProjectAtItsRelativeRoot(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var readme string
	opts := options(f, tr, commitFile("cart.go", "package cart\n"))
	opts.SharedServices = func(_ context.Context, req sandbox.Request) (func(context.Context) error, error) {
		data, err := os.ReadFile(filepath.Join(req.Worktree, req.Rel, "README.md"))
		if err != nil {
			t.Error(err)
		}
		readme = string(data)
		return nil, nil
	}

	factory.Run(context.Background(), opts)

	if readme != "hello\n" {
		t.Errorf("README.md = %q", readme)
	}
}

func TestSharedServicesThatCannotBeRemovedKeepTheWorktreeTheirRecordNames(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var worktree string
	opts := options(f, tr, commitFile("cart.go", "package cart\n"))
	opts.SharedServices = func(_ context.Context, req sandbox.Request) (func(context.Context) error, error) {
		worktree = req.Worktree
		return func(context.Context) error { return errors.New("the engine hiccuped") }, nil
	}

	factory.Run(context.Background(), opts)

	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("the next Run can't remove them without their compose file: %v", err)
	}
}
