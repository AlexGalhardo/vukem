package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// version is a parsed semantic version; build metadata is dropped.
type version struct {
	raw                 string
	major, minor, patch int
	pre                 string
}

func parseVersion(s string) (version, error) {
	v := version{raw: s}
	core := strings.TrimPrefix(s, "v")
	core, _, _ = strings.Cut(core, "+")
	core, v.pre, _ = strings.Cut(core, "-")

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("version %q is not major.minor.patch", s)
	}

	nums := [3]*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("version %q: bad number %q", s, p)
		}

		*nums[i] = n
	}

	return v, nil
}

func (v version) stable() bool { return v.pre == "" }

// cmp orders by major, minor, patch; pre-releases never reach it (callers
// filter them out first).
func (v version) cmp(o version) int {
	for _, p := range [][2]int{{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}

			return 1
		}
	}

	return 0
}

// partial is a range operand such as 1, 1.2, 1.x or 1.2.3; n counts the
// numeric parts given.
type partial struct {
	nums [3]int
	n    int
}

func parsePartial(s string) (partial, error) {
	var p partial

	s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "-")
	if s == "" {
		return p, nil
	}

	for part := range strings.SplitSeq(s, ".") {
		if part == "x" || part == "X" || part == "*" {
			break
		}

		if p.n == 3 {
			return p, fmt.Errorf("range operand %q has too many parts", s)
		}

		n, err := strconv.Atoi(part)
		if err != nil {
			return p, fmt.Errorf("range operand %q: bad number %q", s, part)
		}

		p.nums[p.n] = n
		p.n++
	}

	return p, nil
}

func (p partial) floor() version {
	return version{major: p.nums[0], minor: p.nums[1], patch: p.nums[2]}
}

// bump is the first version above everything the partial covers (1.2 -> 1.3.0).
func (p partial) bump() version {
	switch p.n {
	case 0:
		return version{major: 1 << 30}
	case 1:
		return version{major: p.nums[0] + 1}
	case 2:
		return version{major: p.nums[0], minor: p.nums[1] + 1}
	default:
		return version{major: p.nums[0], minor: p.nums[1], patch: p.nums[2] + 1}
	}
}

func (p partial) caretCeil() version {
	switch {
	case p.n == 0:
		return p.bump()
	case p.nums[0] > 0 || p.n == 1:
		return version{major: p.nums[0] + 1}
	case p.nums[1] > 0 || p.n == 2:
		return version{major: 0, minor: p.nums[1] + 1}
	default:
		return p.bump()
	}
}

// satisfies reports whether v is inside the npm range r. It covers what
// peerDependencies use in practice: ||, space-separated comparators, ^, ~,
// x-ranges and the relational operators. Hyphen ranges are rejected so a
// range this tool cannot read fails closed instead of passing.
func satisfies(v version, r string) (bool, error) {
	for set := range strings.SplitSeq(r, "||") {
		ok, err := satisfiesSet(v, strings.Fields(set))
		if err != nil {
			return false, err
		}

		if ok {
			return true, nil
		}
	}

	return false, nil
}

func satisfiesSet(v version, comps []string) (bool, error) {
	for _, c := range comps {
		if c == "-" {
			return false, errors.New("hyphen ranges are not supported")
		}

		ok, err := satisfiesComparator(v, c)
		if err != nil || !ok {
			return false, err
		}
	}

	return true, nil
}

func satisfiesComparator(v version, c string) (bool, error) {
	i := strings.IndexAny(c, "0123456789vxX*")
	if i < 0 {
		i = len(c)
	}

	op := c[:i]

	p, err := parsePartial(c[i:])
	if err != nil {
		return false, err
	}

	lo, hi := p.floor(), p.bump()

	switch op {
	case "", "=":
		return v.cmp(lo) >= 0 && v.cmp(hi) < 0, nil
	case "^":
		return v.cmp(lo) >= 0 && v.cmp(p.caretCeil()) < 0, nil
	case "~":
		if p.n == 1 {
			return v.cmp(lo) >= 0 && v.cmp(hi) < 0, nil
		}

		return v.cmp(lo) >= 0 && v.cmp(version{major: p.nums[0], minor: p.nums[1] + 1}) < 0, nil
	case ">=":
		return v.cmp(lo) >= 0, nil
	case ">":
		return v.cmp(hi) >= 0 || (p.n == 3 && v.cmp(lo) > 0), nil
	case "<":
		return v.cmp(lo) < 0, nil
	case "<=":
		return v.cmp(hi) < 0, nil
	default:
		return false, fmt.Errorf("unsupported range operator %q in %q", op, c)
	}
}

// namesMajor reports whether range r explicitly lists major m (^7, ~7.1,
// 7.x, 7.0.0); an open-ended ">=" or an upper bound does not count, because
// it says nothing about whether the package was tried on that major.
func namesMajor(r string, m int) bool {
	for set := range strings.SplitSeq(r, "||") {
		for c := range strings.FieldsSeq(set) {
			if strings.HasPrefix(c, ">") || strings.HasPrefix(c, "<") {
				continue
			}

			p, err := parsePartial(strings.TrimLeft(c, "^~="))
			if err == nil && p.n > 0 && p.nums[0] == m {
				return true
			}
		}
	}

	return false
}
