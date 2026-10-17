package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
)

// exception allows one incompatible change after v1: a security fix that must
// break an unsafe API in a minor or patch release (docs/stability.md).
type exception struct {
	// Module is the module path from go.mod.
	Module string `json:"module"`
	// Symbol is the import path and identifier as the gate prints it,
	// e.g. github.com/AlexGalhardo/vukem/auth/hash.DefaultArgon2.
	Symbol string `json:"symbol"`
	// Change is the apidiff message, matched exactly ("removed", "changed
	// from func() to func(int)").
	Change        string `json:"change"`
	Justification string `json:"justification"`
	// Advisory links the GitHub security advisory or issue (https).
	Advisory string `json:"advisory"`
	// Version is the module release that ships the break (vX.Y.Z). Once the
	// module's base tag reaches it the change leaves the diff, so the entry
	// is expired and must be removed.
	Version string `json:"version"`
}

func (e exception) matches(modPath string, c change) bool {
	return e.Module == modPath && e.Symbol == importPath(modPath, c.Package)+"."+c.Subject && e.Change == c.Message
}

// loadExceptions reads and validates the exception file; a missing file means
// no exceptions.
func loadExceptions(name string, mods []module) ([]exception, error) {
	b, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	exs, err := parseExceptions(bytes.NewReader(b), mods)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	return exs, nil
}

// parseExceptions decodes {"exceptions": [...]} strictly (unknown fields and
// trailing data are errors) and validates every entry against mods.
func parseExceptions(r io.Reader, mods []module) ([]exception, error) {
	var file struct {
		Exceptions []exception `json:"exceptions"`
	}

	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("malformed exception file: %w", err)
	}

	// dec.More reports false before a stray "}", so read on to EOF.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("malformed exception file: trailing data after the JSON object")
	}

	var errs []error

	for i, e := range file.Exceptions {
		if err := e.validate(mods); err != nil {
			errs = append(errs, fmt.Errorf("exception %d (%s): %w", i, e.Symbol, err))
		}

		for _, prev := range file.Exceptions[:i] {
			if prev.Module == e.Module && prev.Symbol == e.Symbol && prev.Change == e.Change {
				errs = append(errs, fmt.Errorf("exception %d (%s): duplicate entry", i, e.Symbol))
			}
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return file.Exceptions, nil
}

func (e exception) validate(mods []module) error {
	var errs []error

	for field, v := range map[string]string{
		"module": e.Module, "symbol": e.Symbol, "change": e.Change,
		"justification": e.Justification, "advisory": e.Advisory, "version": e.Version,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("missing %s", field))
		}
	}

	if e.Module != "" && !slices.ContainsFunc(mods, func(m module) bool { return m.Path == e.Module }) {
		errs = append(errs, fmt.Errorf("unknown module %q", e.Module))
	}

	if e.Symbol != "" && e.Module != "" && !strings.HasPrefix(e.Symbol, e.Module+".") &&
		!strings.HasPrefix(e.Symbol, e.Module+"/") {
		errs = append(errs, fmt.Errorf("symbol is not in module %s", e.Module))
	}

	if e.Advisory != "" {
		if u, err := url.Parse(e.Advisory); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, fmt.Errorf("advisory %q is not an https link", e.Advisory))
		}
	}

	if v, ok := parseStable(e.Version); e.Version != "" && !ok {
		errs = append(errs, fmt.Errorf("version %q is not vMAJOR.MINOR.PATCH", e.Version))
	} else if ok && e.Module != "" && !majorMatches(e.Module, v.major) {
		// Otherwise a v2.x entry in a v1 module would never expire.
		errs = append(errs, fmt.Errorf("version %q is not a release of module %s", e.Version, e.Module))
	}

	// Map iteration is random; sort for a stable message.
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })

	return errors.Join(errs...)
}

// majorMatches reports whether major is a major version of modPath: N for a
// /vN path (N >= 2), 0 or 1 otherwise.
func majorMatches(modPath string, major int) bool {
	if _, suffix, ok := strings.CutLast(modPath, "/v"); ok {
		if n, err := strconv.Atoi(suffix); err == nil && n >= 2 {
			return major == n
		}
	}

	return major <= 1
}

// applyExceptions marks the incompatible changes of r covered by an exception
// as excepted and returns the indexes of the exceptions it used. An exception
// for r's module whose version is not newer than the base tag has expired.
func applyExceptions(r *result, exs []exception) ([]int, error) {
	var (
		used []int
		errs []error
	)

	base, _ := parseStable(strings.TrimPrefix(r.Base, r.Module.tagPrefix()))

	for i, e := range exs {
		if e.Module != r.Module.Path {
			continue
		}

		if v, _ := parseStable(e.Version); v.compare(base) <= 0 {
			errs = append(errs, fmt.Errorf("exception %d (%s): expired: base tag %s already ships %s; remove it",
				i, e.Symbol, r.Base, e.Version))

			continue
		}

		for j, c := range r.Changes {
			if c.Incompatible && !c.Excepted && e.matches(r.Module.Path, c) {
				r.Changes[j].Excepted = true

				used = append(used, i)

				break
			}
		}
	}

	return used, errors.Join(errs...)
}

// unusedExceptions reports every exception that matched no change: the gate
// fails on them so the list never carries stale or mistyped entries.
func unusedExceptions(exs []exception, used []int) error {
	var errs []error

	for i, e := range exs {
		if !slices.Contains(used, i) {
			errs = append(errs, fmt.Errorf("exception %d (%s: %s) is unused; remove it or fix the entry",
				i, e.Symbol, e.Change))
		}
	}

	return errors.Join(errs...)
}
