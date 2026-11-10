package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/AlexGalhardo/vukem/config"
)

func keyGenerate(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	show := set.Bool("show", false, "print the key instead of writing .env")
	force := set.Bool("force", false, "replace an existing APP_KEY (invalidates sessions and encrypted data)")
	ifMissing := set.Bool(
		"if-missing",
		false,
		"write a key only when APP_KEY is empty; never print it and never replace one",
	)

	return func(_ context.Context, env *Env, _ []string) error {
		if *ifMissing {
			if *show || *force {
				return errors.New(
					"key:generate --if-missing cannot be combined with --show or --force (it never prints or replaces a key; use key:rotate to change one)",
				)
			}

			return writeAppKeyIfMissing(env)
		}

		key := config.GenerateKey()

		if *show {
			fmt.Fprintln(env.Stdout, key)
			return nil
		}

		return writeAppKey(env, key, *force)
	}
}

// writeAppKeyIfMissing is the idempotent setup. A set key is kept and is
// not printed. It does not rotate: APP_PREVIOUS_KEYS is left alone.
func writeAppKeyIfMissing(env *Env) error {
	p := env.path(".env")

	src, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	set, err := appKeySet(string(src))
	if err != nil {
		return err
	}

	if set {
		fmt.Fprintln(env.Stderr, "APP_KEY already set")
		return nil
	}

	return writeAppKey(env, config.GenerateKey(), false)
}

// appKeySet reports whether text defines a non-empty APP_KEY the way
// config.ParseEnv reads it (export, spaces and quotes; the last line wins).
func appKeySet(text string) (bool, error) {
	entries, err := config.ParseEnv(strings.NewReader(text))
	if err != nil {
		return false, err
	}

	set := false

	for _, e := range entries {
		if e.Key == "APP_KEY" {
			set = strings.TrimSpace(e.Value) != ""
		}
	}

	return set, nil
}

// writeAppKey sets APP_KEY in .env, creating it from .env.example when
// missing. An existing non-empty key is kept unless force is set.
func writeAppKey(env *Env, key string, force bool) error {
	p := env.path(".env")

	src, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		src, err = os.ReadFile(env.path(".env.example"))
		if errors.Is(err, fs.ErrNotExist) {
			src, err = nil, nil
		}
	}

	if err != nil {
		return err
	}

	text := string(src)

	set, err := appKeySet(text)
	if err != nil {
		return err
	}

	if set && !force {
		return errors.New(
			"APP_KEY is already set in .env (use --force to replace it; existing sessions and encrypted data become unreadable)",
		)
	}

	if re := envLine("APP_KEY"); re.MatchString(text) {
		text = setEnvValue(re, text, key)
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}

		text += "APP_KEY=" + key + "\n"
	}

	if err := writeFileAtomic(p, []byte(text)); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "APP_KEY written to .env")

	return nil
}
