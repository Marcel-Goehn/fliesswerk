//go:build integration && darwin

package native

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

func TestTheRealTmpIsClosed(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	mustFail(t, sb, "touch /tmp/fliesswerk-sandbox-escaped")
	mustFail(t, sb, "touch /private/tmp/fliesswerk-sandbox-escaped")
}

func TestAHardLinkCannotReachAProtectedPathOrGitControlFile(t *testing.T) {
	pr := newProject(t, map[string]string{".fliesswerk/config.toml": "version = 1\n"}, nil)
	sb := pr.open(t, ".fliesswerk/")

	mustFail(t, sb, "ln .fliesswerk/config.toml linked")
	mustFail(t, sb, "ln "+filepath.Join(pr.root, ".git", "config")+" linked")
}

func TestTheWorktreeCannotBeMovedAside(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := newProject(t, nil, func(o *Options) { o.Writable = []string{base} })
	sb := pr.open(t)

	mustFail(t, sb, "mv "+pr.worktree+" "+filepath.Join(base, "moved"))
}

func TestAUnixSocketIsOutOfReachEvenOnAWritablePath(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fwk-") // t.TempDir() is too long for a socket's path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	sb := newProject(t, nil, func(o *Options) { o.Writable = []string{dir} }).open(t)

	mustSh(t, sb, "test -S "+path)
	mustFail(t, sb, "nc -U -w 2 "+path+" < /dev/null")
}

func TestAKeychainAddedByAccidentStaysHidden(t *testing.T) {
	pr := newProject(t, nil, func(o *Options) { o.Writable = []string{"~"} })
	keychain := filepath.Join(pr.home, "Library", "Keychains", "login.keychain-db")
	writeFile(t, keychain, "KEY")
	sb := pr.open(t)

	mustFail(t, sb, "cat "+keychain)
	mustFail(t, sb, "test -e "+keychain)
	mustFail(t, sb, "rm "+keychain)
}

func TestWhatLiesBesideTheWayIsNotEvenSeen(t *testing.T) {
	pr := newProject(t, nil, nil)
	mustFail(t, pr.open(t), "test -e "+filepath.Join(pr.home, ".ssh", "id_rsa"))
}

func TestKillAllEndsWhatACommandLeftBehind(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	const marker = "27182818"
	mustSh(t, sb, "sleep "+marker+" >/dev/null 2>&1 &")
	alive := func() bool {
		out, _ := exec.Command("pgrep", "-f", "sleep "+marker).Output()
		return strings.TrimSpace(string(out)) != ""
	}
	if !alive() {
		t.Fatal("the background process never ran, so this proves nothing")
	}

	if err := sb.KillAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	for deadline := time.Now().Add(5 * time.Second); alive(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("still running after KillAll")
		}
	}
}

func TestTheGitDirectoriesCannotBeMovedAside(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := newProject(t, nil, func(o *Options) { o.Writable = []string{base} })
	sb := pr.open(t)
	common := filepath.Join(pr.root, ".git")

	for _, dir := range []string{filepath.Join(common, "worktrees", "ticket-1"), filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")} {
		mustFail(t, sb, "mv "+dir+" "+filepath.Join(base, "moved-"+filepath.Base(dir)))
	}
}

func TestAKeychainCannotBeMovedOutFromUnderItsRule(t *testing.T) {
	pr := newProject(t, nil, func(o *Options) { o.Writable = []string{"~"} })
	writeFile(t, filepath.Join(pr.home, "Library", "Keychains", "login.keychain-db"), "KEY")
	sb := pr.open(t)

	mustFail(t, sb, `mv "`+filepath.Join(pr.home, "Library")+`" "`+filepath.Join(pr.home, "elsewhere")+`"`)
	mustFail(t, sb, "cat "+filepath.Join(pr.home, "elsewhere", "Keychains", "login.keychain-db"))
}

func TestTheDataVolumeIsNotAWayAroundTheRules(t *testing.T) {
	pr := newProject(t, nil, nil)
	mustFail(t, pr.open(t), "cat /System/Volumes/Data"+filepath.Join(pr.home, ".ssh", "id_rsa"))
}

