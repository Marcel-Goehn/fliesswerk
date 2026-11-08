package native

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// Inside a Sandbox.
const (
	insideDir    = "/run/kraftwerk"
	insideSelf   = insideDir + "/kraftwerk"
	insideSocket = insideDir + "/" + socketName
	// proxyPort is where a Sandbox's programs find the egress proxy, on the
	// Sandbox's own loopback.
	proxyPort = "3128"
)

// socketName is the egress proxy's socket.
const socketName = "egress.sock"

// The hidden subcommands of the binary.
const (
	helperExec = "__sandbox-exec"
	helperInit = "__sandbox-init"
)

// systemRoots are what every program needs, read-only.
var systemRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc"}

// bwrapPlan is what the helper needs to build a command's bwrap call. It holds
// no secret, and it lies outside everything a Sandbox can see.
type bwrapPlan struct {
	Bwrap, Self, Socket string
	// Worktree is the worktree, Rel the project root in it and Workdir
	// where commands start.
	Worktree, Rel, Workdir string
	// Home and Tmp are the host directories behind the private home, which
	// is at UserHome inside, and /tmp.
	Home, Tmp, UserHome string
	// Toolchains are read-only paths PATH needs besides the system roots.
	Toolchains []string
	// Added are `read_only_paths` and `writable_paths`.
	Added []addedPath
	// Never are the paths no Sandbox may see, as Host.neverExposed.
	Never []string
	// Git are the worktree's git directories.
	Git sandbox.GitDirs
	// Protected are the Protected paths, relative to the project root.
	Protected []string
}

// addedPath is a configured path, which may not exist on this machine.
type addedPath struct {
	Source, Target string
	Writable       bool
}

// options are the bwrap options of one command, as things stand now. Later
// mounts go on top of earlier ones, so what must hold comes last.
func (s bwrapPlan) options(env []string) ([]string, error) {
	opts := []string{
		"--unshare-user", "--unshare-ipc", "--unshare-pid", "--unshare-net", "--unshare-uts", "--unshare-cgroup-try",
		"--die-with-parent", "--new-session", "--clearenv",
	}
	for _, root := range systemRoots {
		target, err := os.Readlink(root)
		switch {
		case err == nil:
			opts = append(opts, "--symlink", target, root)
		case exists(root):
			opts = append(opts, "--ro-bind", root, root)
		}
	}
	// A toolchain may lie in the real home or /tmp, so these two go first.
	opts = append(opts, "--bind", s.Tmp, "/tmp", "--bind", s.Home, s.UserHome)
	for _, p := range s.Toolchains {
		opts = append(opts, "--ro-bind", p, p)
	}
	for _, a := range s.Added {
		flag := "--ro-bind-try"
		if a.Writable {
			flag = "--bind-try"
		}
		opts = append(opts, flag, a.Source, a.Target)
	}
	opts = append(opts,
		"--proc", "/proc", "--dev", "/dev",
		"--ro-bind", s.Self, insideSelf,
		"--bind", s.Socket, insideSocket,
		"--bind", s.Worktree, s.Worktree,
	)
	opts = append(opts, s.gitMounts()...)
	protected, err := s.protectedMounts()
	if err != nil {
		return nil, err
	}
	opts = append(opts, protected...)
	opts = append(opts, s.masks()...)
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("malformed environment entry for %s", name)
		}
		opts = append(opts, "--setenv", name, value)
	}
	return append(opts, "--chdir", s.Workdir, "--remount-ro", "/"), nil
}

// masks cover what must stay hidden inside an added path, such as a
// keychain when the home root was added. CheckPaths lets such a path through
// because each of these can be covered.
func (s bwrapPlan) masks() []string {
	var opts []string
	for _, a := range s.Added {
		for _, never := range s.Never {
			if never == a.Source || !within(never, a.Source) {
				continue
			}
			target := filepath.Join(a.Target, strings.TrimPrefix(never, a.Source))
			switch info, err := os.Stat(never); {
			case err != nil:
			case info.IsDir():
				opts = append(opts, "--tmpfs", target)
			default:
				opts = append(opts, "--ro-bind", "/dev/null", target)
			}
		}
	}
	return opts
}

