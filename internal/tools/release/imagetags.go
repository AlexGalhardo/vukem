package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// stableVersionRe matches a product version with no pre-release suffix.
// Module tags, pins and pre-releases do not match.
var stableVersionRe = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// semver is a parsed stable product version.
type semver struct {
	major int
	minor int
	patch int
}

func parseStable(tag string) (semver, bool) {
	m := stableVersionRe.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return semver{}, false
	}

	major, errMajor := strconv.Atoi(m[1])
	minor, errMinor := strconv.Atoi(m[2])
	patch, errPatch := strconv.Atoi(m[3])

	if errMajor != nil || errMinor != nil || errPatch != nil {
		return semver{}, false
	}

	return semver{major: major, minor: minor, patch: patch}, true
}

func (s semver) less(o semver) bool {
	if s.major != o.major {
		return s.major < o.major
	}

	if s.minor != o.minor {
		return s.minor < o.minor
	}

	return s.patch < o.patch
}

func (s semver) equal(o semver) bool {
	return s.major == o.major && s.minor == o.minor && s.patch == o.patch
}

// canonicalTag adds the leading v when version is a bare X.Y.Z.
func canonicalTag(version string) string {
	version = strings.TrimSpace(version)
	if strings.HasPrefix(version, "v") {
		return version
	}

	return "v" + version
}

// floatingImageTags reports whether version should also publish the X.Y and
// latest image tags. Both move only when version is a stable vX.Y.Z and no
// name in published is a higher stable vX.Y.Z. Tags that are not vX.Y.Z
// (ffmpeg pins, module tags, pre-releases) are ignored.
func floatingImageTags(version string, published []string) bool {
	candidate, ok := parseStable(canonicalTag(version))
	if !ok {
		return false
	}

	best := candidate

	for _, raw := range published {
		parsed, stable := parseStable(raw)
		if !stable {
			continue
		}

		if best.less(parsed) {
			best = parsed
		}
	}

	return best.equal(candidate)
}

// imageTagLines lists the registry tags for one image. The exact version is
// always included. X.Y and latest are included only when floatingImageTags
// reports that version is the highest stable product version.
func imageTagLines(image, version string, published []string) ([]string, error) {
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("image name is empty")
	}

	tag := canonicalTag(version)
	if !validVersion(tag) {
		return nil, fmt.Errorf("not a product version: %s", version)
	}

	bare := strings.TrimPrefix(tag, "v")
	lines := []string{image + ":" + bare}

	if !floatingImageTags(tag, published) {
		return lines, nil
	}

	minor := bare[:strings.LastIndex(bare, ".")]
	lines = append(lines, image+":"+minor, image+":latest")

	return lines, nil
}

// printImageTags writes one tag per line for each image. published comes
// from tagsFile (one tag name per line) when it is set.
func printImageTags(out io.Writer, version string, images []string, tagsFile string) error {
	published, err := readTagNames(tagsFile)
	if err != nil {
		return err
	}

	if len(images) == 0 {
		return errUsageImageTags
	}

	for _, image := range images {
		lines, err := imageTagLines(image, version, published)
		if err != nil {
			return err
		}

		for _, line := range lines {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	}

	return nil
}

func readTagNames(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tags file: %w", err)
	}

	var names []string

	for line := range strings.Lines(string(body)) {
		name := strings.TrimSpace(line)
		if name != "" {
			names = append(names, name)
		}
	}

	return names, nil
}
