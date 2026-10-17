package cli

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// scaffoldFS holds install:* scaffolds: user-owned code copied into a
// project. Like the starter, .tmpl files are rendered and the rest is
// copied verbatim.
//
//go:embed all:scaffolds
var scaffoldFS embed.FS

var socialProviders = []string{"github", "gitlab", "discord", "google", "microsoft", "linkedin", "x", "apple"}

func installAuth(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")
	socialList := set.String("social", "", "also add social login: "+strings.Join(socialProviders, ","))
	twoFactor := set.Bool("2fa", false, "also add two-factor authentication (TOTP)")

	return func(ctx context.Context, env *Env, _ []string) error {
		if hasTempl(env) {
			return usagef("install:auth writes html/template pages; templ projects are not supported yet")
		}

		providers, err := parseProviders(*socialList)
		if err != nil {
			return err
		}

		data, err := scaffoldData(env)
		if err != nil {
			return err
		}

		regs, err := installCore(env, data, *force)
		if err != nil {
			return err
		}

		// With --social or --2fa on a project that already has install:auth,
		// add only that part.
		addOn := len(providers) > 0 || *twoFactor
		if !addOn || *force || !exists(env, filepath.Join("routes", "auth.go")) {
			for _, name := range authScaffolds(env) {
				if err := installScaffold(env, name, data, *force); err != nil {
					return err
				}
			}

			regs = append(regs, registration{"routes/registry.go", "All", "Auth"})
		}

		if len(providers) > 0 {
			quoted := make([]string, len(providers))
			for i, p := range providers {
				quoted[i] = strconv.Quote(p)
			}

			data["Providers"] = strings.Join(quoted, ", ")
			data["FirstProvider"] = providers[0]

			if err := installScaffold(env, "auth-social", data, *force); err != nil {
				return err
			}

			regs = append(
				regs,
				registration{
					"database/migrations/registry.go",
					"All",
					"m" + data["TS3"] + "CreateSocialAccountsTable()",
				},
				registration{"routes/registry.go", "All", "Social"},
			)

			if err := appendEnvExample(env, socialEnv(providers)); err != nil {
				return err
			}
		}

		if *twoFactor {
			more, err := installTwoFactor(env, data, *force)
			if err != nil {
				return err
			}

			regs = append(regs, more...)
		}

		// Social sign-in also stops at the code step once 2FA is installed,
		// whichever of the two came first.
		if exists(env, filepath.Join("routes", "two_factor.go")) {
			if err := applyPatches(env, socialTwoFactorPatch); err != nil {
				return err
			}
		}

		if err := finishInstall(ctx, env, regs); err != nil {
			return err
		}

		fmt.Fprint(env.Stdout, "\nAuthentication installed. Next:\n\n"+
			"  vukem migrate\n"+
			"  set MAIL_MAILER (smtp or resend) and APP_URL so verification and reset emails work\n"+
			"  go test ./tests/...\n\n")

		if *twoFactor {
			fmt.Fprint(env.Stdout, "Two-factor authentication: users turn it on at /user/two-factor.\n\n")
		}

		return nil
	}
}

func installAPI(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(ctx context.Context, env *Env, _ []string) error {
		data, err := scaffoldData(env)
		if err != nil {
			return err
		}

		regs, err := installCore(env, data, *force)
		if err != nil {
			return err
		}

		if err := installScaffold(env, "api", data, *force); err != nil {
			return err
		}

		regs = append(regs, registration{"routes/registry.go", "All", "API"})

		// With two-factor authentication installed, tokens need the code too.
		if err := applyAPITwoFactor(env, data); err != nil {
			return err
		}

		if err := appendEnvExample(env, []string{"CORS_ALLOWED_ORIGINS"}); err != nil {
			return err
		}

		if err := finishInstall(ctx, env, regs); err != nil {
			return err
		}

		fmt.Fprint(env.Stdout, "\nAPI installed: POST /api/v1/tokens, GET /api/v1/me, DELETE /api/v1/tokens/current.\n"+
			"Run vukem migrate, and set CORS_ALLOWED_ORIGINS for browser clients.\n\n")

		return nil
	}
}

func parseProviders(list string) ([]string, error) {
	var providers []string

	for p := range strings.SplitSeq(list, ",") {
		if p = strings.TrimSpace(strings.ToLower(p)); p == "" {
			continue
		}

		if !slices.Contains(socialProviders, p) {
			return nil, usagef("unknown social provider %q (one of %s)", p, strings.Join(socialProviders, ", "))
		}

		providers = append(providers, p)
	}

	return providers, nil
}

