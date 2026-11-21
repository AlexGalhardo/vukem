package main

import (
	"fmt"
	"regexp"
	"strings"
)

var commitLinkRe = regexp.MustCompile(`\]\(` + regexp.QuoteMeta(repoURL) + `/commit/([0-9a-f]{40})\)`)

// linkedCommits lists the commit ids a section links, without duplicates.
func linkedCommits(notes string) []string {
	var ids []string

	seen := map[string]bool{}

	for _, m := range commitLinkRe.FindAllStringSubmatch(notes, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}

	return ids
}

// notAncestors returns the commits of ids that are not an ancestor of ref
// (or that the repository does not know at all). A rebase merge rewrites the
// commits of a pull request, so the ids a -prepare run linked on the branch
// are gone from the history the tag will contain.
func notAncestors(ids []string, ref string) []string {
	var missing []string

	for _, id := range ids {
		if _, err := gitIn(".", "merge-base", "--is-ancestor", id, ref); err != nil {
			missing = append(missing, id)
		}
	}

	return missing
}

// verifyLinkedCommits fails when a commit linked by version's section is not
// an ancestor of ref.
func verifyLinkedCommits(changelog, version, ref string) error {
	notes := releaseNotes(changelog, version)
	if notes == "" {
		return fmt.Errorf("CHANGELOG.md has no section for %s", version)
	}

	missing := notAncestors(linkedCommits(notes), ref)
	if len(missing) == 0 {
		return nil
	}

	short := make([]string, len(missing))
	for i, id := range missing {
		short[i] = id[:7]
	}

	return fmt.Errorf(
		"%d commit link(s) of the %s section are not ancestors of %s (a rebase merge rewrites them): %s",
		len(missing), version, ref, strings.Join(short, ", "),
	)
}

// verifySectionCurrent fails unless the committed section of version is what
// the commits between the previous release and ref generate now, so a link a
// rebase merge replaced, and a commit added after -prepare, both stop the tag.
func verifySectionCurrent(changelog, version, ref string) error {
	if err := verifyLinkedCommits(changelog, version, ref); err != nil {
		return stalePrepared(version, err)
	}

	fresh, _, err := buildSection(ref, version)
	if err != nil {
		return err
	}

	if releaseNotes(fresh, version) != releaseNotes(changelog, version) {
		return stalePrepared(version, fmt.Errorf(
			"the %s section differs from the commits between the previous release and %s", version, ref))
	}

	return nil
}

func stalePrepared(version string, cause error) error {
	return fmt.Errorf("%w: branch from main, run -prepare %s again (it replaces the untagged section with one "+
		"generated from the commits now on main), merge that pull request, then run -tag", cause, version)
}

// dropSection removes the section of version from changelog, so -prepare can
// generate it again. The caller refuses a version that is already tagged:
// released sections stay immutable.
func dropSection(changelog, version string) string {
	heading := "## [" + strings.TrimPrefix(version, "v") + "]"

	start := strings.Index(changelog, heading)
	if start < 0 {
		return changelog
	}

	rest := changelog[start+len(heading):]
	end := len(changelog)

	if i := strings.Index(rest, "\n## ["); i >= 0 {
		end = start + len(heading) + i + 1
	} else if loc := linkLineRe.FindStringIndex(rest); loc != nil {
		end = start + len(heading) + loc[0]
	}

	return changelog[:start] + changelog[end:]
}
