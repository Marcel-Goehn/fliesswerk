//go:build integration && linux

package native

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

const secret = "sk-very-secret-value" //nolint:gosec // a test value

// TestMain lets the test binary be what a Sandbox runs, as kraftwerk is.
func TestMain(m *testing.M) {
	if code, ok := RunHelper(os.Args[1:]); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// requireBwrap skips a test on a machine that blocks bwrap, after proving
// that the block is detected and its fix named: on a stock Ubuntu 24.04
// runner that detection is what there is to test.
func requireBwrap(t *testing.T) {
	t.Helper()
	_, err := Check(context.Background())
	var blocked *BlockedError
	if errors.As(err, &blocked) {
		if !strings.Contains(err.Error(), "sudo ") {
			t.Fatalf("the block is detected, but no fix is named: %v", err)
		}
		t.Skipf("bwrap is blocked here, and that is detected: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

type project struct {
	root, worktree, home string
	p                    *Provider
	logged               []string
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { //nolint:gosec // a path of the test's own
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { //nolint:gosec // a path of the test's own
		t.Fatal(err)
	}
}

// newProject is a checkout with a Factory worktree, on a machine whose home
// is a directory of the test's own.
func newProject(t *testing.T, files map[string]string, change func(*Options)) *project {
	t.Helper()
	requireBwrap(t)
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+who+"_NAME", "Test")
		t.Setenv("GIT_"+who+"_EMAIL", "t@example.com")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	writeFile(t, filepath.Join(home, ".ssh", "id_rsa"), "PRIVATE KEY")
	t.Setenv("HOME", home)

	root := filepath.Join(base, "project")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(root, "README.md"), "shop\n")
	for name, content := range files {
		writeFile(t, filepath.Join(root, name), content)
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "Initial commit")
	worktree := filepath.Join(root, ".kraftwerk", "state", "worktrees", "ticket-1")
	gitIn(t, root, "worktree", "add", "-q", "-b", "ticket-1", worktree)

	pr := &project{root: root, worktree: worktree, home: home}
	opts := Options{Root: root, StateDir: filepath.Join(root, ".kraftwerk", "state"), Log: func(l string) { pr.logged = append(pr.logged, l) }}
	if change != nil {
		change(&opts)
	}
	pr.p = New(opts)
	t.Cleanup(pr.p.Close)
	ctx := context.Background()
	if err := pr.p.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pr.p.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	return pr
}

func (pr *project) open(t *testing.T, protected ...string) sandbox.Sandbox {
	t.Helper()
	sb, err := pr.p.Create(context.Background(), sandbox.Request{Name: "build-1", Worktree: pr.worktree, Protected: protected})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

func sh(sb sandbox.Sandbox, line string, env ...string) (string, bool) {
	out, err := sandbox.Shell(context.Background(), sb, "", line, env).CombinedOutput()
	return strings.TrimSpace(string(out)), err == nil
}

func mustSh(t *testing.T, sb sandbox.Sandbox, line string, env ...string) string {
	t.Helper()
	out, ok := sh(sb, line, env...)
	if !ok {
		t.Fatalf("`%s` failed: %s", line, out)
	}
	return out
}

func mustFail(t *testing.T, sb sandbox.Sandbox, line string) {
	t.Helper()
	if out, ok := sh(sb, line); ok {
		t.Errorf("`%s` passed: %s", line, out)
	}
}

func TestACommandRunsInTheWorktreeWithItsEnvAndNotTheHosts(t *testing.T) {
	t.Setenv("HOST_ONLY", "from-the-host")
	pr := newProject(t, nil, func(o *Options) { o.Env = map[string]string{"NODE_ENV": "test"} })
	sb := pr.open(t)

	if got := mustSh(t, sb, "pwd"); got != pr.worktree {
		t.Errorf("pwd = %q, want the worktree", got)
	}
	if got := mustSh(t, sb, `printf %s "$API_TOKEN/$NODE_ENV/${HOST_ONLY:-unset}"`, "API_TOKEN="+secret); got != secret+"/test/unset" {
		t.Errorf("env = %q", got)
	}
	argv, _ := sb.Exec([]string{"true"}, []string{"API_TOKEN=" + secret})
	if strings.Contains(strings.Join(argv, " "), secret) {
		t.Errorf("the secret is in the argv: %q", argv)
	}
	if got := mustSh(t, sb, `tr '\0' ' ' < /proc/1/cmdline; ls /proc/self/fd | wc -l`, "API_TOKEN="+secret); strings.Contains(got, secret) {
		t.Errorf("the secret is in the Sandbox's first process's argv: %q", got)
	}
}

func TestAFailingCommandsExitCodeComesBack(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	err := sandbox.Shell(context.Background(), sb, "", "exit 7", nil).Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("err = %v, want exit 7", err)
	}
}

func TestReadsAreClosedAndWritesReachOnlyTheWorktree(t *testing.T) {
	pr := newProject(t, nil, nil)
	sb := pr.open(t)

	mustFail(t, sb, "cat "+filepath.Join(pr.home, ".ssh", "id_rsa"))
	mustFail(t, sb, "cat "+filepath.Join(pr.root, "README.md"))
	for _, dir := range []string{"/usr", "/etc", "/", "/run"} {
		mustFail(t, sb, "touch "+filepath.Join(dir, "written"))
	}
	mustSh(t, sb, "touch made-here")
	if _, err := os.Stat(filepath.Join(pr.worktree, "made-here")); err != nil {
		t.Errorf("a write to the worktree didn't reach it: %v", err)
	}
}

func TestHomeAndTmpArePrivateAndLastAcrossCommands(t *testing.T) {
	pr := newProject(t, nil, nil)
	sb := pr.open(t)
	marker := filepath.Join(os.TempDir(), "kraftwerk-host-marker-"+filepath.Base(pr.home)+filepath.Base(filepath.Dir(pr.home)))
	writeFile(t, marker, "host")
	t.Cleanup(func() { _ = os.Remove(marker) })

	mustFail(t, sb, "cat "+marker)
	mustSh(t, sb, `touch "$HOME/cached" /tmp/kept`)
	mustSh(t, sb, `test -e "$HOME/cached" && test -e /tmp/kept`)
	if _, err := os.Stat(filepath.Join(pr.home, "cached")); err == nil {
		t.Error("the Sandbox's home is the real one")
	}
}

func TestNothingOutlivesItsCommand(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	const marker = "31415926"
	mustSh(t, sb, "setsid sleep "+marker+" >/dev/null 2>&1 &")
	out, _ := exec.Command("pgrep", "-f", "sleep "+marker).Output()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("left running: %s", got)
	}
}

func TestToolchainsOnPathWorkWithoutOpeningTheHome(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Like a self-updating Harness: the PATH entry is a symlink out of ~/.local/bin.
	local := filepath.Join(base, ".local")
	writeExecutable := func(path, body string) {
		t.Helper()
		writeFile(t, path, "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(path, 0o700); err != nil { //nolint:gosec // an executable the test runs
			t.Fatal(err)
		}
	}
	writeExecutable(filepath.Join(local, "share", "harness", "versions", "1"), "echo harness")
	bin := filepath.Join(local, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(local, "share", "harness", "versions", "1"), filepath.Join(bin, "harness")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(local, "share", "keyrings", "login.keyring"), "KEY")

	// Like nvm: a symlink into its own package, which needs its siblings.
	node := filepath.Join(base, "versions", "node", "v22")
	writeExecutable(filepath.Join(node, "lib", "pkg", "bin", "tool"), `cat "$(dirname "$(readlink -f "$0")")/../data"`)
	writeFile(t, filepath.Join(node, "lib", "pkg", "data"), "tool-data")
	if err := os.MkdirAll(filepath.Join(node, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/pkg/bin/tool", filepath.Join(node, "bin", "tool")); err != nil {
		t.Fatal(err)
	}

	// A symlink whose common directory with its PATH entry holds the keyrings.
	writeExecutable(filepath.Join(local, "share", "b", "x"), "echo beside")
	shared := filepath.Join(local, "share", "a", "bin")
	if err := os.MkdirAll(shared, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(local, "share", "b", "x"), filepath.Join(shared, "beside")); err != nil {
		t.Fatal(err)
	}
	// A symlink to the whole home, and a PATH entry that holds the keyrings.
	if err := os.Symlink(base, filepath.Join(bin, "whole-home")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", strings.Join([]string{bin, filepath.Join(node, "bin"), shared, local, os.Getenv("PATH")}, ":"))
	pr := newProject(t, nil, func(o *Options) {
		host := CurrentHost()
		host.Home = base
		o.Host = &host
	})
	sb := pr.open(t)

	if got := mustSh(t, sb, "harness"); got != "harness" {
		t.Errorf("harness printed %q", got)
	}
	if got := mustSh(t, sb, "tool"); got != "tool-data" {
		t.Errorf("tool printed %q", got)
	}
	if got := mustSh(t, sb, "beside"); got != "beside" {
		t.Errorf("beside printed %q", got)
	}
	mustFail(t, sb, "cat "+filepath.Join(local, "share", "keyrings", "login.keyring"))
	mustFail(t, sb, "touch "+filepath.Join(bin, "planted"))
}

func TestACommitWorksAndReachesTheHostsRepository(t *testing.T) {
	pr := newProject(t, nil, nil)
	sb := pr.open(t)

	mustSh(t, sb, `echo change > feature.txt && git add feature.txt && git commit -q -m "Add the feature"`)

	if got := gitIn(t, pr.root, "log", "-1", "--format=%s %an", "ticket-1"); got != "Add the feature Test" {
		t.Errorf("the commit on the host: %q", got)
	}
}

func TestGitControlFilesAreReadOnly(t *testing.T) {
	pr := newProject(t, nil, nil)
	sb := pr.open(t)
	common := filepath.Join(pr.root, ".git")
	admin := filepath.Join(common, "worktrees", "ticket-1")

	for _, line := range []string{
		"echo x > .git",
		"rm .git",
		"echo x >> " + filepath.Join(common, "config"),
		"touch " + filepath.Join(common, "hooks", "post-commit"),
		"echo x >> " + filepath.Join(admin, "gitdir"),
		"echo x >> " + filepath.Join(admin, "commondir"),
		"echo '[core]' >> " + filepath.Join(admin, "config.worktree"),
	} {
		mustFail(t, sb, line)
	}
}

func TestProtectedPathsAreReadOnly(t *testing.T) {
	pr := newProject(t, map[string]string{
		".kraftwerk/config.toml": "version = 1\n",
		"docs/standards.md":      "be nice\n",
		"secrets/a.pem":          "A\n",
		"src/app.go":             "package app\n",
	}, nil)
	sb := pr.open(t, ".kraftwerk/", "docs/standards.md", "secrets/*.pem")

	for _, file := range []string{".kraftwerk/config.toml", "docs/standards.md", "secrets/a.pem"} {
		mustFail(t, sb, "echo x >> "+file)
		if got := mustSh(t, sb, "wc -l < "+file); got != "1" {
			t.Errorf("%s isn't readable as it was: %q", file, got)
		}
	}
	mustFail(t, sb, "touch .kraftwerk/new")
	mustSh(t, sb, "echo x >> src/app.go")
}

func TestAProtectedPathIsReadOnlyInASubdirectoryProject(t *testing.T) {
	pr := newProject(t, map[string]string{"web/.kraftwerk/config.toml": "version = 1\n", "web/app.go": "x\n"}, nil)
	sb, err := pr.p.Create(context.Background(), sandbox.Request{Name: "sub", Worktree: pr.worktree, Rel: "web", Protected: []string{".kraftwerk/"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })

	if got := mustSh(t, sb, "pwd"); got != filepath.Join(pr.worktree, "web") {
		t.Errorf("pwd = %q", got)
	}
	mustFail(t, sb, "echo x >> .kraftwerk/config.toml")
	mustSh(t, sb, "echo x >> app.go")
}

func TestASymlinkMatchingAProtectedPathDoesNotExposeItsTarget(t *testing.T) {
	pr := newProject(t, nil, nil)
	key := filepath.Join(pr.home, ".ssh", "id_rsa")
	if err := os.Symlink(key, filepath.Join(pr.worktree, "standards.md")); err != nil {
		t.Fatal(err)
	}
	sb := pr.open(t, "standards.md")

	mustSh(t, sb, "true") // a bind of it would stop every command, or show the key
	mustFail(t, sb, "cat standards.md")
}

// status is the HTTP status curl gets for url, through the egress proxy.
func status(t *testing.T, sb sandbox.Sandbox, url string) string {
	t.Helper()
	return mustSh(t, sb, "curl -s -o /dev/null -w '%{http_code}' --noproxy '' --max-time 10 "+url)
}

func TestTheOnlyWayOutIsTheEgressProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) }))
	t.Cleanup(srv.Close)
	for _, v := range proxyVars {
		t.Setenv(v, "")
	}
	pr := newProject(t, nil, func(o *Options) {
		o.Egress = egress.Options{Allow: []string{"127.0.0.1"}}
		o.Env = map[string]string{"HTTPS_PROXY": "http://evil.example:1"}
	})
	sb := pr.open(t)

	if got := status(t, sb, srv.URL); got != "200" {
		t.Errorf("an allowed host: status %s", got)
	}
	if got := status(t, sb, "http://denied.example/"); got != "403" {
		t.Errorf("a denied host: status %s", got)
	}
	status(t, sb, "http://denied.example/")
	denials := 0
	for _, line := range pr.logged {
		if strings.Contains(line, "denied.example") {
			denials++
		}
	}
	if denials != 1 {
		t.Errorf("logged %d denials, want 1: %q", denials, pr.logged)
	}
	mustFail(t, sb, "curl -s --noproxy '*' --max-time 3 "+srv.URL)
	mustFail(t, sb, "curl -s --noproxy '*' --max-time 3 http://1.1.1.1/")
	if got := mustSh(t, sb, `printf %s "$HTTPS_PROXY"`, "HTTPS_PROXY=http://evil.example:1"); !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Errorf("HTTPS_PROXY = %q", got)
	}
}

