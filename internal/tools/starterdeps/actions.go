package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// actionRef finds "uses: owner/repo@<sha> # vX.Y.Z" in generated workflows.
var actionRef = regexp.MustCompile(`(uses:\s*)([A-Za-z0-9._-]+/[A-Za-z0-9._/-]+)@([0-9a-f]{40} # \S+)`)

var releaseTag = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

const ghJSON = "application/vnd.github+json"

// latestAction returns "<commit sha> # <tag>" for the newest published
// release that is neither a draft nor a pre-release.
func (c *client) latestAction(ctx context.Context, p pin) (string, error) {
	owner, repo, ok := strings.Cut(p.pkg, "/")
	if !ok {
		return "", fmt.Errorf("action %q is not owner/repo", p.pkg)
	}

	repo, _, _ = strings.Cut(repo, "/")
	api := fmt.Sprintf("%s/repos/%s/%s", c.urls.github, owner, repo)

	b, err := c.get(ctx, api+"/releases?per_page=100", ghJSON)
	if err != nil {
		return "", err
	}

	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(b, &releases); err != nil {
		return "", fmt.Errorf("%s releases: %w", p.pkg, err)
	}

	var best version

	found := false

	for _, r := range releases {
		if r.Draft || r.Prerelease || !releaseTag.MatchString(r.Tag) {
			continue
		}

		v, err := parseVersion(r.Tag)
		if err != nil {
			continue
		}

		if !found || v.cmp(best) > 0 {
			best, found = v, true
		}
	}

	if !found {
		return "", fmt.Errorf("%s: no stable release found", p.pkg)
	}

	sha, err := c.commitOf(ctx, api, best.raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.pkg, err)
	}

	return sha + " # " + best.raw, nil
}

// commitOf resolves a tag to the commit it points at, dereferencing an
// annotated tag object.
func (c *client) commitOf(ctx context.Context, api, tag string) (string, error) {
	b, err := c.get(ctx, api+"/git/ref/tags/"+tag, ghJSON)
	if err != nil {
		return "", err
	}

	var ref struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		return "", err
	}

	if ref.Object.Type == "tag" {
		if b, err = c.get(ctx, api+"/git/tags/"+ref.Object.SHA, ghJSON); err != nil {
			return "", err
		}

		if err := json.Unmarshal(b, &ref); err != nil {
			return "", err
		}
	}

	if len(ref.Object.SHA) != 40 {
		return "", fmt.Errorf("tag %s has no commit sha", tag)
	}

	return ref.Object.SHA, nil
}
