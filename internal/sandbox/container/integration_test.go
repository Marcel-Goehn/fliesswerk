//go:build integration

package container

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// baseImage has git, a shell and wget, and an entrypoint of its own that
// the Sandbox must not depend on.
const baseImage = "FROM docker.io/alpine/git:latest\n"

const secret = "sk-very-secret-value" //nolint:gosec // a test value

// project is a git checkout with a Factory worktree and a Provider for it.
type project struct {
	root, worktree string
	p              *Provider
	logged         []string
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

func newProject(t *testing.T, runArgs ...string) *project {
	t.Helper()
	// The identity a Sandbox's commits get is the one the host's git has.
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+who+"_NAME", "Test")
		t.Setenv("GIT_"+who+"_EMAIL", "t@example.com")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(root, ".kraftwerk", "state"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".kraftwerk", "Containerfile"), baseImage)
	writeFile(t, filepath.Join(root, "README.md"), "shop\n")
	gitIn(t, root, "add", "README.md")
	gitIn(t, root, "commit", "-q", "-m", "Initial commit")
	worktree := filepath.Join(root, ".kraftwerk", "state", "worktrees", "ticket-1")
	gitIn(t, root, "worktree", "add", "-q", "-b", "ticket-1", worktree)
	pr := &project{root: root, worktree: worktree}
	pr.p = pr.provider(t, runArgs...)
	return pr
}

