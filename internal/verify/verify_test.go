package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

// The commands are portable between sh and cmd.exe: `echo` and `git`.

func TestEveryCommandPassing(t *testing.T) {
	dir := t.TempDir()

	f := verify.Run(context.Background(), nil, dir, os.Environ(), time.Minute, []verify.Command{
		{Name: "a", Line: "echo one"},
		{Name: "b", Line: "echo two"},
	})

	if f != nil {
		t.Fatalf("failure = %v, want none", f)
	}
}

func TestNoCommandsMeansNoChecks(t *testing.T) {
	if f := verify.Run(context.Background(), nil, t.TempDir(), os.Environ(), time.Minute, nil); f != nil {
		t.Fatalf("failure = %v, want none", f)
	}
}

func TestTheFirstFailingCommandStopsTheRestAndKeepsItsOutput(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")

	f := verify.Run(context.Background(), nil, dir, os.Environ(), time.Minute, []verify.Command{
		{Name: "ok", Line: "echo fine"},
		{Name: "test", Line: "git --no-such-flag"},
		{Name: "after", Line: "echo x > ran"},
	})

	if f == nil {
		t.Fatal("no failure, want `test` to fail")
	}
	if f.Command.Name != "test" {
		t.Errorf("failed command = %q, want test", f.Command.Name)
	}
	if !strings.Contains(f.Output, "no-such-flag") {
		t.Errorf("output = %q, want git's complaint about the flag", f.Output)
	}
	if !strings.Contains(f.Error(), "`test`") {
		t.Errorf("error = %q, want it to name the command", f.Error())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a command after the failing one ran")
	}
}

func TestCommandsRunInTheDirWithTheGivenEnv(t *testing.T) {
	dir := t.TempDir()
	env := append(os.Environ(), "FWK_VERIFY_PROBE=probe-value")
	line := "echo $FWK_VERIFY_PROBE > out.txt"
	if runtime.GOOS == "windows" {
		line = "echo %FWK_VERIFY_PROBE%> out.txt"
	}

	if f := verify.Run(context.Background(), nil, dir, env, time.Minute, []verify.Command{{Name: "probe", Line: line}}); f != nil {
		t.Fatalf("failure = %v", f)
	}

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "probe-value" {
		t.Errorf("out.txt = %q, want the env value", got)
	}
}

func TestTailKeepsTheLastLines(t *testing.T) {
	f := &verify.Failure{Output: "1\n2\n3\n4\n"}
	if got := f.Tail(2); got != "3\n4" {
		t.Errorf("Tail(2) = %q, want \"3\\n4\"", got)
	}
	if got := f.Tail(10); got != "1\n2\n3\n4" {
		t.Errorf("Tail(10) = %q, want all four lines", got)
	}
}

func TestACommandThatHangsIsStoppedAtTheTimeoutWithWhatItStarted(t *testing.T) {
	// The shell starts a child that holds the output open; both must go.
	line := "sleep 30 & sleep 30"
	if runtime.GOOS == "windows" {
		line = "start /b ping -n 30 127.0.0.1 >nul & ping -n 30 127.0.0.1 >nul"
	}
	start := time.Now()

	f := verify.Run(context.Background(), nil, t.TempDir(), os.Environ(), 300*time.Millisecond, []verify.Command{{Name: "slow", Line: line}})

	if f == nil || !strings.Contains(f.Error(), "300ms") {
		t.Fatalf("failure = %v, want the timeout", f)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Run took %s; the command wasn't stopped at its timeout", elapsed)
	}
}

func TestDoubleQuotesInACommandReachTheProgramIntact(t *testing.T) {
	f := verify.Run(context.Background(), nil, t.TempDir(), os.Environ(), time.Minute,
		[]verify.Command{{Name: "quoted", Line: `git -c "fwk.probe=1" --version`}})

	if f != nil {
		t.Fatalf("failure = %v\n%s", f, f.Output)
	}
}

func TestTheShellIsFoundWithoutPATH(t *testing.T) {
	// The Factory may run with a PATH that lacks the system's own
	// directories; the shell is the system's, not PATH's.
	t.Setenv("PATH", t.TempDir())

	f := verify.Run(context.Background(), nil, t.TempDir(), os.Environ(), time.Minute,
		[]verify.Command{{Name: "exit", Line: "exit 0"}})

	if f != nil {
		t.Fatalf("failure = %v", f)
	}
}
