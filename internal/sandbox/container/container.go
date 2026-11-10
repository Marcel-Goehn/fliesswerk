// Package container is the container Sandbox: an image built from the
// project's Containerfile, one container per Sandbox on an internal network
// of its own whose only way out is the Run's proxy sidecar, and every command
// run in it through the engine's `exec`, so that secrets are never part of
// what the container was created with.
package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// Inside a Sandbox.
const (
	// gitDir is where the checkout's git directory is mounted.
	gitDir = factoryDir + "/git"
	// homeDir is the agent's home, writable whichever user it runs as.
	homeDir = "/tmp/kraftwerk-home"
	// caDir holds the CA files of a Sandbox with `ca_certs`.
	caDir = factoryDir + "/ca"
	// credentialDir holds the credential file the Factory keeps.
	credentialDir = factoryDir + "/credentials"
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
	// Egress is what the proxy sidecar lets through.
	Egress egress.Options
	// CACerts is `[network] ca_certs`.
	CACerts []string
	// CredentialDir holds the credential file the Factory keeps up to date,
	// "" for none. Every Sandbox mounts it read-only.
	CredentialDir string
	// Version is the Factory's, which tags the sidecar's image.
	Version string
	// ComposeFile is the compose file relative to the project root, Services
	// the own Services to start from it for each Sandbox, and Shared the ones
	// that run once per Run; none for a project without.
	ComposeFile      string
	Services, Shared []string
	// EnvTemplates are each Service's `env` templates, by Service.
	EnvTemplates map[string]map[string]string
	// Log gets a line about what the Provider does.
	Log func(string)
}

// Provider creates the container Sandboxes of a Run.
type Provider struct {
	opts   Options
	engine Engine
	// compose is the Run's compose tool, set only when there are Services.
	compose composeTool
	image   string
	// proxyImage is the sidecar's image, and sidecar the name of the Run's
	// sidecar and of its normal network, "" until it is started.
	proxyImage, sidecar string
	// project tells this checkout's containers from those of others on the
	// same engine.
	project string
	// identity is the commit identity the host would use.
	identity []string
	// shared are the Run's shared Services, nil until they run.
	shared *sharedServices
	// ca is what CACerts names.
	ca sandbox.CACerts
}

// New is a Provider that still has to be prepared.
func New(opts Options) *Provider {
	return &Provider{opts: opts, project: projectHash(opts.Root)}
}

// projectHash tells a checkout's containers and Compose projects from those
// of others on the same engine.
func projectHash(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:12]
}

// Connect resolves the engine and its compose tool, and clears what a
// crashed Run of this checkout left on it.
func (p *Provider) Connect(ctx context.Context) error {
	engine, err := Resolve(ctx)
	if err != nil {
		return err
	}
	p.engine = engine
	p.log("Container engine: %s", engine)
	if p.hasServices() {
		if p.compose, err = resolveCompose(ctx, engine, runCLI, exec.LookPath); err != nil {
			return err
		}
	}
	p.cleanUp(ctx)
	return nil
}

// Prepare rejects a compose file the Factory can't use, makes sure the
// image is current and starts the Run's proxy sidecar. The Provider must be
// connected, and Close ends what this starts.
func (p *Provider) Prepare(ctx context.Context) error {
	var err error
	if p.ca, err = sandbox.LoadCACerts(p.opts.Root, p.opts.CACerts); err != nil {
		return err
	}
	if p.hasServices() {
		if _, err := p.compose.validate(ctx, p.opts.Root, p.opts.ComposeFile, p.opts.Services, p.opts.Shared); err != nil {
			return err
		}
	}
	p.identity = sandbox.CommitIdentity(ctx, p.opts.Root)
	if err := p.ensureImage(ctx); err != nil {
		return err
	}
	return p.startSidecar(ctx)
}

// hasServices reports whether the project has any Service, own or shared.
func (p *Provider) hasServices() bool {
	return len(p.opts.Services)+len(p.opts.Shared) > 0
}

// labels mark something as this Run's.
func (p *Provider) labels() []string {
	return []string{"--label", labelRun + "=" + p.opts.RunID, "--label", labelProject + "=" + p.project}
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
	var stackErr error
	if p.hasServices() {
		if stackErr = downRecorded(ctx, p.compose, p.opts.StateDir); stackErr != nil {
			warn(stackErr)
		}
	}
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
	if stackErr != nil {
		return // the records say what is still to remove
	}
	if err := os.RemoveAll(filepath.Join(p.opts.StateDir, "sandboxes")); err != nil {
		warn(err)
	}
}

