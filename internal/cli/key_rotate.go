package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/config"
)

// envLine matches every assignment of one .env variable (optional export
// and spaces) without its line ending, so CRLF files stay CRLF.
func envLine(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?` + name + `[ \t]*=[^\r\n]*`)
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

	for k := range strings.SplitSeq(previous, ",") {
		if k = strings.TrimSpace(k); k != "" && k != next && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}

	keyLine := envLine("APP_KEY")
	text := keyLine.ReplaceAllLiteralString(string(src), "APP_KEY="+next)
	prevLine := "APP_PREVIOUS_KEYS=" + strings.Join(keys, ",")

	if re := envLine("APP_PREVIOUS_KEYS"); re.MatchString(text) {
		text = re.ReplaceAllLiteralString(text, prevLine)
	} else {
		nl := "\n"
		if strings.Contains(text, "\r\n") {
			nl = "\r\n"
		}

		end := keyLine.FindStringIndex(text)[1]
		text = text[:end] + nl + prevLine + text[end:]
	}

	if err := os.WriteFile(p, []byte(text), 0o600); err != nil { //nolint:gosec // G703: the project .env
		return err
	}

	fmt.Fprintln(env.Stdout, "APP_KEY rotated in .env; the old key is now first in APP_PREVIOUS_KEYS.")
	fmt.Fprintln(env.Stdout, "Restart the app. Remove the old key from APP_PREVIOUS_KEYS once the sessions and links")
	fmt.Fprintln(
		env.Stdout,
		"it signed have expired and encrypted columns are re-saved (docs/security.md#key-rotation).",
	)

	return nil
}
