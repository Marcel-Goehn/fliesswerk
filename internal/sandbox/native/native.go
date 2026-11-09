// Package native is the native Sandbox: every command runs on the user's
// machine, in a bubblewrap of its own on Linux and WSL2 and under a Seatbelt
// profile of its own on macOS. Reads are closed but for the system and the
// toolchains on PATH, writes reach only the worktree, and the one way out is
// the egress proxy.
package native

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// destroyTimeout bounds removing a Sandbox that failed part way.
const destroyTimeout = time.Minute

// proxyVars are the variables that point a program at a proxy.
var proxyVars = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// Options are what every Sandbox of a Run shares.
type Options struct {
	// Root is the project root, and StateDir the runtime state in it.
	Root, StateDir string
	// Env is `[sandbox.env]`.
	Env map[string]string
	// Egress is what the egress proxy lets through.
	Egress egress.Options
	// CACerts is `[network] ca_certs`.
	CACerts []string
	// ReadOnly and Writable are the checked `sandbox.read_only_paths` and
	// `sandbox.writable_paths`.
	ReadOnly, Writable []string
	// HomeSeed are the Harness's files for the private home.
	HomeSeed []harness.HomeFile
	// Services run the project's Services, nil for a project without, and
	// EnvTemplates are each one's `env` templates.
	Services     Services
	EnvTemplates map[string]map[string]string
	// Log gets a line about what the Provider does.
	Log func(string)
	// Host is the machine; the zero value means the one this runs on.
	Host *Host
}

// Provider creates the native Sandboxes of a Run.
type Provider struct {
	opts Options
	host Host
	// launcher is bwrap, or sandbox-exec on macOS.
	launcher string
	// self is this binary, which a Sandbox runs as its first process.
	self     string
	identity []string
	proxy    *egress.Proxy
	// socketDir holds the proxy's socket under bwrap. It lives outside the
	// project: the path of a Unix socket is short, and a project's may not be.
	socketDir string
	// proxyPort is the proxy's port on the host's loopback under Seatbelt,
	// which has no loopback of the Sandbox's own to forward from.
	proxyPort string
	// shared are the Run's shared Services, none until they run.
	shared []sandbox.PublishedService
	// ca is what CACerts names.
	ca sandbox.CACerts

	mu     sync.Mutex
	denied map[string]bool
}

// New is a Provider that still has to be prepared.
func New(opts Options) *Provider {
	return &Provider{opts: opts, denied: map[string]bool{}}
}

// Connect proves the machine can build a Sandbox and clears what a crashed
// Run of this checkout left behind.
func (p *Provider) Connect(ctx context.Context) error {
	launcher, err := Check(ctx)
	if err != nil {
		return err
	}
	p.launcher = launcher
	if p.opts.Services != nil {
		if err := p.opts.Services.Connect(ctx); err != nil {
			return err
		}
		// A Service left running is only found through its record.
		if err := p.opts.Services.DownLeft(ctx); err != nil {
			p.log("warning: cleaning up an earlier Run's Services: %v", err)
			return nil
		}
	}
	if err := forceRemove(filepath.Join(p.opts.StateDir, "sandboxes")); err != nil {
		p.log("warning: cleaning up an earlier Run's Sandboxes: %v", err)
	}
	return nil
}

// Prepare rejects Services a Sandbox couldn't reach and starts the Run's
// egress proxy, on what is the only thing connecting a Sandbox to it: a Unix
// socket under bwrap, and under Seatbelt the one loopback port the profile
// allows. The Provider must be connected, and Close ends what this starts.
func (p *Provider) Prepare(ctx context.Context) error {
	var err error
	if p.ca, err = sandbox.LoadCACerts(p.opts.Root, p.opts.CACerts); err != nil {
		return err
	}
	if p.opts.Services != nil {
		if err := p.opts.Services.Validate(ctx); err != nil {
			return err
		}
	}
	p.host = CurrentHost()
	if p.opts.Host != nil {
		p.host = *p.opts.Host
	}
	if p.host.Home == "" {
		return errors.New("the native Sandbox needs to know your home directory; set HOME")
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding the kraftwerk binary: %w", err)
	}
	if p.self, err = filepath.EvalSymlinks(self); err != nil {
		return fmt.Errorf("finding the kraftwerk binary: %w", err)
	}
	p.identity = sandbox.CommitIdentity(ctx, p.opts.Root)
	if err := p.opts.Egress.Validate(); err != nil {
		return err
	}
	o := p.opts.Egress
	o.Log = p.logDenied
	if !p.host.Mac {
		if p.socketDir, err = os.MkdirTemp("", "kwk-"); err != nil {
			return err
		}
		o.Socket = filepath.Join(p.socketDir, socketName)
	}
	if p.proxy, err = egress.Start(context.WithoutCancel(ctx), o); err != nil {
		_ = os.RemoveAll(p.socketDir)
		p.socketDir = ""
		return err
	}
	if p.host.Mac {
		if _, p.proxyPort, err = net.SplitHostPort(p.proxy.Addr()); err != nil {
			return fmt.Errorf("finding the egress proxy's port: %w", err)
		}
	}
	return nil
}