// ensureImage builds the image if none exists for this Containerfile and
// these `ca_certs`. The tag holds their hash, so a change to either is a new
// image. The build context is empty: nothing of the project, least of all
// `.env` or a worktree, can end up in an image.
func (p *Provider) ensureImage(ctx context.Context) error {
	file := filepath.Join(p.opts.Root, ".kraftwerk", "Containerfile")
	data, err := os.ReadFile(file) //nolint:gosec // the project's own Containerfile
	if err != nil {
		return fmt.Errorf("the container Sandbox needs .kraftwerk/Containerfile: %w", err)
	}
	sum := sha256.Sum256(append(data, p.ca.PEM...))
	p.image = "kraftwerk-" + p.project + ":" + hex.EncodeToString(sum[:])[:12]
	if _, err := p.engine.run(ctx, "image", "inspect", p.image); err == nil {
		p.log("The Sandbox image %s is present", p.image)
		return nil
	}
	empty, err := os.MkdirTemp("", "kraftwerk-build")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(empty) }()
	args := []string{"build", "--tag", p.image, "--file", file, "--label", labelProject + "=" + p.project}
	args = append(args, buildProxyArgs(p.opts.Egress.UpstreamProxy)...)
	if p.ca.Configured() {
		secret := filepath.Join(empty + "-ca.pem")
		if err := os.WriteFile(secret, p.ca.PEM, 0o600); err != nil {
			return err
		}
		defer func() { _ = os.Remove(secret) }()
		args = append(args, "--secret", "id="+caSecret+",src="+secret)
	}
	p.log("Building the Sandbox image from .kraftwerk/Containerfile")
	if _, err := p.engine.run(ctx, append(args, empty)...); err != nil {
		return fmt.Errorf("building the Sandbox image from .kraftwerk/Containerfile: %w", explainBuildFailure(err))
	}
	return nil
}

// explainBuildFailure adds the fix to the one build failure that isn't about
// the Containerfile: `RUN --mount`, which the build secret needs, on a Docker
// with no BuildKit. Distribution packages of Docker often leave it out.
func explainBuildFailure(err error) error {
	if !strings.Contains(err.Error(), "requires BuildKit") && !strings.Contains(err.Error(), "buildx component is missing") {
		return err
	}
	return fmt.Errorf("%w\nDocker needs its buildx plugin to build this image: install it (on Debian or Ubuntu, `sudo apt install docker-buildx`; otherwise see https://docs.docker.com/go/buildx/) and run this again", err)
}

// caSecret is the build secret `ca_certs` go in as.
const caSecret = "kraftwerk-ca" //nolint:gosec // the name of a build secret, not one

