package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// The commands are portable between sh and cmd.exe: `echo` and `git`.

func TestEveryCommandPassing(t *testing.T) {
	dir := t.TempDir()

	f := verify.Run(context.Background(), dir, os.Environ(), []verify.Command{
		{Name: "a", Line: "echo one"},
		{Name: "b", Line: "echo two"},
	})

	if f != nil {
		t.Fatalf("failure = %v, want none", f)
	}
}

func TestNoCommandsMeansNoChecks(t *testing.T) {
	if f := verify.Run(context.Background(), t.TempDir(), os.Environ(), nil); f != nil {
		t.Fatalf("failure = %v, want none", f)
	}
}

func TestTheFirstFailingCommandStopsTheRestAndKeepsItsOutput(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")

	f := verify.Run(context.Background(), dir, os.Environ(), []verify.Command{
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
	env := append(os.Environ(), "KWK_VERIFY_PROBE=probe-value")
	line := "echo $KWK_VERIFY_PROBE > out.txt"
	if runtime.GOOS == "windows" {
		line = "echo %KWK_VERIFY_PROBE%> out.txt"
	}

	if f := verify.Run(context.Background(), dir, env, []verify.Command{{Name: "probe", Line: line}}); f != nil {
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
