package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/config"
)

// maxPreviousKeys is the APP_PREVIOUS_KEYS length above which key:rotate
// warns: old keys are meant to be dropped after their overlap window.
const maxPreviousKeys = 3

// envLine matches every assignment of one .env variable without its line
// ending, so CRLF files stay CRLF. Group 1 is the lead (indent and an
// optional export), group 2 the name and "=", group 3 the value and any
// inline comment.
func envLine(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^([ \t]*(?:export[ \t]+)?)(` + name + `[ \t]*=[ \t]*)([^\r\n]*)`)
}

// setEnvValue replaces the value of every re line in text with value
// (unquoted: keys are base64), keeping the lead and any inline comment.
func setEnvValue(re *regexp.Regexp, text, value string) string {
	return re.ReplaceAllStringFunc(text, func(line string) string {
		m := re.FindStringSubmatch(line)
		return m[1] + m[2] + value + inlineComment(m[3])
	})
}

// inlineComment returns what follows the value in rest (the line after
// "="): the text after the closing quote of a quoted value, or from " #" or
// "\t#" for an unquoted one, as config.ParseEnv reads it.
func inlineComment(rest string) string {
	if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
		for i := 1; i < len(rest); i++ {
			if rest[0] == '"' && rest[i] == '\\' {
				i++
				continue
			}

			if rest[i] == rest[0] {
				return rest[i+1:]
			}
		}

		return ""
	}

	idx := -1

	for _, sep := range []string{" #", "\t#"} {
		if i := strings.Index(rest, sep); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}

	if idx < 0 {
		return ""
	}

	return rest[idx:]
}

// writeFileAtomic replaces path with data through a fsynced temp file in
// the same directory, so a crash never leaves a truncated .env and the keys
// in it lost. Symlinks are resolved first, so a linked .env stays linked.
func writeFileAtomic(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(path), ".env.tmp-*")
	if err != nil {
		return err
	}

	tmp := f.Name()

	err = writeSynced(f, data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp, path)
	}

	if err != nil {
		_ = os.Remove(tmp)
	}

	return err
}

func writeSynced(f *os.File, data []byte) error {
	if err := f.Chmod(0o600); err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		return err
	}

	return f.Sync()
}

// keyRotate replaces APP_KEY in .env with a new key and moves the current
// one to the front of APP_PREVIOUS_KEYS, so everything it sealed or signed
// keeps opening until it is removed from there. Keys are never printed.
func keyRotate(_ *flag.FlagSet) func(context.Context, *Env, []string) error {
	return func(_ context.Context, env *Env, args []string) error {
		if len(args) > 0 {
			return usagef("key:rotate takes no arguments")
		}

		return rotateAppKey(env, config.GenerateKey())
	}
}

func rotateAppKey(env *Env, next string) error {
	p := env.path(".env")

	src, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("no .env to rotate APP_KEY in (run `vukem key:generate` first)")
	}

	if err != nil {
		return err
	}

	entries, err := config.ParseEnv(strings.NewReader(string(src)))
	if err != nil {
		return fmt.Errorf(".env: %w", err)
	}

	var current, previous string

	for _, e := range entries {
		switch e.Key {
		case "APP_KEY":
			current = e.Value
		case "APP_PREVIOUS_KEYS":
			previous = e.Value
		}
	}

	if current == "" {
		return errors.New("APP_KEY is not set in .env: nothing to rotate (run `vukem key:generate`)")
	}

	// A malformed previous key is a boot error, so never move one there.
	if _, err := config.ParseKey(config.NewSecret(current)); err != nil {
		return fmt.Errorf("APP_KEY in .env is invalid, refusing to keep it as a previous key: %w", err)
	}

	keys := []string{current}

	for i, k := range strings.Split(previous, ",") {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}

		if _, err := config.ParseKey(config.NewSecret(k)); err != nil {
			return fmt.Errorf(
				"APP_PREVIOUS_KEYS entry %d in .env is invalid (the app would refuse to boot): %w",
				i+1,
				err,
			)
		}

		if k != next && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}

	keyLine := envLine("APP_KEY")
	text := setEnvValue(keyLine, string(src), next)
	prevValue := strings.Join(keys, ",")

	if re := envLine("APP_PREVIOUS_KEYS"); re.MatchString(text) {
		text = setEnvValue(re, text, prevValue)
	} else {
		nl := "\n"
		if strings.Contains(text, "\r\n") {
			nl = "\r\n"
		}

		// The new line follows the APP_KEY line's indent and export.
		m := keyLine.FindStringSubmatchIndex(text)
		text = text[:m[1]] + nl + text[m[2]:m[3]] + "APP_PREVIOUS_KEYS=" + prevValue + text[m[1]:]
	}

	if err := writeFileAtomic(p, []byte(text)); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "APP_KEY rotated in .env; the old key is now first in APP_PREVIOUS_KEYS.")
	fmt.Fprintln(env.Stdout, "Restart the app. Remove the old key from APP_PREVIOUS_KEYS once the sessions and links")
	fmt.Fprintln(
		env.Stdout,
		"it signed have expired and encrypted columns are re-saved (docs/security.md#key-rotation).",
	)

	if len(keys) > maxPreviousKeys {
		fmt.Fprintf(env.Stderr,
			"warning: APP_PREVIOUS_KEYS now holds %d keys; each one can still forge sessions and signed links, "+
				"so remove those whose sessions, links and encrypted data are gone\n", len(keys))
	}

	return nil
}
