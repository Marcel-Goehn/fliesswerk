// Package container is the container Sandbox: an image built from the
// project's Containerfile, one container per Sandbox on an internal network
// of its own, and every command run in it through the engine's `exec`, so
// that secrets are never part of what the container was created with.
package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// Inside a Sandbox.
const (
	// gitDir is where the checkout's git directory is mounted.
	gitDir = factoryDir + "/git"
	// homeDir is the agent's home, writable whichever user it runs as.
	homeDir = "/tmp/kraftwerk-home"
)

// killTimeout bounds clearing a Sandbox.
const killTimeout = 15 * time.Second

// Options are what every Sandbox of a Run shares.
type Options struct {
	// Root is the project root, and StateDir the runtime state in it.
	Root, StateDir string
	RunID          string
	// RunArgs are the checked `sandbox.run_args`.
	RunArgs []string
	// Env is `[sandbox.env]`.
	Env map[string]string
	// Log gets a line about what the Provider does.
	Log func(string)
}

// Provider creates the container Sandboxes of a Run.
type Provider struct {
	opts   Options
	engine Engine
	image  string
	// project tells this checkout's containers from those of others on the
	// same engine.
	project string
	// identity is the commit identity the host would use.
	identity []string
}

// New is a Provider that still has to be prepared.
func New(opts Options) *Provider {
	sum := sha256.Sum256([]byte(opts.Root))
	return &Provider{opts: opts, project: hex.EncodeToString(sum[:])[:12]}
}

// Connect resolves the engine and clears what a crashed Run of this
// checkout left on it.
func (p *Provider) Connect(ctx context.Context) error {
	engine, err := Resolve(ctx)
	if err != nil {
		return err
	}
	p.engine = engine
	p.log("Container engine: %s", engine)
	p.cleanUp(ctx)
	return nil
}

// Prepare makes sure the image is current. The Provider must be connected.
func (p *Provider) Prepare(ctx context.Context) error {
	p.identity = commitIdentity(ctx, p.opts.Root)
	return p.ensureImage(ctx)
}

func (p *Provider) log(format string, a ...any) {
	if p.opts.Log != nil {
		p.opts.Log(fmt.Sprintf(format, a...))
	}
}

