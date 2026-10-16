package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/AlexGalhardo/golang-framework/config"
)

var appKeyLine = regexp.MustCompile(`(?m)^APP_KEY=(.*?)\r?$`)

func keyGenerate(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	show := set.Bool("show", false, "print the key instead of writing .env")
	force := set.Bool("force", false, "replace an existing APP_KEY (invalidates sessions and encrypted data)")

	return func(_ context.Context, env *Env, _ []string) error {
		key := config.GenerateKey()

		if *show {
			fmt.Fprintln(env.Stdout, key)
			return nil
		}

		return writeAppKey(env, key, *force)
	}
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

	switch m := appKeyLine.FindStringSubmatchIndex(text); {
	case m == nil:
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}

		text += "APP_KEY=" + key + "\n"
	case strings.TrimSpace(text[m[2]:m[3]]) != "" && !force:
		return errors.New(
			"APP_KEY is already set in .env (use --force to replace it; existing sessions and encrypted data become unreadable)",
		)
	default:
		text = text[:m[2]] + key + text[m[3]:]
	}

	if err := os.WriteFile(p, []byte(text), 0o600); err != nil { //nolint:gosec // G703: the project .env
		return err
	}

	fmt.Fprintln(env.Stdout, "APP_KEY written to .env")

	return nil
}