func scaffoldData(env *Env) (map[string]string, error) {
	mod, err := modulePath(env)
	if err != nil {
		return nil, err
	}

	now := env.Now().UTC()

	return map[string]string{
		"Module": mod,
		"Name":   filepath.Base(env.dirOrDot()),
		"TS1":    now.Format("20060102150405"),
		"TS2":    now.Add(time.Second).Format("20060102150405"),
		"TS3":    now.Add(2 * time.Second).Format("20060102150405"),
		"TS4":    now.Add(3 * time.Second).Format("20060102150405"),
		"TS5":    now.Add(4 * time.Second).Format("20060102150405"),
	}, nil
}

// installCore installs the shared authentication core (users, accounts,
// tokens, mails) unless the project already has it.
func installCore(env *Env, data map[string]string, force bool) ([]registration, error) {
	if exists(env, filepath.Join("app", "accounts", "accounts.go")) && !force {
		return nil, nil
	}

	if err := installScaffold(env, "auth-core", data, force); err != nil {
		return nil, err
	}

	return []registration{
		{"database/migrations/registry.go", "All", "m" + data["TS1"] + "CreateUsersTable()"},
		{"database/migrations/registry.go", "All", "m" + data["TS2"] + "CreateAuthTables()"},
	}, nil
}

// finishInstall registers the new code and fetches its dependencies.
func finishInstall(ctx context.Context, env *Env, regs []registration) error {
	for _, reg := range regs {
		if err := register(env, reg.file, reg.fn, reg.expr); err != nil {
			return err
		}
	}

	// Scaffolds import packages (auth/hash → x/crypto) the starter did not.
	return tool(ctx, env, "go", "mod", "tidy")
}

func socialEnv(providers []string) []string {
	var keys []string

	for _, p := range providers {
		up := strings.ToUpper(p)
		if p == "apple" {
			// Apple signs a client secret with a .p8 key instead of sharing one.
			keys = append(keys, "SOCIAL_APPLE_CLIENT_ID", "SOCIAL_APPLE_TEAM_ID", "SOCIAL_APPLE_KEY_ID",
				"SOCIAL_APPLE_PRIVATE_KEY_PATH")

			continue
		}

		keys = append(keys, "SOCIAL_"+up+"_CLIENT_ID", "SOCIAL_"+up+"_CLIENT_SECRET")
	}

	return keys
}

type registration struct{ file, fn, expr string }

// appendEnvExample adds empty KEY= lines to .env.example for keys it does
// not mention yet.
func appendEnvExample(env *Env, keys []string) error {
	p := env.path(".env.example")

	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	text := string(b)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	for _, key := range keys {
		if !strings.Contains(text, key+"=") {
			text += key + "=\n"
		}
	}

	return writeFile(p, []byte(text))
}

// installScaffold renders scaffolds/<name> into the project. It checks
// every destination first, so nothing is written when a file exists.
func installScaffold(env *Env, name string, data map[string]string, force bool) error {
	root := "scaffolds/" + name

	type file struct {
		rel     string
		content []byte
	}

	var files []file

	err := fs.WalkDir(scaffoldFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel := strings.TrimPrefix(p, root+"/")
		rel = strings.NewReplacer(
			"TS1", data["TS1"], "TS2", data["TS2"], "TS3", data["TS3"], "TS4", data["TS4"], "TS5", data["TS5"],
		).Replace(rel)

		content, err := scaffoldFS.ReadFile(p)
		if err != nil {
			return err
		}

		if stub, ok := strings.CutSuffix(rel, ".tmpl"); ok {
			rel = stub

			if content, err = renderScaffold(rel, content, data); err != nil {
				return err
			}
		}

		files = append(files, file{rel, content})

		return nil
	})
	if err != nil {
		return err
	}

	if !force {
		var taken []string

		for _, f := range files {
			if exists(env, filepath.FromSlash(f.rel)) {
				taken = append(taken, f.rel)
			}
		}

		if len(taken) > 0 {
			return fmt.Errorf(
				"these files already exist (use --force to overwrite):\n  %s",
				strings.Join(taken, "\n  "),
			)
		}
	}

	for _, f := range files {
		dst := env.path(filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}

		if err := writeFile(dst, f.content); err != nil {
			return err
		}

		fmt.Fprintln(env.Stdout, "created", f.rel)
	}

	return nil
}

func renderScaffold(rel string, src []byte, data map[string]string) ([]byte, error) {
	t, err := template.New(rel).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("scaffold %s: %w", rel, err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("scaffold %s: %w", rel, err)
	}

	if !strings.HasSuffix(rel, ".go") {
		return b.Bytes(), nil
	}

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("scaffold %s produced invalid Go", rel), err)
	}

	return out, nil
}