func TestAddedPathsAreReadOnlyOrWritableAsConfigured(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reference, cache := filepath.Join(base, "reference"), filepath.Join(base, "cache")
	writeFile(t, filepath.Join(reference, "doc"), "docs")
	writeFile(t, filepath.Join(cache, "old"), "cached")
	pr := newProject(t, nil, func(o *Options) {
		o.ReadOnly = []string{reference, filepath.Join(base, "not-on-this-machine")}
		o.Writable = []string{cache}
	})
	sb := pr.open(t)

	if got := mustSh(t, sb, "cat "+filepath.Join(reference, "doc")); got != "docs" {
		t.Errorf("read %q", got)
	}
	mustFail(t, sb, "touch "+filepath.Join(reference, "new"))
	mustSh(t, sb, "touch "+filepath.Join(cache, "new"))
	if _, err := os.Stat(filepath.Join(cache, "new")); err != nil {
		t.Errorf("the write didn't reach the host: %v", err)
	}
}

func TestAnAddedPathNeverExposesAKeychain(t *testing.T) {
	pr := newProject(t, map[string]string{".kraftwerk/config.toml": "version = 1\n"}, func(o *Options) {
		o.Writable = []string{"~"}
	})
	keyring := filepath.Join(os.Getenv("HOME"), ".local", "share", "keyrings", "login.keyring")
	gnupg := filepath.Join(os.Getenv("HOME"), ".gnupg", "private-keys")
	writeFile(t, keyring, "KEY")
	writeFile(t, gnupg, "KEY")
	sb := pr.open(t, ".kraftwerk/")

	mustFail(t, sb, "cat "+keyring)
	mustFail(t, sb, "cat "+gnupg)
	mustFail(t, sb, "echo x >> .kraftwerk/config.toml")
	mustFail(t, sb, "echo x > .git")
}

