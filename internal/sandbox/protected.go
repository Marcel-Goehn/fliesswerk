package sandbox

import (
	"path"
	"strings"
)

// Protected reports whether file, relative to the project root with
// forward slashes, is one of patterns or inside one. A pattern ending in
// "/" is a directory; any other is a file, a directory or a glob
// (path.Match syntax, where * stays within one directory). A glob that
// matches a directory protects everything in it.
func Protected(file string, patterns []string) bool {
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