func (pr *project) provider(t *testing.T, runArgs ...string) *Provider {
	t.Helper()
	p := New(Options{
		Root: pr.root, StateDir: filepath.Join(pr.root, ".kraftwerk", "state"),
		RunID: "test-" + strconv.FormatInt(time.Now().UnixNano(), 36), RunArgs: runArgs,
		Log: func(line string) { pr.logged = append(pr.logged, line) },
	})
	t.Cleanup(p.Close)
	return p
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// open prepares the Provider and creates a Sandbox removed with the test.
func (pr *project) open(t *testing.T) sandbox.Sandbox {
	t.Helper()
	ctx := context.Background()
	if err := connectAndPrepare(ctx, pr.p); err != nil {
		t.Fatal(err)
	}
	sb, err := pr.p.Create(ctx, sandbox.Request{Name: "build-1", Worktree: pr.worktree})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

func connectAndPrepare(ctx context.Context, p *Provider) error {
	if err := p.Connect(ctx); err != nil {
		return err
	}
	return p.Prepare(ctx)
}

// sh runs line in the Sandbox and returns its output and whether it passed.
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

func (pr *project) engine(t *testing.T, args ...string) string {
	t.Helper()
	out, err := pr.p.engine.run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTheImageIsRebuiltOnlyWhenTheContainerfileChanges(t *testing.T) {
	pr := newProject(t)
	ctx := context.Background()
	if err := connectAndPrepare(ctx, pr.p); err != nil {
		t.Fatal(err)
	}
	first := pr.p.image

	pr.logged = nil
	again := pr.provider(t)
	if err := connectAndPrepare(ctx, again); err != nil {
		t.Fatal(err)
	}
	if again.image != first || strings.Contains(strings.Join(pr.logged, "\n"), "Building") {
		t.Errorf("unchanged Containerfile: image %s (was %s), logged %q", again.image, first, pr.logged)
	}

	writeFile(t, filepath.Join(pr.root, ".kraftwerk", "Containerfile"), baseImage+"LABEL changed=yes\n")
	changed := pr.provider(t)
	if err := connectAndPrepare(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if changed.image == first {
		t.Fatal("a changed Containerfile kept the old image")
	}
	if got := pr.engine(t, "image", "inspect", "--format", `{{index .Config.Labels "changed"}}`, changed.image); strings.TrimSpace(got) != "yes" {
		t.Errorf("the rebuilt image has label %q", got)
	}
}

func TestASecretReachesTheCommandButNeverTheArgvOrInspect(t *testing.T) {
	pr := newProject(t)
	sb := pr.open(t)

	env := []string{"API_TOKEN=" + secret}
	if got := mustSh(t, sb, `printf %s "$API_TOKEN"`, env...); got != secret {
		t.Fatalf("the command saw %q", got)
	}
	argv, _ := sb.Exec([]string{"true"}, env)
	if strings.Contains(strings.Join(argv, " "), secret) {
		t.Errorf("the secret is in the argv: %q", argv)
	}
	if got := pr.engine(t, "inspect", sb.(*box).name); strings.Contains(got, secret) {
		t.Errorf("inspect shows the secret:\n%s", got)
	}
	if got := mustSh(t, sb, `printf %s "${API_TOKEN:-unset}"`); got != "unset" {
		t.Errorf("the secret outlived its command: %q", got)
	}
}

func TestEachSandboxHasItsOwnInternalLabelledNetworkRemovedWithIt(t *testing.T) {
	pr := newProject(t)
	sb := pr.open(t)
	name := sb.(*box).name

	if got := strings.TrimSpace(pr.engine(t, "network", "inspect", "--format", "{{.Internal}}", name)); got != "true" {
		t.Errorf("Internal = %q, want true", got)
	}
	label := "label=" + labelRun + "=" + pr.p.opts.RunID
	sidecar := pr.p.sidecar
	got := strings.Fields(pr.engine(t, "network", "ls", "--filter", label, "--format", "{{.Name}}"))
	if want := []string{name, sidecar}; !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("networks of the Run = %q, want %q", got, want)
	}

	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	// What is left is the Run's, not the Sandbox's.
	if got := strings.TrimSpace(pr.engine(t, "network", "ls", "--filter", label, "--format", "{{.Name}}")); got != sidecar {
		t.Errorf("networks left: %q, want only %q", got, sidecar)
	}
	if got := strings.TrimSpace(pr.engine(t, "ps", "-a", "--filter", label, "--format", "{{.Names}}")); got != sidecar {
		t.Errorf("containers left: %q, want only %q", got, sidecar)
	}
}

func TestTheSandboxCanReachNothing(t *testing.T) {
	// Something on the host, listening on every address.
	l, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("reached the host"))
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)

	pr := newProject(t)
	sb := pr.open(t)
	name := sb.(*box).name

	targets := []string{"1.1.1.1:80", "172.17.0.1:" + port, "host.docker.internal:" + port, "host.containers.internal:" + port}
	gateway := strings.TrimSpace(pr.engine(t, "network", "inspect", "--format", networkGatewayFormat(pr.p.engine), name))
	if gateway != "" && gateway != "<no value>" {
		targets = append(targets, gateway+":"+port)
	}
	for _, target := range targets {
		if out, ok := sh(sb, directWget+"http://"+target+"/"); ok {
			t.Errorf("the Sandbox reached %s: %s", target, out)
		}
	}
	if out, ok := sh(sb, "nslookup -timeout=3 example.com"); ok && strings.Contains(out, "Address") && !strings.Contains(out, "NXDOMAIN") && !strings.Contains(out, "can't") {
		t.Errorf("the Sandbox can resolve outside names:\n%s", out)
	}
}

// networkGatewayFormat reads a network's gateway on each engine.
func networkGatewayFormat(e Engine) string {
	if e.Binary == podman {
		return "{{range .Subnets}}{{.Gateway}}{{end}}"
	}
	return "{{range .IPAM.Config}}{{.Gateway}}{{end}}"
}

func TestTheSandboxHasNoEngine(t *testing.T) {
	pr := newProject(t)
	sb := pr.open(t)

	if out, ok := sh(sb, `test -e /var/run/docker.sock || test -e /run/podman/podman.sock || test -n "$DOCKER_HOST$CONTAINER_HOST" || command -v docker || command -v podman`); ok {
		t.Errorf("the Sandbox sees an engine: %s", out)
	}
	if got := pr.engine(t, "inspect", "--format", "{{json .Mounts}}", sb.(*box).name); strings.Contains(got, ".sock") {
		t.Errorf("a socket is mounted: %s", got)
	}
}

func TestGitCommitsFromTheSandboxButCannotChangeWhatTheHostRuns(t *testing.T) {
	pr := newProject(t)
	sb := pr.open(t)

	mustSh(t, sb, `echo work > work.txt && git add work.txt && git commit -q -m "From the Sandbox"`)
	if got := gitIn(t, pr.root, "log", "-1", "--format=%s", "ticket-1"); got != "From the Sandbox" {
		t.Errorf("the Ticket branch's tip is %q", got)
	}
	if got := gitIn(t, pr.root, "log", "-1", "--format=%ae", "ticket-1"); got == "" {
		t.Error("the commit has no author")
	}
	id := filepath.Base(pr.worktree)
	other := filepath.Join(pr.root, ".kraftwerk", "state", "worktrees", "ticket-2")
	gitIn(t, pr.root, "worktree", "add", "-q", "-b", "ticket-2", other)
	admin := "/kraftwerk/git/worktrees/" + id
	for _, attempt := range []string{
		"echo planted >> /kraftwerk/git/config",
		"echo planted > /kraftwerk/git/config.worktree",
		"mkdir -p /kraftwerk/git/hooks && echo planted > /kraftwerk/git/hooks/post-commit",
		"mkdir -p /kraftwerk/git/modules/x",
		"echo planted >> " + admin + "/commondir",
		"echo planted >> " + admin + "/gitdir",
		"echo planted >> " + admin + "/config.worktree",
		"mv " + admin + "/commondir " + admin + "/commondir.old",
		"mv " + admin + " /kraftwerk/git/worktrees/moved",
		"echo planted >> /kraftwerk/git/worktrees/ticket-2/commondir",
		"mv /kraftwerk/git/worktrees/ticket-2 /kraftwerk/git/worktrees/moved",
		"echo planted >> /workspace/.git",
	} {
		if out, ok := sh(sb, attempt); ok {
			t.Errorf("the Sandbox could `%s`: %s", attempt, out)
		}
	}
	// What a Step does in the normal course of its work still works.
	mustSh(t, sb, `git status --short && git log -1 --format=%s && echo more >> work.txt && git commit -q -am "Again" && git rev-parse HEAD`)
}

func TestKillAllEndsWhatACommandLeftBehindButNotTheSandbox(t *testing.T) {
	pr := newProject(t)
	sb := pr.open(t)

	mustSh(t, sb, `nohup sleep 300 >/dev/null 2>&1 &`)
	if got := mustSh(t, sb, "ps -o args"); !strings.Contains(got, "sleep 300") {
		t.Fatalf("nothing was left behind:\n%s", got)
	}
	if err := sb.KillAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustSh(t, sb, "ps -o args"); strings.Contains(got, "sleep 300") {
		t.Errorf("it is still running:\n%s", got)
	}
}

func TestRunArgsAreAppended(t *testing.T) {
	pr := newProject(t, "--label", "team=shop")
	sb := pr.open(t)

	got := pr.engine(t, "inspect", "--format", `{{index .Config.Labels "team"}}`, sb.(*box).name)
	if strings.TrimSpace(got) != "shop" {
		t.Errorf("label = %q", got)
	}
}

func TestPrepareClearsWhatACrashedRunOfThisCheckoutLeft(t *testing.T) {
	pr := newProject(t)
	sb := pr.open(t) // never destroyed: the Run "crashed"
	name := sb.(*box).name

	next := pr.provider(t)
	if err := connectAndPrepare(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(pr.engine(t, "ps", "-a", "--filter", "name="+name, "--format", "{{.Names}}")); got != "" {
		t.Errorf("container left: %q", got)
	}
	if got := strings.TrimSpace(pr.engine(t, "network", "ls", "--filter", "name="+name, "--format", "{{.Name}}")); got != "" {
		t.Errorf("network left: %q", got)
	}
}

func TestTheCredentialFileIsReadOnlyAndARewriteShowsInside(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, sandbox.CredentialFile)
	writeFile(t, file, "first")
	pr := newProject(t)
	pr.p.opts.CredentialDir = dir
	sb := pr.open(t)

	if got := mustSh(t, sb, sandbox.ReadCredentialCommand(false)); got != "first" {
		t.Errorf("read %q, want the file's content", got)
	}
	if out, ok := sh(sb, `echo x > "$`+sandbox.CredentialFileEnv+`"`); ok {
		t.Errorf("the Sandbox could write the file: %s", out)
	}

	writeFile(t, file+".new", "second")
	if err := os.Rename(file+".new", file); err != nil {
		t.Fatal(err)
	}
	if got := mustSh(t, sb, sandbox.ReadCredentialCommand(false)); got != "second" {
		t.Errorf("read %q after the rewrite, want the new content", got)
	}
}
