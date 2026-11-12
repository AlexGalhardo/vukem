package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// tsHoldMajor is the TypeScript major the Vue and Svelte starters stay on
// until vue-tsc and svelte-check name the next one in their peerDependencies.
const tsHoldMajor = 6

type kind uint8

const (
	npmPkg kind = iota
	goMod
	dockerImg
	ghAction
)

// pin is one pinned dependency: where it is written and the rules that limit
// which release counts as its latest.
type pin struct {
	pkg string

	// Small fields grouped so the struct stays cheap to copy.
	kind kind
	// lockMinor keeps a Docker image on the minor line of its pinned tag.
	lockMinor bool
	// major locks the pin to one major line (htmx follows 4.x, decision F2).
	major int16
	// holdMajor caps the pin at that major until every holdUntil package in
	// the group lists the next major in its peerDependencies for this pin.
	holdMajor int16

	file    string // path relative to the repository root
	group   string // pins of one group must install together (peerDependencies)
	current string
	re      *regexp.Regexp // three groups; the second is the version

	holdUntil []string
	// follow names the Docker pin whose tag version this pin copies (bun).
	follow string
}

type candidate struct {
	v     version
	peers map[string]string
}

func (c *client) fetch(ctx context.Context, p pin) (info, error) {
	if p.kind == goMod {
		return c.goModule(ctx, p.pkg)
	}

	return c.npm(ctx, p.pkg)
}

// candidates lists the stable versions a pin may take, highest first.
func candidates(in info, major, maxMajor int) []candidate {
	var out []candidate

	for raw, peers := range in.versions {
		v, err := parseVersion(raw)
		if err != nil || !v.stable() || (major > 0 && v.major != major) || (maxMajor > 0 && v.major > maxMajor) {
			continue
		}

		out = append(out, candidate{v, peers})
	}

	// Equal versions differ only by build metadata (+incompatible); the plain one wins.
	slices.SortFunc(out, func(a, b candidate) int {
		if c := b.v.cmp(a.v); c != 0 {
			return c
		}

		return len(a.v.raw) - len(b.v.raw)
	})

	return out
}

// highest is the registry's latest tag when it is a usable stable candidate,
// otherwise the highest stable candidate.
func highest(in info, cands []candidate) (candidate, bool) {
	for _, c := range cands {
		if c.v.raw == in.latest {
			return c, true
		}
	}

	if len(cands) == 0 {
		return candidate{}, false
	}

	return cands[0], true
}

// resolveGroup returns the latest stable version of every pin of one group
// such that each chosen version satisfies the peerDependencies of the others.
func (c *client) resolveGroup(ctx context.Context, pins []pin) (map[string]string, error) {
	if len(pins) == 1 && (pins[0].kind == dockerImg || pins[0].kind == ghAction) {
		fn := c.latestImage
		if pins[0].kind == ghAction {
			fn = c.latestAction
		}

		v, err := fn(ctx, pins[0])

		return map[string]string{pins[0].pkg: v}, err
	}

	infos := map[string]info{}

	for _, p := range pins {
		in, err := c.fetch(ctx, p)
		if err != nil {
			return nil, err
		}

		infos[p.pkg] = in
	}

	cands := map[string][]candidate{}
	chosen := map[string]candidate{}

	for _, p := range pins {
		cands[p.pkg] = candidates(infos[p.pkg], int(p.major), int(p.holdMajor))

		top, ok := highest(infos[p.pkg], cands[p.pkg])
		if !ok {
			return nil, fmt.Errorf("%s: no stable version found", p.pkg)
		}

		chosen[p.pkg] = top
	}

	// A held pin is released when the tools it waits for name the next major.
	for _, p := range pins {
		if p.holdMajor == 0 || !released(p, chosen) {
			continue
		}

		cands[p.pkg] = candidates(infos[p.pkg], int(p.major), 0)
		chosen[p.pkg], _ = highest(infos[p.pkg], cands[p.pkg])
	}

	if err := settlePeers(pins, cands, chosen); err != nil {
		return nil, err
	}

	out := map[string]string{}
	for pkg, c := range chosen {
		out[pkg] = c.v.raw
	}

	return out, nil
}

func released(p pin, chosen map[string]candidate) bool {
	for _, tool := range p.holdUntil {
		c, ok := chosen[tool]
		if !ok || !namesMajor(c.peers[p.pkg], int(p.holdMajor)+1) {
			return false
		}
	}

	return len(p.holdUntil) > 0
}

// settlePeers lowers any pin whose chosen version is refused by a peer range
// of another chosen version, until the set is stable.
func settlePeers(pins []pin, cands map[string][]candidate, chosen map[string]candidate) error {
	for range 20 {
		changed := false

		for _, p := range pins {
			pick, ok, err := firstAllowed(p.pkg, cands[p.pkg], chosen)
			if err != nil {
				return err
			}

			if !ok {
				return fmt.Errorf(
					"%s: no stable version satisfies the peerDependencies of %s",
					p.pkg,
					peerOwners(p.pkg, chosen),
				)
			}

			if pick.v.raw != chosen[p.pkg].v.raw {
				chosen[p.pkg] = pick
				changed = true
			}
		}

		if !changed {
			return nil
		}
	}

	return fmt.Errorf("peerDependencies of %s did not settle", pins[0].file)
}

func firstAllowed(pkg string, cands []candidate, chosen map[string]candidate) (candidate, bool, error) {
	for _, c := range cands {
		allowed := true

		for owner, oc := range chosen {
			rng, ok := oc.peers[pkg]
			if !ok || owner == pkg {
				continue
			}

			ok, err := satisfies(c.v, rng)
			if err != nil {
				return candidate{}, false, fmt.Errorf("%s peer %s %q: %w", owner, pkg, rng, err)
			}

			if !ok {
				allowed = false

				break
			}
		}

		if allowed {
			return c, true, nil
		}
	}

	return candidate{}, false, nil
}

func peerOwners(pkg string, chosen map[string]candidate) string {
	var owners []string

	for owner, c := range chosen {
		if _, ok := c.peers[pkg]; ok {
			owners = append(owners, owner)
		}
	}

	slices.Sort(owners)

	return strings.Join(owners, ", ")
}
