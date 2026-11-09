//go:build darwin

package native

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// seatbeltBase is the part of a profile that never changes. Seatbelt lets the
// last matching rule decide. Not listed, so denied: every network operation,
// the hard links a Protected path could otherwise be reached through, the
// user's terminals, which a process could type into, and the Mach services of
// the Keychain (com.apple.SecurityServer, com.apple.securityd.xpc) and of DNS
// (com.apple.dnssd.service). kern.procargs would show the environment of the
// user's other processes.
const seatbeltBase = `(version 1)
(deny default)

(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))
(allow mach-priv-task-port (target same-sandbox))
(allow sysctl-read)
(deny sysctl-read (sysctl-name-prefix "kern.procargs"))
(allow ipc-posix-shm)
(allow ipc-posix-sem)
(allow mach-lookup
  (global-name "com.apple.bsd.dirhelper")
  (global-name "com.apple.logd")
  (global-name "com.apple.system.logger")
  (global-name "com.apple.system.notification_center")
  (global-name "com.apple.system.opendirectoryd.libinfo")
  (global-name "com.apple.system.opendirectoryd.membership")
  (global-name "com.apple.trustd")
  (global-name "com.apple.trustd.agent"))

(allow file-read* (literal "/") (literal "/etc") (literal "/var") (literal "/tmp"))
(allow file-read* file-map-executable (regex #"^/Applications/Xcode[^/]*\.app(/|$)"))
(allow file-read* file-write-data file-ioctl
  (literal "/dev/null")
  (literal "/dev/zero")
  (literal "/dev/stdin")
  (literal "/dev/stdout")
  (literal "/dev/stderr")
  (subpath "/dev/fd"))
(allow file-read* (literal "/dev/random") (literal "/dev/urandom") (literal "/dev/dtracehelper"))
`

// seatbeltProfile is the profile of one command, as things stand now. Like
// bwrap's mounts, what must hold comes last.
func (s sandboxPlan) seatbeltProfile() (string, error) {
	pins, protected, err := s.protectedPaths()
	if err != nil {
		return "", err
	}
	r := seatbeltRules{ancestors: map[string]bool{}}
	r.WriteString(seatbeltBase)
	r.rule("allow network-outbound (remote ip %s)", "localhost:"+s.ProxyPort)
	for _, port := range s.ServicePorts {
		r.rule("allow network-outbound (remote ip %s)", "localhost:"+strconv.Itoa(port))
	}

	for _, root := range macSystemRoots {
		r.read(root)
	}
	for _, p := range s.Toolchains {
		r.read(p)
	}
	for _, a := range s.Added {
		r.read(a.Target)
		verb := "deny"
		if a.Writable {
			verb = "allow"
		}
		r.rule(verb+" file-write* (subpath %s)", realPath(a.Source))
	}
	for _, dir := range []string{s.Worktree, s.Home, s.Tmp} {
		r.read(dir)
		r.rule("allow file-write* (subpath %s)", realPath(dir))
	}

	// As under bwrap: the git directory is read-only as a whole, and only
	// what a commit writes is opened up again.
	common, admin, worktree := realPath(s.Git.Common), realPath(s.Git.Admin), realPath(s.Worktree)
	r.read(common)
	r.rule("deny file-write* (subpath %s)", common)
	opened := []string{admin}
	for _, dir := range gitCommitDirs {
		opened = append(opened, filepath.Join(common, dir))
	}
	for _, dir := range opened {
		r.rule("allow file-write* (subpath %s)", dir)
	}
	for _, file := range gitControlFiles {
		r.rule("deny file-write* (literal %s)", filepath.Join(admin, file))
	}
	r.rule("deny file-write* (literal %s)", filepath.Join(worktree, ".git"))

	// Renaming a directory is only checked against the directory itself, and
	// a rule matches by path: what is moved leaves its rules behind. So what
	// must stay put is pinned, along with every directory above what is denied.
	pinned := map[string]bool{worktree: true}
	for _, dir := range opened {
		pinned[dir] = true
	}
	for _, dir := range pins {
		pinned[realPath(dir)] = true
	}
	for _, p := range append(slices.Clone(s.Never), common, worktree) {
		for _, dir := range above(p) {
			pinned[dir] = true
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(pinned)) {
		r.rule("deny file-write-unlink (literal %s)", dir)
	}
	for _, p := range protected {
		r.rule("deny file-write* (subpath %s)", realPath(p))
	}

	for _, p := range slices.Sorted(maps.Keys(r.ancestors)) {
		r.rule("allow file-read-metadata (literal %s)", p)
	}
	for _, never := range s.Never {
		r.rule("deny file-read* file-write* (subpath %s)", never)
	}
	return r.String(), nil
}

// seatbeltRules builds a profile.
type seatbeltRules struct {
	strings.Builder
	ancestors map[string]bool
}

// rule adds a rule whose %s is path, quoted.
func (r *seatbeltRules) rule(format, path string) {
	r.WriteString("(" + strings.Replace(format, "%s", seatbeltQuote(path), 1) + ")\n")
}

// read opens p, and lets a program walk to it: what lies beside the way
// stays closed, down to whether it exists.
func (r *seatbeltRules) read(p string) {
	real := realPath(p)
	r.rule("allow file-read* file-map-executable (subpath %s)", real)
	for _, dir := range above(real) {
		r.ancestors[dir] = true
	}
	if p != real {
		r.ancestors[p] = true
		for _, dir := range above(p) {
			r.ancestors[dir] = true
		}
	}
}

// above are the directories over p, short of the root.
func above(p string) []string {
	var dirs []string
	for dir := filepath.Dir(p); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
	}
	return dirs
}

// seatbeltQuote is a path as a string of the profile language.
func seatbeltQuote(p string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
}
