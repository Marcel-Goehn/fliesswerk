package factory

import (
	"context"
	"fmt"
	"path"
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
	files, err := git.Repo{Dir: b.dir}.Touched(ctx, from, b.branch)
	if err != nil {
		return factoryFailure(err)
	}
	var hit []string
	for _, f := range files {
		if protected(f, r.protectedPaths()) {
			hit = append(hit, f)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return &failure{handBack: true, protected: hit,
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