func TestEngineSocketsAndTheBusAreNotThere(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	sb := newProject(t, nil, nil).open(t)

	for _, p := range []string{"/run/docker.sock", "/var/run/docker.sock", "/run/podman", "/run/dbus", "/run/user", "/sys"} {
		mustFail(t, sb, "test -e "+p)
	}
	if got := mustSh(t, sb, `printf %s "${DBUS_SESSION_BUS_ADDRESS:-unset}"`); got != "unset" {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS = %q", got)
	}
}

func TestUnderWSLTheWindowsSideAndInteropAreHidden(t *testing.T) {
	t.Setenv("PATH", "/mnt:"+os.Getenv("PATH"))
	pr := newProject(t, nil, func(o *Options) {
		host := CurrentHost()
		host.WSL = true
		o.Host = &host
	})
	sb := pr.open(t)

	if got := mustSh(t, sb, `printf %s "$PATH"`); strings.Contains(got, "/mnt") {
		t.Errorf("PATH = %q", got)
	}
	for _, p := range []string{"/mnt", "/run/WSL", "/init", "/proc/sys/fs/binfmt_misc/register", "/proc/sys/fs/binfmt_misc/WSLInterop"} {
		mustFail(t, sb, "test -e "+p)
	}
}

func TestAProtectedPathsParentsCannotBeMovedAside(t *testing.T) {
	pr := newProject(t, map[string]string{"sub/dir/prot.txt": "keep\n", "sub/other.txt": "x\n"}, nil)
	sb := pr.open(t, "sub/dir/prot.txt")

	mustFail(t, sb, "mv sub sub2")
	mustFail(t, sb, "mv sub/dir sub/dir2")
	mustFail(t, sb, "rm -rf sub")
	mustFail(t, sb, "echo evil > sub/dir/prot.txt")
	mustSh(t, sb, "echo x >> sub/other.txt")
}

