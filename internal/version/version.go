// Package version compares the versions of the tools the Factory calls.
package version

import (
	"errors"
	"fmt"
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

// Version is a release's major, minor and patch number.
type Version [3]int

// Parse reads a full version such as v1.2.3 or 1.2.3-rc.1, and reports
// whether it is a prerelease. Build metadata is ignored.
func Parse(s string) (v Version, prerelease, ok bool) {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	s, pre, prerelease := strings.Cut(s, "-")
	if prerelease && pre == "" {
		return Version{}, false, false
	}
	parts, ok := partial(s)
	if !ok || len(parts) != 3 {
		return Version{}, false, false
	}
	return Version(parts), prerelease, true
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// Major is the version's major number.
func (v Version) Major() int { return v[0] }

// Less reports whether v comes before o.
func (v Version) Less(o Version) bool { return slices.Compare(v[:], o[:]) < 0 }

// interval holds the versions from lo up to, but not including, hi. Without
// bounded it has no end.
type interval struct {
	lo, hi  Version
	bounded bool
}

func (iv interval) contains(v Version) bool {
	return !v.Less(iv.lo) && (!iv.bounded || v.Less(iv.hi))
}

// Range is a semver range: a list of intervals, any of which may match.
type Range []interval

// ParseRange reads a range in the usual notation: 1, 1.2, 1.2.3, 1.x, *,
// ^1.2, ~1.2, >=1.2 <2, and alternatives joined with ||.
func ParseRange(s string) (Range, error) {
	var r Range
	for alt := range strings.SplitSeq(s, "||") {
		comparators := strings.Fields(alt)
		if len(comparators) == 0 {
			return nil, errors.New("a part of it is empty")
		}
		var iv interval
		for _, c := range comparators {
			next, err := parseComparator(c)
			if err != nil {
				return nil, err
			}
			iv = iv.intersect(next)
		}
		r = append(r, iv)
	}
	return r, nil
}

func (iv interval) intersect(o interval) interval {
	if iv.lo.Less(o.lo) {
		iv.lo = o.lo
	}
	if o.bounded && (!iv.bounded || o.hi.Less(iv.hi)) {
		iv.hi, iv.bounded = o.hi, true
	}
	return iv
}

// parseComparator reads one operator and its possibly partial version.
func parseComparator(c string) (interval, error) {
	op := ""
	for _, candidate := range []string{">=", "<=", ">", "<", "^", "~", "="} {
		if strings.HasPrefix(c, candidate) {
			op = candidate
			break
		}
	}
	p, ok := partial(strings.TrimPrefix(strings.TrimPrefix(c, op), "v"))
	if !ok || strings.TrimPrefix(c, op) == "" {
		return interval{}, fmt.Errorf("%q isn't a version", c)
	}
	if len(p) == 0 && (op == ">" || op == "<") {
		return interval{}, fmt.Errorf("%q matches nothing", c)
	}
	low := bump(p, len(p))
	switch op {
	case ">=":
		return interval{lo: low}, nil
	case ">":
		return interval{lo: bump(p, len(p)-1)}, nil
	case "<":
		return interval{hi: low, bounded: true}, nil
	case "<=":
		return interval{hi: bump(p, len(p)-1), bounded: true}, nil
	case "~":
		return upTo(p, low, min(len(p)-1, 1)), nil
	case "^":
		at := len(p) - 1
		for i, n := range p {
			if n != 0 {
				at = i
				break
			}
		}
		return upTo(p, low, at), nil
	}
	return upTo(p, low, len(p)-1), nil
}

// upTo is the interval from low up to p with its number at index at raised.
// With no numbers at all it is every version.
func upTo(p []int, low Version, at int) interval {
	if len(p) == 0 {
		return interval{}
	}
	return interval{lo: low, hi: bump(p, at), bounded: true}
}

// bump is p, padded with zeros, with its number at index at raised by one
// and every number after it zero. An index past p's end only pads.
func bump(p []int, at int) Version {
	var v Version
	copy(v[:], p)
	if at < len(p) && at >= 0 {
		v[at]++
		clear(v[at+1:])
	}
	return v
}

// partial reads up to three dotted numbers. A trailing x, X or * ends the
// numbers it gives, so 1.x and 1 mean the same.
func partial(s string) ([]int, bool) {
	var out []int
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return nil, false
	}
	wild := false
	for _, part := range parts {
		if part == "x" || part == "X" || part == "*" {
			wild = true
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || wild {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// Contains reports whether v is in the range. A prerelease counts as the
// release it leads up to.
func (r Range) Contains(v Version) bool {
	return slices.ContainsFunc(r, func(iv interval) bool { return iv.contains(v) })
}

// Above is the lowest version in the range that is above v, if the range has
// a part that starts there.
func (r Range) Above(v Version) (Version, bool) {
	var best Version
	found := false
	for _, iv := range r {
		if v.Less(iv.lo) && (!found || iv.lo.Less(best)) {
			best, found = iv.lo, true
		}
	}
	return best, found
}

// Floor is where the range's highest part starts.
func (r Range) Floor() Version {
	var best Version
	for _, iv := range r {
		if best.Less(iv.lo) {
			best = iv.lo
		}
	}
	return best
}
