package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const repoURL = "https://github.com/AlexGalhardo/vukem"

var (
	versionRe  = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?$`)
	linkLineRe = regexp.MustCompile(`(?m)^\[[^\]]+\]: \S+\n?`)
)

// validVersion reports whether v is a SemVer tag with a leading "v".
func validVersion(v string) bool { return versionRe.MatchString(v) }

// insertRelease adds section (a "## [x.y.z] - date" block) right below the
// "## [Unreleased]" heading and rewrites the link references at the end of
// the file. Released sections are never modified.
func insertRelease(changelog, section, version, previous string) (string, error) {
	const unreleased = "## [Unreleased]"

	idx := strings.Index(changelog, unreleased)
	if idx < 0 {
		return "", errors.New("CHANGELOG.md has no \"## [Unreleased]\" heading")
	}

	bare := strings.TrimPrefix(version, "v")
	if strings.Contains(changelog, "## ["+bare+"]") {
		return "", fmt.Errorf("CHANGELOG.md already has a section for %s", bare)
	}

	section = strings.TrimSpace(section)
	if !strings.HasPrefix(section, "## ["+bare+"]") {
		return "", fmt.Errorf("generated section does not start with \"## [%s]\"", bare)
	}

	// Drop the existing link block; it is regenerated below.
	body := strings.TrimRight(linkLineRe.ReplaceAllString(changelog, ""), "\n")

	after := idx + len(unreleased)

	// Keep any [Unreleased] notes written by hand (rare) above the release.
	nextSection := strings.Index(body[after:], "\n## [")

	var unreleasedNotes, rest string
	if nextSection < 0 {
		unreleasedNotes, rest = body[after:], ""
	} else {
		unreleasedNotes, rest = body[after:after+nextSection], body[after+nextSection:]
	}

	var b strings.Builder

	b.WriteString(body[:after])

	if notes := strings.TrimSpace(unreleasedNotes); notes != "" {
		b.WriteString("\n\n" + notes)
	}

	b.WriteString("\n\n" + section + "\n")
	b.WriteString(strings.TrimRight(rest, "\n"))
	b.WriteString("\n\n")
	b.WriteString(links(b.String(), version, previous))

	return b.String(), nil
}

var sectionRe = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+[^\]]*)\]`)

// links renders the compare/tag link references for every released section.
func links(changelog, latest, previous string) string {
	versions := sectionRe.FindAllStringSubmatch(changelog, -1)

	var b strings.Builder

	fmt.Fprintf(&b, "[Unreleased]: %s/compare/%s...HEAD\n", repoURL, latest)

	for i, m := range versions {
		v := m[1]

		var prev string

		switch {
		case i+1 < len(versions):
			prev = "v" + versions[i+1][1]
		case "v"+v == latest:
			prev = previous
		}

		if prev == "" {
			fmt.Fprintf(&b, "[%s]: %s/releases/tag/v%s\n", v, repoURL, v)
			continue
		}

		fmt.Fprintf(&b, "[%s]: %s/compare/%s...v%s\n", v, repoURL, prev, v)
	}

	return b.String()
}

// releaseNotes extracts the body of version's section (without heading).
func releaseNotes(changelog, version string) string {
	heading := "## [" + strings.TrimPrefix(version, "v") + "]"

	start := strings.Index(changelog, heading)
	if start < 0 {
		return ""
	}

	rest := changelog[start:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return ""
	}

	end := len(rest)
	if i := strings.Index(rest, "\n## ["); i >= 0 {
		end = i
	}

	if loc := linkLineRe.FindStringIndex(rest[:end]); loc != nil {
		end = loc[0]
	}

	return strings.TrimSpace(rest[:end]) + "\n"
}