// cleanUp removes this checkout's containers and networks. The Run holds
// the checkout's lock, so none of them is in use. What can't be removed
// only warns.
func (p *Provider) cleanUp(ctx context.Context) {
	filter := "label=" + labelProject + "=" + p.project
	warn := func(err error) { p.log("warning: cleaning up an earlier Run's Sandboxes: %v", err) }
	out, err := p.engine.run(ctx, "ps", "-a", "--filter", filter, "--format", "{{.Names}}")
	if err != nil {
		warn(err)
	}
	for name := range strings.FieldsSeq(out) {
		if err := p.engine.remove(ctx, name); err != nil {
			warn(err)
		}
	}
	out, err = p.engine.run(ctx, "network", "ls", "--filter", filter, "--format", "{{.Name}}")
	if err != nil {
		warn(err)
	}
	for name := range strings.FieldsSeq(out) {
		if _, err := p.engine.run(ctx, "network", "rm", name); err != nil {
			warn(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(p.opts.StateDir, "sandboxes")); err != nil {
		warn(err)
	}
}

var identLine = regexp.MustCompile(`^(.*) <(.*)> \d+ [+-]\d+$`)

// commitIdentity is the env that gives commits in a Sandbox the identity
// the user's git would give them. The user's git config isn't in there.
func commitIdentity(ctx context.Context, root string) []string {
	var env []string
	repo := git.Repo{Dir: root}
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		out, err := repo.Run(ctx, "var", "GIT_"+who+"_IDENT")
		if m := identLine.FindStringSubmatch(strings.TrimSpace(out)); err == nil && m != nil {
			env = append(env, "GIT_"+who+"_NAME="+m[1], "GIT_"+who+"_EMAIL="+m[2])
		}
	}
	return env
}

// ensureImage builds the image if none exists for this Containerfile. The
// tag holds the file's hash, so a changed file is a new image. The build
// context is empty: nothing of the project, least of all `.env` or a
// worktree, can end up in an image.
func (p *Provider) ensureImage(ctx context.Context) error {
	file := filepath.Join(p.opts.Root, ".kraftwerk", "Containerfile")
	data, err := os.ReadFile(file) //nolint:gosec // the project's own Containerfile
	if err != nil {
		return fmt.Errorf("the container Sandbox needs .kraftwerk/Containerfile: %w", err)
	}
	sum := sha256.Sum256(data)
	p.image = "kraftwerk-" + p.project + ":" + hex.EncodeToString(sum[:])[:12]
	if _, err := p.engine.run(ctx, "image", "inspect", p.image); err == nil {
		return nil
	}
	empty, err := os.MkdirTemp("", "kraftwerk-build")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(empty) }()
	p.log("Building the Sandbox image from .kraftwerk/Containerfile")
	if _, err := p.engine.run(ctx, "build", "--tag", p.image, "--file", file, "--label", labelProject+"="+p.project, empty); err != nil {
		return fmt.Errorf("building the Sandbox image from .kraftwerk/Containerfile: %w", err)
	}
	return nil
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// Create creates and starts a Sandbox holding the worktree, on a new
// internal network that nothing else is on.
func (p *Provider) Create(ctx context.Context, req sandbox.Request) (sandbox.Sandbox, error) {
	name := unsafeName.ReplaceAllString("kraftwerk-"+p.opts.RunID+"-"+req.Name, "-")
	b := &box{
		engine: p.engine, name: name, state: filepath.Join(p.opts.StateDir, "sandboxes", name),
		workdir: path.Join(workspaceDir, filepath.ToSlash(req.Rel)),
		env:     p.baseEnv(),
	}
	mounts, err := p.mounts(req.Worktree, b.state)
	if err != nil {
		return nil, err
	}
	labels := []string{"--label", labelRun + "=" + p.opts.RunID, "--label", labelProject + "=" + p.project}
	create := append([]string{"network", "create", "--internal"}, labels...)
	if _, err := p.engine.run(ctx, append(append(create, networkHardening(p.engine)...), name)...); err != nil {
		return nil, fmt.Errorf("creating the Sandbox's network: %w", err)
	}
	if err := b.start(ctx, p.createArgs(name, b.workdir, labels, mounts)); err != nil {
		// A kill of the Run mustn't leave half a Sandbox behind.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), killTimeout)
		defer cancel()
		return nil, errors.Join(err, b.Destroy(cleanup))
	}
	return b, nil
}

// networkHardening closes what `--internal` leaves open. On Docker the
// network's bridge would have an address on the host, reachable from the
// Sandbox; on Podman the resolver would forward names to the host's, a way
// out for data.
func networkHardening(e Engine) []string {
	if e.Binary == docker {
		return []string{"--opt", "com.docker.network.bridge.inhibit_ipv4=true"}
	}
	return []string{"--disable-dns"}
}

// createArgs is the `create` call of a Sandbox. It has no env at all: what
// a command needs is delivered to that command. The first process only
// keeps the container alive, whatever the image's entrypoint.
func (p *Provider) createArgs(name, workdir string, labels []string, mounts []mount) []string {
	args := []string{"create", "--name", name, "--network", name, "--workdir", workdir, "--entrypoint", "sleep"}
	args = append(args, labels...)
	for _, m := range mounts {
		args = append(args, "--mount", m.flag())
	}
	args = append(args, userArgs(p.engine, os.Getuid(), os.Getgid())...)
	args = append(args, p.opts.RunArgs...)
	return append(args, p.image, "infinity")
}

// userArgs make the Sandbox's user the owner of the bind-mounted worktree.
func userArgs(e Engine, uid, gid int) []string {
	if uid < 0 { // Windows: the engine's VM handles ownership
		return nil
	}
	user := fmt.Sprintf("%d:%d", uid, gid)
	switch {
	case e.Rootless && e.Binary == docker:
		return []string{"--user", "0:0"} // root in there is the user out here
	case e.Rootless:
		return []string{"--userns=keep-id", "--user", user}
	}
	return []string{"--user", user}
}

// mount is one bind mount.
type mount struct {
	source, target string
	readOnly       bool
}

func (m mount) flag() string {
	s := "type=bind,source=" + m.source + ",target=" + m.target
	if m.readOnly {
		s += ",readonly"
	}
	return s
}

// mounts are a Sandbox's bind mounts: the worktree, and the git directory
// it belongs to. The host's git later runs in this repository too, so the
// git directory is read-only as a whole: its configs, hooks, submodules and
// other worktrees' pointers all tell the host what to execute. Only what a
// commit writes is opened up again: objects, refs, logs, and this worktree's
// own part, minus the files in it that say where the git directory is.
// Being mount points, those can't be renamed away. The worktree's `.git`
// file is replaced by one with the path inside the Sandbox.
func (p *Provider) mounts(worktree, state string) ([]mount, error) {
	data, err := os.ReadFile(filepath.Join(worktree, ".git")) //nolint:gosec // the Factory's own worktree
	if err != nil {
		return nil, err
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return nil, fmt.Errorf("%s is not a git worktree", worktree)
	}
	common, id := filepath.Dir(filepath.Dir(admin)), filepath.Base(admin)

	if err := os.MkdirAll(state, 0o750); err != nil {
		return nil, err
	}
	gitFile := filepath.Join(state, "git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+gitDir+"/worktrees/"+id+"\n"), 0o644); err != nil { //nolint:gosec // readable by the Sandbox's user; holds a path
		return nil, err
	}
	// A mount needs its source to exist, and git would read this file if
	// the agent could create it.
	if err := os.MkdirAll(filepath.Join(common, "logs"), 0o750); err != nil { //nolint:gosec // inside the git directory of the Factory's own worktree
		return nil, err
	}
	worktreeConfig := filepath.Join(admin, "config.worktree")
	if _, err := os.Stat(worktreeConfig); errors.Is(err, os.ErrNotExist) { //nolint:gosec // inside the git directory of the Factory's own worktree
		if err := os.WriteFile(worktreeConfig, nil, 0o600); err != nil { //nolint:gosec // inside the git directory of the Factory's own worktree
			return nil, err
		}
	}
	mounts := []mount{
		{source: worktree, target: workspaceDir},
		{source: gitFile, target: workspaceDir + "/.git", readOnly: true},
		{source: common, target: gitDir, readOnly: true},
	}
	for _, dir := range []string{"objects", "refs", "logs"} {
		mounts = append(mounts, mount{source: filepath.Join(common, dir), target: gitDir + "/" + dir})
	}
	own := gitDir + "/worktrees/" + id
	mounts = append(mounts, mount{source: admin, target: own})
	for _, file := range []string{"commondir", "gitdir", "config.worktree"} {
		mounts = append(mounts, mount{source: filepath.Join(admin, file), target: own + "/" + file, readOnly: true})
	}
	return mounts, nil
}

// baseEnv is what every command in a Sandbox gets.
func (p *Provider) baseEnv() []string {
	env := []string{
		"HOME=" + homeDir,
		// Whichever user the Sandbox runs as, git must accept the mounts.
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*",
		// A repack writes to the top of the git directory, which is read-only.
		"GIT_CONFIG_KEY_1=gc.auto", "GIT_CONFIG_VALUE_1=0",
	}
	env = append(env, p.identity...)
	for k, v := range p.opts.Env {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return env
}

// box is one running container Sandbox.
type box struct {
	engine        Engine
	name, workdir string
	// state is the Sandbox's directory in the runtime state.
	state string
	env   []string
}

// start creates and starts the container, and makes its home.
func (b *box) start(ctx context.Context, create []string) error {
	if _, err := b.engine.run(ctx, create...); err != nil {
		return fmt.Errorf("creating the Sandbox: %w", err)
	}
	if _, err := b.engine.run(ctx, "start", b.name); err != nil {
		return fmt.Errorf("starting the Sandbox: %w", err)
	}
	if _, err := b.engine.run(ctx, "exec", b.name, "mkdir", "-p", homeDir); err != nil {
		return fmt.Errorf("setting up the Sandbox: %w", err)
	}
	return nil
}

// Exec implements sandbox.Sandbox. `exec -e NAME` passes a variable from
// the engine client's own env, so what a command is given stays out of the
// argv and the container's config. The Sandbox's plain settings, which by
// definition hold no secret, go in with their value instead: in the
// client's env they could change how the client itself runs on the host.
func (b *box) Exec(argv, env []string) (hostArgv, hostEnv []string) {
	given := map[string]string{}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		given[name] = value
	}
	args := []string{"exec", "-i", "--workdir", b.workdir}
	for _, kv := range b.env {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := given[name]; !ok {
			args = append(args, "-e", kv)
		}
	}
	hostEnv = os.Environ()
	for _, name := range slices.Sorted(maps.Keys(given)) {
		args = append(args, "-e", name)
		hostEnv = append(hostEnv, name+"="+given[name])
	}
	args = append(args, b.name)
	return b.engine.argv(append(args, argv...)...), hostEnv
}

// KillAll implements sandbox.Sandbox: as root in the container's own PID
// namespace, it signals everything but itself and the first process.
func (b *box) KillAll(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, killTimeout)
	defer cancel()
	_, err := b.engine.run(ctx, "exec", "--user", "0", b.name, "/bin/sh", "-c", "kill -9 -1; true")
	return err
}

// Destroy implements sandbox.Sandbox.
func (b *box) Destroy(ctx context.Context) error {
	rmErr := b.engine.remove(ctx, b.name)
	_, netErr := b.engine.run(ctx, "network", "rm", b.name)
	return errors.Join(rmErr, netErr, os.RemoveAll(b.state))
}
