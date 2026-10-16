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

var socialProviders = []string{"github", "gitlab", "discord", "google", "microsoft"}

func installAuth(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")
	socialList := set.String("social", "", "also add social login: "+strings.Join(socialProviders, ","))

	return func(ctx context.Context, env *Env, _ []string) error {
		mod, err := modulePath(env)
		if err != nil {
			return err
		}

		var providers []string

		for p := range strings.SplitSeq(*socialList, ",") {
			if p = strings.TrimSpace(strings.ToLower(p)); p == "" {
				continue
			}

			if !slices.Contains(socialProviders, p) {
				return usagef("unknown social provider %q (one of %s)", p, strings.Join(socialProviders, ", "))
			}

			providers = append(providers, p)
		}

		now := env.Now().UTC()
		data := map[string]string{
			"Module": mod,
			"TS1":    now.Format("20060102150405"),
			"TS2":    now.Add(time.Second).Format("20060102150405"),
			"TS3":    now.Add(2 * time.Second).Format("20060102150405"),
		}

		var regs []registration

		// With --social on a project that already has install:auth, add only
		// the social part.
		if len(providers) == 0 || *force || !exists(env, filepath.Join("app", "accounts", "accounts.go")) {
			if err := installScaffold(env, "auth", data, *force); err != nil {
				return err
			}

			regs = append(regs,
				registration{"database/migrations/registry.go", "All", "m" + data["TS1"] + "CreateUsersTable()"},
				registration{"database/migrations/registry.go", "All", "m" + data["TS2"] + "CreateAuthTables()"},
				registration{"routes/registry.go", "All", "Auth"},
			)
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

			if err := appendEnvExample(env, providers); err != nil {
				return err
			}
		}

		for _, reg := range regs {
			if err := register(env, reg.file, reg.fn, reg.expr); err != nil {
				return err
			}
		}

		// The scaffold imports packages (auth/hash → x/crypto) the starter did not.
		if err := tool(ctx, env, "go", "mod", "tidy"); err != nil {
			return err
		}

		fmt.Fprint(env.Stdout, "\nAuthentication installed. Next:\n\n"+
			"  vukem migrate\n"+
			"  set MAIL_MAILER (smtp or resend) and APP_URL so verification and reset emails work\n"+
			"  go test ./tests/...\n\n")

		return nil
	}
}

type registration struct{ file, fn, expr string }

// appendEnvExample documents the provider credentials in .env.example.
func appendEnvExample(env *Env, providers []string) error {
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

	for _, pr := range providers {
		key := "SOCIAL_" + strings.ToUpper(pr) + "_CLIENT_ID"
		if strings.Contains(text, key) {
			continue
		}

		text += key + "=\nSOCIAL_" + strings.ToUpper(pr) + "_CLIENT_SECRET=\n"
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
		rel = strings.NewReplacer("TS1", data["TS1"], "TS2", data["TS2"], "TS3", data["TS3"]).Replace(rel)

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