// gitMounts make the repository usable and its control files read-only.
// The host's git later runs in this repository too, so the git directory is
// read-only as a whole: its configs, hooks and other worktrees' pointers
// all tell the host what to execute. Only what a commit writes is opened up
// again, minus the files that say where the git directory is.
func (s bwrapPlan) gitMounts() []string {
	opts := []string{"--ro-bind", filepath.Join(s.Worktree, ".git"), filepath.Join(s.Worktree, ".git"), "--ro-bind", s.Git.Common, s.Git.Common}
	for _, dir := range []string{"objects", "refs", "logs"} {
		p := filepath.Join(s.Git.Common, dir)
		opts = append(opts, "--bind", p, p)
	}
	opts = append(opts, "--bind", s.Git.Admin, s.Git.Admin)
	for _, file := range []string{"commondir", "gitdir", "config.worktree"} {
		p := filepath.Join(s.Git.Admin, file)
		opts = append(opts, "--ro-bind", p, p)
	}
	return opts
}

// protectedMounts make the Protected paths that exist now read-only, and
// pin the directories above each: being mount points, they can't be renamed
// away to put a new file where the Protected path was. It looks at the tree
// afresh for each command, without following a symlink: a bind would follow
// it, out of the worktree, and show the agent where it leads. A symlink, or
// a path that doesn't exist yet, can still be written; the check after each
// committing Step rejects that.
func (s bwrapPlan) protectedMounts() ([]string, error) {
	var pins, readOnly []string
	root := filepath.Join(s.Worktree, s.Rel)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !sandbox.Protected(filepath.ToSlash(rel), s.Protected) || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		for dir := filepath.Dir(p); dir != s.Worktree; dir = filepath.Dir(dir) {
			pins = append(pins, dir)
		}
		readOnly = append(readOnly, p)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("looking for Protected paths: %w", err)
	}
	// A mount covers what is under it; sorted, a directory precedes its own.
	slices.Sort(pins)
	var opts []string
	for _, dir := range slices.Compact(pins) {
		opts = append(opts, "--bind", dir, dir)
	}
	for _, p := range readOnly {
		opts = append(opts, "--ro-bind", p, p)
	}
	return opts, nil
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// underSystemRoot reports whether a Sandbox has p anyway.
func underSystemRoot(p string) bool {
	return slices.ContainsFunc(systemRoots, func(root string) bool { return within(p, realPath(root)) })
}

// toolchains are the directories of PATH a Sandbox keeps, in order, and the
// paths to bind for them. A symlink in one often leads out of it (npm into
// its package, a self-updating Harness into its versions), and what it
// leads to has to be there as well.
func toolchains(h Host) (path, binds []string) {
	home := realPath(h.Home)
	for _, dir := range h.Path {
		real := realPath(dir)
		info, err := os.Stat(real)
		if !filepath.IsAbs(dir) || err != nil || !info.IsDir() || h.exposes(real) {
			continue
		}
		path = append(path, dir)
		if !underSystemRoot(real) {
			binds = append(binds, dir)
		}
		entries, err := os.ReadDir(real)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, err := filepath.EvalSymlinks(filepath.Join(real, e.Name()))
			if err != nil || underSystemRoot(target) || within(target, real) || h.exposes(target) {
				continue
			}
			bind := toolchainRoot(real, target, home)
			if h.exposes(bind) {
				bind = target
			}
			binds = append(binds, bind)
		}
	}
	slices.Sort(binds)
	return slices.Compact(path), slices.Compact(binds)
}

// toolchainRoot is what to bind so that target, reached by a symlink in
// dir, works: the two's common directory, if that is one toolchain's. Right
// under the home, or at the top, it is a place everything shares (~/.local
// holds the keyrings), and only target itself goes in. So too, in the
// caller, where the common directory holds anything that must stay hidden.
func toolchainRoot(dir, target, home string) string {
	common := dir
	for !within(target, common) {
		common = filepath.Dir(common)
	}
	if common == "/" || common == home || filepath.Dir(common) == home || filepath.Dir(common) == "/" {
		return target
	}
	return common
}
