// Package version compares the versions of the tools the Factory calls.
package version

import (
	"slices"
	"strconv"
	"strings"
)

// AtLeast compares dotted versions as numbers, ignoring a leading v and any
// suffix such as +ds1. A version it can't read isn't enough.
func AtLeast(got, want string) bool {
	g := numbers(got)
	return g != nil && slices.Compare(g, numbers(want)) >= 0
}

func numbers(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexFunc(v, func(r rune) bool { return r != '.' && (r < '0' || r > '9') }); i >= 0 {
		v = v[:i]
	}
	var out []int
	for part := range strings.SplitSeq(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}
