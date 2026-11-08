package factory

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
)

// factoryDir is the Factory's own directory, always a Protected path.
const factoryDir = ".kraftwerk/"

// protectedPaths are every Protected path.
func (r *run) protectedPaths() []string {
	return append([]string{factoryDir}, r.opts.ProtectedPaths...)
}

// checkProtected fails the Ticket if a commit the Step made since from
// touched a Protected path.
func (r *run) checkProtected(ctx context.Context, b build, step, from string) *failure {
	if failed := r.rejectPlantedGit(b.worktree); failed != nil {
		return failed
	}
	files, err := git.Repo{Dir: b.dir}.Touched(ctx, from, b.branch)
	if err != nil {
		return factoryFailure(err)
	}
	return r.rejectProtected(step, files)
}

// rejectPlantedGit fails the Ticket if a `.git` appeared between the top of
// the worktree and the project root. The Factory's own git runs in the
// project root, and would take it for the repository and do what its config
// says, on the host.
func (r *run) rejectPlantedGit(worktree string) *failure {
	dir, rel := worktree, ""
	for _, part := range strings.Split(filepath.ToSlash(r.rel), "/") {
		if part == "" {
			continue
		}
		dir, rel = filepath.Join(dir, part), path.Join(rel, part)
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			planted := path.Join(rel, ".git")
			return &failure{handBack: true, kind: KindProtectedPath, reason: "created " + planted + ", which the Factory's own git would obey", protected: []string{planted}}
		}
	}
	return nil
}

// rejectProtected fails the Ticket if the Step changed a Protected path
// among files, which are relative to the project root.
func (r *run) rejectProtected(step string, files []string) *failure {
	var hit []string
	for _, f := range files {
		if protected(f, r.protectedPaths()) {
			hit = append(hit, f)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return &failure{handBack: true, kind: KindProtectedPath, protected: hit,
		reason: fmt.Sprintf("%s committed changes to %d Protected %s, which only a person may change", step, len(hit), plural(len(hit), "path", "paths"))}
}

// protected reports whether file, relative to the project root with
// forward slashes, is one of patterns or inside one. A pattern ending in
// "/" is a directory; any other is a file, a directory or a glob
// (path.Match syntax, where * stays within one directory). A glob that
// matches a directory protects everything in it.
func protected(file string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimPrefix(path.Clean("/"+strings.TrimSuffix(p, "/")), "/")
		for f := file; f != "." && f != "/"; f = path.Dir(f) {
			if ok, _ := path.Match(p, f); ok {
				return true
			}
		}
	}
	return false
}