// logDenied passes on each denial once: a client's retries say nothing new.
func (p *Provider) logDenied(line string) {
	p.mu.Lock()
	seen := p.denied[line]
	p.denied[line] = true
	p.mu.Unlock()
	if !seen {
		p.log("%s", line)
	}
}

// Close stops the egress proxy. It can be called when none was started.
func (p *Provider) Close() {
	if p.proxy != nil {
		if err := p.proxy.Close(); err != nil {
			p.log("warning: stopping the egress proxy: %v", err)
		}
		p.proxy = nil
	}
	if p.socketDir != "" {
		_ = os.RemoveAll(p.socketDir)
		p.socketDir = ""
	}
}

func (p *Provider) log(format string, a ...any) {
	if p.opts.Log != nil {
		p.opts.Log(fmt.Sprintf(format, a...))
	}
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// Create makes a Sandbox's private home and /tmp, which last as long as the
// Sandbox so that what a Hook installs is there for the Steps, and starts its
// Services, which are ready when it returns.
func (p *Provider) Create(ctx context.Context, req sandbox.Request) (sandbox.Sandbox, error) {
	git, err := sandbox.WorktreeGit(req.Worktree)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(p.opts.StateDir, "sandboxes", unsafeName.ReplaceAllString(req.Name, "-"))
	s := sandboxPlan{
		Git: git, Protected: req.Protected,
		Launcher: p.launcher, Self: p.self,
		Worktree: req.Worktree, Rel: req.Rel,
		Workdir: filepath.Join(req.Worktree, req.Rel),
		Home:    filepath.Join(state, "home"), Tmp: filepath.Join(state, "tmp"),
		UserHome: p.host.Home,
	}
	// Seatbelt can't put a directory at another path, so the agent is told
	// where its private home and temp directory really are.
	home, tmp, groupDir := p.host.Home, "/tmp", filepath.Join(state, groupsDir)
	if p.host.Mac {
		s.ProxyPort = p.proxyPort
		home, tmp = s.Home, s.Tmp
	} else {
		s.Socket = filepath.Join(p.socketDir, socketName)
	}
	path, binds := toolchains(p.host)
	if p.host.Mac {
		path, binds = preferXcodeTools(path, binds, developerDir())
	}
	s.Toolchains = binds
	s.Added, s.Never = p.added(), p.host.neverExposed()
	s.TrustStore = trustStore(p.host)
	for _, dir := range []string{s.Home, s.Tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	for _, f := range p.opts.HomeSeed {
		file := filepath.Join(s.Home, f.Path)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, []byte(f.Content), 0o600); err != nil {
			return nil, err
		}
	}
	if p.host.Mac {
		if err := linkAddedHomePaths(s.Home, p.host.Home, s.Added); err != nil {
			return nil, err
		}
	}
	b := &box{self: p.self, state: state, plan: filepath.Join(state, "plan.json"), groupDir: groupDir, env: p.baseEnv(path, home, tmp), ownServices: p.opts.Services}
	b.setEnv([]string{"KRAFTWERK_SANDBOX=" + req.Name})
	start := func() error {
		if err := p.installCA(b, &s); err != nil {
			return err
		}
		if err := p.startServices(ctx, b, &s, req); err != nil {
			return err
		}
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		return os.WriteFile(b.plan, data, 0o600)
	}
	if err := start(); err != nil {
		// A kill of the Run mustn't leave half a Sandbox behind.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), destroyTimeout)
		defer cancel()
		return nil, errors.Join(err, b.Destroy(cleanup))
	}
	return b, nil
}

// installCA builds the Sandbox's combined CA file from the host's system list
// and points its programs at it. They win over `[sandbox.env]`.
func (p *Provider) installCA(b *box, s *sandboxPlan) error {
	if !p.ca.Configured() {
		return nil
	}
	system, err := hostSystemCA()
	if err != nil {
		return err
	}
	dir := filepath.Join(b.state, "ca")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := p.ca.Install(dir, system); err != nil {
		return err
	}
	s.CADir, s.CACerts = dir, p.ca.Paths
	inside := insideCADir
	if p.host.Mac { // Seatbelt can't put a directory at another path
		inside = dir
	}
	b.setEnv(sandbox.CAEnv(inside))
	return nil
}

// hostSystemCA is the list of roots this machine's programs use.
func hostSystemCA() ([]byte, error) {
	for _, file := range sandbox.SystemCAFiles {
		if data, err := os.ReadFile(file); err == nil && len(data) > 0 { //nolint:gosec // a fixed list of system files
			return data, nil
		}
	}
	return nil, errors.New("this machine has no system CA list for [network] ca_certs to add to; install `ca-certificates`")
}

// added are the configured paths, as CheckPaths has let them through.
func (p *Provider) added() []addedPath {
	var added []addedPath
	add := func(paths []string, writable bool) {
		for _, configured := range paths {
			if target, ok := p.host.expand(configured); ok {
				added = append(added, addedPath{Source: realPath(target), Target: target, Writable: writable})
			}
		}
	}
	add(p.opts.Writable, true)
	add(p.opts.ReadOnly, false)
	// Mounts go on top of one another: the more specific path has the last
	// word, and read-only wins where a path is listed as both.
	slices.SortStableFunc(added, func(a, b addedPath) int { return cmp.Compare(len(a.Target), len(b.Target)) })
	return added
}

// linkAddedHomePaths puts the configured paths under the real home where a
// program looks for them, in the private home that is $HOME under Seatbelt.
// Under bwrap they are simply mounted there.
func linkAddedHomePaths(private, realHome string, added []addedPath) error {
	for _, a := range added {
		rel, ok := strings.CutPrefix(a.Target, realHome+"/")
		link := filepath.Join(private, rel)
		if !ok || !exists(a.Target) || exists(link) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			return err
		}
		if err := os.Symlink(a.Target, link); err != nil {
			return err
		}
	}
	return nil
}

