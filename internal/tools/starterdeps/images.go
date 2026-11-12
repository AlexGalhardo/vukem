package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var imageRef = regexp.MustCompile(
	`([a-z0-9][a-z0-9._-]*(?:/[a-z0-9][a-z0-9._-]*)*):([A-Za-z0-9._-]+@sha256:[0-9a-f]{64})`,
)

var (
	tagShape    = regexp.MustCompile(`^(v?)(\d+(?:\.\d+)*)(.*)$`)
	alpineShape = regexp.MustCompile(`^(.*alpine)(\d+\.\d+)(.*)$`)
)

const maxTagPages = 40

// lockMinorImages stay on the minor line of the tag they carry. golang is
// the only one: its minor is the Go toolchain the repository builds with
// (the go directive of go.mod), and a minor bump is a toolchain decision.
// Every image stays on its major line; alpine variants follow the newest
// alpine release the image publishes.
var lockMinorImages = map[string]bool{"golang": true}

// tagPattern turns a pinned tag into the pattern of its sibling tags: same
// prefix, same number of numeric parts, same suffix, with the alpine release
// (alpine3.24) free to move.
func tagPattern(tag string) (*regexp.Regexp, version, error) {
	m := tagShape.FindStringSubmatch(tag)
	if m == nil {
		return nil, version{}, fmt.Errorf("tag %q has no version", tag)
	}

	core, suffix := m[2], m[3]
	parts := strings.Count(core, ".") + 1

	cur, err := parseLoose(core)
	if err != nil {
		return nil, version{}, err
	}

	suffixPat := regexp.QuoteMeta(suffix)
	if a := alpineShape.FindStringSubmatch(suffix); a != nil {
		suffixPat = regexp.QuoteMeta(a[1]) + `(\d+\.\d+)` + regexp.QuoteMeta(a[3])
	}

	re, err := regexp.Compile(
		`^` + regexp.QuoteMeta(m[1]) + `(\d+(?:\.\d+){` + strconv.Itoa(parts-1) + `})` + suffixPat + `$`,
	)

	return re, cur, err
}

// parseLoose reads 1, 1.2 or 1.2.3 (missing parts are zero).
func parseLoose(s string) (version, error) {
	var nums [3]int

	for i, p := range strings.Split(s, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || i > 2 {
			return version{}, fmt.Errorf("bad version %q", s)
		}

		nums[i] = n
	}

	return version{raw: s, major: nums[0], minor: nums[1], patch: nums[2]}, nil
}

type imageTag struct {
	name   string
	v      version
	alpine version
}

func (a imageTag) cmp(b imageTag) int {
	if c := a.v.cmp(b.v); c != 0 {
		return c
	}

	return a.alpine.cmp(b.alpine)
}

// latestImage returns "tag@sha256:digest" for the newest tag of the pinned
// image line, with the digest of its multi-arch index.
func (c *client) latestImage(ctx context.Context, p pin) (string, error) {
	tag, _, _ := strings.Cut(p.current, "@")

	re, cur, err := tagPattern(tag)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.pkg, err)
	}

	ns, name := "library", p.pkg
	if before, after, ok := strings.Cut(p.pkg, "/"); ok {
		ns, name = before, after
	}

	filter := strconv.Itoa(cur.major) + "."
	if p.lockMinor {
		filter += strconv.Itoa(cur.minor) + "."
	}

	best, ok, err := c.bestTag(ctx, ns, name, filter, re, cur, p.lockMinor)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.pkg, err)
	}

	if !ok {
		return "", fmt.Errorf("%s: no tag matches the shape of %q", p.pkg, tag)
	}

	digest, err := c.digest(ctx, ns+"/"+name, best.name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.pkg, err)
	}

	return best.name + "@" + digest, nil
}

func (c *client) bestTag(
	ctx context.Context,
	ns, name, filter string,
	re *regexp.Regexp,
	cur version,
	lockMinor bool,
) (imageTag, bool, error) {
	var best imageTag

	found := false
	next := fmt.Sprintf(
		"%s/v2/repositories/%s/%s/tags?page_size=100&name=%s",
		c.urls.hub,
		ns,
		name,
		url.QueryEscape(filter),
	)

	for page := 0; next != "" && page < maxTagPages; page++ {
		b, err := c.get(ctx, next, "")
		if err != nil {
			return best, false, err
		}

		var doc struct {
			Next    string `json:"next"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return best, false, fmt.Errorf("docker hub tags: %w", err)
		}

		for _, r := range doc.Results {
			m := re.FindStringSubmatch(r.Name)
			if m == nil {
				continue
			}

			v, err := parseLoose(m[1])
			if err != nil || v.major != cur.major || (lockMinor && v.minor != cur.minor) {
				continue
			}

			t := imageTag{name: r.Name, v: v}
			if len(m) > 2 {
				t.alpine, _ = parseLoose(m[2])
			}

			if !found || t.cmp(best) > 0 {
				best, found = t, true
			}
		}

		next = doc.Next
	}

	return best, found, nil
}

// digest resolves the digest of the manifest list (multi-arch index) behind
// a tag with an anonymous pull token.
func (c *client) digest(ctx context.Context, repo, tag string) (string, error) {
	b, err := c.get(ctx, fmt.Sprintf("%s?service=registry.docker.io&scope=repository:%s:pull", c.urls.auth, repo), "")
	if err != nil {
		return "", err
	}

	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &tok); err != nil {
		return "", fmt.Errorf("docker token: %w", err)
	}

	h, err := c.head(ctx, fmt.Sprintf("%s/v2/%s/manifests/%s", c.urls.registry, repo, tag), map[string]string{
		"Authorization": "Bearer " + tok.Token,
		"Accept": "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
			"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
	})
	if err != nil {
		return "", err
	}

	d := h.Get("Docker-Content-Digest")
	if !strings.HasPrefix(d, "sha256:") {
		return "", errors.New("registry returned no digest for " + repo + ":" + tag)
	}

	return d, nil
}