func TestGitControlFilesStayReadOnlyInAnotherCase(t *testing.T) {
	pr := newProject(t, map[string]string{"standards.md": "x\n"}, nil)
	sb := pr.open(t, "standards.md")

	mustFail(t, sb, "echo x > .GIT")
	mustFail(t, sb, "echo x >> "+filepath.Join(pr.root, ".git", "CONFIG"))
	mustFail(t, sb, "echo x >> STANDARDS.md")
}

func TestTerminalsAreOutOfReach(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	mustFail(t, sb, ": < /dev/ptmx")
}

func TestTheEnvironmentOfAHostProcessIsNotVisible(t *testing.T) {
	const marker = "FWK_HOST_SECRET=31337"
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	sb := newProject(t, nil, nil).open(t)

	if out, _ := sh(sb, "ps -Eww -p "+strconv.Itoa(cmd.Process.Pid)); strings.Contains(out, marker) {
		t.Errorf("visible from the Sandbox: %s", out)
	}
}

func TestAnAddedHomePathIsFoundThroughTheHome(t *testing.T) {
	pr := newProject(t, nil, func(o *Options) { o.ReadOnly = []string{"~/.tool"} })
	writeFile(t, filepath.Join(pr.home, ".tool", "config"), "from-the-real-home")
	sb := pr.open(t)

	if got := mustSh(t, sb, `cat "$HOME/.tool/config"`); got != "from-the-real-home" {
		t.Errorf("read %q", got)
	}
	mustFail(t, sb, `touch "$HOME/.tool/new"`)
}

// denials are the Seatbelt denials macOS logged in the last two minutes.
func denials() string {
	out, _ := exec.Command("/usr/bin/log", "show", "--last", "2m", "--style", "compact", "--predicate", `eventMessage CONTAINS "deny(1)"`).CombinedOutput()
	const keep = 8000
	if len(out) > keep {
		out = out[len(out)-keep:]
	}
	return string(out)
}

func TestAppleGitWorksInTheSandbox(t *testing.T) {
	if err := exec.Command("/usr/bin/git", "--version").Run(); err != nil {
		t.Skipf("/usr/bin/git doesn't work outside the Sandbox either: %v", err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	sb := newProject(t, nil, nil).open(t)

	if out, ok := sh(sb, "git --version"); !ok || !strings.HasPrefix(out, "git version") {
		t.Errorf("git, as a Harness finds it: %s\nwhat Seatbelt denied:\n%s", out, denials())
	}
	if out, ok := sh(sb, "/usr/bin/git --version"); !ok || !strings.Contains(out, "git version") {
		t.Errorf("/usr/bin/git: %s\nwhat Seatbelt denied:\n%s", out, denials())
	}
}

func TestAPublishedPortIsReachableAndNoOtherLocalhostPortIs(t *testing.T) {
	port := listener(t, "db")
	other := unpublishedPort(t)
	pr := newProject(t, nil, func(o *Options) {
		o.Services = &fakeServices{running: []sandbox.PublishedService{{Name: "db", Aliases: []string{"db"}, Ports: []sandbox.PublishedPort{{Container: 5432, Host: port}}}}}
		o.EnvTemplates = map[string]map[string]string{"db": {"DATABASE_URL": "postgres://{host}:{port}/app"}}
	})
	sb := pr.open(t)
	address := "127.0.0.1:" + strconv.Itoa(port)

	if got, ok := sh(sb, fetch(address)); !ok || got != "db" {
		t.Errorf("%s: %q\nwhat Seatbelt denied:\n%s", address, got, denials())
	}
	if got := mustSh(t, sb, `printf '%s %s %s' "$FLIESSWERK_SERVICE_DB_HOST" "$FLIESSWERK_SERVICE_DB_PORT" "$DATABASE_URL"`); got != "127.0.0.1 "+strconv.Itoa(port)+" postgres://"+address+"/app" {
		t.Errorf("the variables say %q", got)
	}
	if listed := sb.Services(); len(listed) != 1 || listed[0].Addresses[0] != address {
		t.Errorf("Services() = %+v", listed)
	}
	mustFail(t, sb, "curl -s --noproxy '*' --max-time 3 http://127.0.0.1:"+strconv.Itoa(other)+"/")
}