// baseEnv is what every command in a Sandbox gets. A commit needs no repack
// of the git directory, which is read-only at the top.
func (p *Provider) baseEnv(path []string, home, tmp string) map[string]string {
	env := map[string]string{
		"HOME": home, "PATH": strings.Join(path, ":"), "TMPDIR": tmp,
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "gc.auto", "GIT_CONFIG_VALUE_0": "0",
	}
	for _, kv := range p.identity {
		name, value, _ := strings.Cut(kv, "=")
		env[name] = value
	}
	maps.Copy(env, p.opts.Env)
	proxy := "http://127.0.0.1:" + cmp.Or(p.proxyPort, proxyPort)
	for name, value := range map[string]string{"HTTP_PROXY": proxy, "HTTPS_PROXY": proxy, "NO_PROXY": "localhost,127.0.0.1,::1"} {
		env[name], env[strings.ToLower(name)] = value, value
	}
	return env
}

// box is one native Sandbox: a private home and /tmp, and a description of
// the bwrap or profile each command gets.
type box struct {
	self, state, plan string
	// groupDir lists the first processes of its commands, which on macOS are
	// also the leaders of their process groups.
	groupDir string
	env      map[string]string
	// services is what the Step is told it can reach, and stopServices ends
	// them and their forwarders, nil for none.
	services     []sandbox.Service
	stopServices func(context.Context) error
	// ownServices measures its own Services, nil for a project without.
	ownServices Services
}

// Exec implements sandbox.Sandbox. The hidden helper builds the bwrap call or
// the profile: an env value never shows in a process's argv, and it looks at
// the worktree as it is now.
func (b *box) Exec(argv, env []string) (hostArgv, hostEnv []string) {
	merged := maps.Clone(b.env)
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if !slices.Contains(proxyVars, name) { // these always name the proxy
			merged[name] = value
		}
	}
	for _, name := range slices.Sorted(maps.Keys(merged)) {
		hostEnv = append(hostEnv, name+"="+merged[name])
	}
	return append([]string{b.self, helperExec, b.plan, "--"}, argv...), hostEnv
}

// KillAll implements sandbox.Sandbox. Under bwrap there is nothing to do:
// each command has its own PID namespace, and the kernel ends everything in
// it with the command. macOS has none, so the commands' process groups go;
// a process that left its group with setsid survives, an accepted risk.
func (b *box) KillAll(context.Context) error {
	killGroups(b.groupDir)
	return nil
}

// Services implements sandbox.Sandbox.
func (b *box) Services() []sandbox.Service { return b.services }

// Destroy implements sandbox.Sandbox. Services that couldn't be removed keep
// the Sandbox's state: its record is how the next Run finds them.
func (b *box) Destroy(ctx context.Context) error {
	_ = b.KillAll(ctx)
	if b.stopServices != nil {
		if err := b.stopServices(ctx); err != nil {
			return err
		}
	}
	return forceRemove(b.state)
}

// forceRemove removes a tree the agent had the run of, where it may have
// left directories without write permission.
func forceRemove(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // a directory of the Sandbox's, about to go
		}
		return nil
	})
	return os.RemoveAll(dir)
}