// buildProxyArgs send a build through the corporate proxy, not the egress
// proxy, which doesn't exist yet. The host's variables go by name: the engine
// client reads their values from its env, and a login in one stays out of
// every argv. An `upstream_proxy` replaces them, and holds no login.
func buildProxyArgs(upstream string) []string {
	var args []string
	if upstream != "" {
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			args = append(args, "--build-arg", name+"="+upstream)
		}
		return args
	}
	for _, name := range proxyVars {
		if _, ok := os.LookupEnv(name); ok {
			args = append(args, "--build-arg", name)
		}
	}
	return args
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// Create creates and starts a Sandbox holding the worktree, on a new
// internal network that only the proxy sidecar and the Sandbox's own
// Services are on as well. The Services are ready when it returns.
func (p *Provider) Create(ctx context.Context, req sandbox.Request) (sandbox.Sandbox, error) {
	name := unsafeName.ReplaceAllString("kraftwerk-"+p.opts.RunID+"-"+req.Name, "-")
	b := &box{
		engine: p.engine, name: name, sidecar: p.sidecar, state: filepath.Join(p.opts.StateDir, "sandboxes", name),
		workdir: path.Join(workspaceDir, filepath.ToSlash(req.Rel)),
		env:     append(p.baseEnv(), "KRAFTWERK_SANDBOX="+req.Name),
	}
	mounts, err := p.mounts(req.Worktree, b.state)
	if err != nil {
		return nil, err
	}
	labels := p.labels()
	create := append([]string{"network", "create", "--internal"}, labels...)
	if _, err := p.engine.run(ctx, append(append(create, networkHardening(p.engine)...), name)...); err != nil {
		return nil, fmt.Errorf("creating the Sandbox's network: %w", err)
	}
	start := func() error {
		services, err := p.startServices(ctx, b, req)
		if err != nil {
			return err
		}
		ip, err := p.attachSidecar(ctx, name)
		if err != nil {
			return err
		}
		b.env = slices.Concat(b.env, proxyEnv(ip, serviceHosts(services)), serviceEnv(services, p.opts.EnvTemplates))
		b.services = addresses(services)
		if err := b.start(ctx, p.createArgs(name, b.workdir, labels, mounts, services)); err != nil {
			return err
		}
		if err := p.installCA(ctx, b); err != nil {
			return err
		}
		return p.startForwarder(ctx, b, labels, services)
	}
	if err := start(); err != nil {
		// A kill of the Run mustn't leave half a Sandbox behind.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
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
func (p *Provider) createArgs(name, workdir string, labels []string, mounts []mount, services []runningService) []string {
	args := []string{"create", "--name", name, "--network", name, "--workdir", workdir, "--entrypoint", "sleep"}
	args = append(args, labels...)
	for _, m := range mounts {
		args = append(args, "--mount", m.flag())
	}
	args = append(args, userArgs(p.engine, os.Getuid(), os.Getgid())...)
	args = append(args, noHostProxy(p.engine)...)
	args = append(args, hostArgs(services)...)
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
	dirs, err := sandbox.WorktreeGit(worktree)
	if err != nil {
		return nil, err
	}
	common, admin, id := dirs.Common, dirs.Admin, dirs.ID

	if err := os.MkdirAll(state, 0o750); err != nil {
		return nil, err
	}
	gitFile := filepath.Join(state, "git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+gitDir+"/worktrees/"+id+"\n"), 0o644); err != nil { //nolint:gosec // readable by the Sandbox's user; holds a path
		return nil, err
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
	if p.opts.CredentialDir != "" {
		mounts = append(mounts, mount{source: p.opts.CredentialDir, target: credentialDir, readOnly: true})
	}
	if p.ca.Configured() {
		// Filled in once the Sandbox can say what its system list is.
		if err := os.MkdirAll(caStateDir(state), 0o755); err != nil { //nolint:gosec // readable by the Sandbox's user; public certificates
			return nil, err
		}
		mounts = append(mounts, mount{source: caStateDir(state), target: caDir, readOnly: true})
	}
	return mounts, nil
}

// caStateDir is where a Sandbox's CA files are on the host.
func caStateDir(state string) string { return filepath.Join(state, "ca") }

// systemCAScript prints the Sandbox's system CA list, nothing if it has
// none.
func systemCAScript() string {
	return "for f in " + strings.Join(sandbox.SystemCAFiles, " ") + `; do if [ -s "$f" ]; then exec cat "$f"; fi; done`
}

// installCA builds the Sandbox's combined CA file from the system list its
// image has, and points its programs at it. They win over `[sandbox.env]`.
func (p *Provider) installCA(ctx context.Context, b *box) error {
	if !p.ca.Configured() {
		return nil
	}
	system, err := b.engine.run(ctx, "exec", b.name, "/bin/sh", "-c", systemCAScript())
	if err != nil {
		return fmt.Errorf("reading the Sandbox's system CA list: %w", err)
	}
	if strings.TrimSpace(system) == "" {
		return errors.New("the Sandbox image has no system CA list for [network] ca_certs to add to; install `ca-certificates` in .kraftwerk/Containerfile")
	}
	if err := p.ca.Install(caStateDir(b.state), []byte(system)); err != nil {
		return err
	}
	vars := sandbox.CAEnv(caDir)
	b.env = slices.DeleteFunc(b.env, func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(vars, func(v string) bool { return strings.HasPrefix(v, name+"=") })
	})
	b.env = append(b.env, vars...)
	return nil
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
		// These always name the sidecar and the Factory's credential file.
		if !slices.Contains(proxyVars, k) && k != sandbox.CredentialFileEnv {
			env = append(env, k+"="+v)
		}
	}
	if p.opts.CredentialDir != "" {
		env = append(env, sandbox.CredentialEnv(credentialDir)...)
	}
	slices.Sort(env)
	return env
}

// box is one running container Sandbox.
type box struct {
	engine Engine
	// sidecar is the Run's proxy sidecar, on this Sandbox's network.
	name, workdir, sidecar string
	// state is the Sandbox's directory in the runtime state.
	state string
	env   []string
	// forwarder is the Service forwarder's container, "" for none.
	forwarder string
	// stack is the Sandbox's Compose project, nil for none, and services
	// what the Step is told it can reach.
	compose  composeTool
	stack    *composeStack
	services []sandbox.Service
	// ownContainers are its own Services' containers, whose memory counts as
	// the Sandbox's. sharedContainers are the shared ones on its network,
	// which outlive it.
	ownContainers, sharedContainers []string
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
		if !slices.Contains(proxyVars, name) { // these always name the sidecar
			given[name] = value
		}
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

// Services implements sandbox.Sandbox.
func (b *box) Services() []sandbox.Service { return b.services }

// Destroy implements sandbox.Sandbox. The Services go before the network
// they are on.
func (b *box) Destroy(ctx context.Context) error {
	var forwardErr, stackErr error
	if b.forwarder != "" {
		forwardErr = b.engine.remove(ctx, b.forwarder)
	}
	rmErr := b.engine.remove(ctx, b.name)
	if b.stack != nil {
		stackErr = b.compose.down(ctx, *b.stack)
	}
	// A network with something still on it can't go; if this fails, so does
	// that.
	for _, name := range append([]string{b.sidecar}, b.sharedContainers...) {
		_, _ = b.engine.run(ctx, "network", "disconnect", "--force", b.name, name)
	}
	_, netErr := b.engine.run(ctx, "network", "rm", b.name)
	return errors.Join(forwardErr, rmErr, stackErr, netErr, os.RemoveAll(b.state))
}