func TestASubdirectoryProjectCannotBeMovedAside(t *testing.T) {
	pr := newProject(t, map[string]string{"web/.kraftwerk/config.toml": "version = 1\n"}, nil)
	sb, err := pr.p.Create(context.Background(), sandbox.Request{Name: "sub", Worktree: pr.worktree, Rel: "web", Protected: []string{".kraftwerk/"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })

	mustFail(t, sb, "cd .. && mv web web2")
}

func TestAReadOnlyPathInsideAWritableOneStaysReadOnly(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(base, "data", "keep", "file"), "x")
	pr := newProject(t, nil, func(o *Options) {
		o.ReadOnly = []string{filepath.Join(base, "data", "keep")}
		o.Writable = []string{filepath.Join(base, "data")}
	})
	sb := pr.open(t)

	mustSh(t, sb, "touch "+filepath.Join(base, "data", "new"))
	mustFail(t, sb, "touch "+filepath.Join(base, "data", "keep", "new"))
}

func TestAKeychainStaysHiddenBehindASymlinkedHome(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "var-home")
	writeFile(t, filepath.Join(real, ".gnupg", "private-keys"), "KEY")
	link := filepath.Join(base, "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	pr := newProject(t, nil, func(o *Options) {
		host := CurrentHost()
		host.Home = link
		o.Host = &host
		o.ReadOnly = []string{"~"}
	})
	sb := pr.open(t)

	mustFail(t, sb, "cat "+filepath.Join(link, ".gnupg", "private-keys"))
}
