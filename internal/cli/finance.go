package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/config"
)

// Editions of the finance app, written to APP_EDITION in .env.
const (
	editionPersonal = "personal"
	editionSaaS     = "saas"
)

var financeEditions = []string{editionPersonal, editionSaaS}

// checkEdition validates --edition: only the finance example has editions.
func checkEdition(o newOpts, kind string) error {
	if o.edition == "" {
		return nil
	}

	if kind != exampleFinance {
		return usagef("--edition is only valid with --example finance")
	}

	if !slices.Contains(financeEditions, o.edition) {
		return usagef("--edition must be %s, not %q", strings.Join(financeEditions, " or "), o.edition)
	}

	return nil
}

// checkFinanceFlags refuses the flags that contradict the app the example
// writes: it ships its own Dockerfile and installs with Bun (it carries
// bun.lock).
func checkFinanceFlags(o newOpts) error {
	if o.pmFlag != "" && !strings.EqualFold(o.pmFlag, "bun") {
		return usagef("--example finance installs with Bun (the app ships bun.lock): drop --pm %s", o.pmFlag)
	}

	if o.assetsImage != "" {
		return usagef("--example finance ships its own Dockerfile: drop --assets-image")
	}

	return nil
}

// runFinanceProject creates the finance example: the exported vukem-finances
// tree rendered for the project, a go.mod for the chosen framework, the
// standards the flags ask for and the steps every example shares. It does not
// start from the Inertia starter and patches nothing.
func runFinanceProject(ctx context.Context, env *Env, name, dir string, o newOpts) error {
	st, err := financeStack(o)
	if err != nil {
		return err
	}

	if err := checkFinanceFlags(o); err != nil {
		return err
	}

	std := o.std
	askStandards(env, &std)
	std = std.withStack(st.kind)

	pm := pmByName["bun"]

	data := starterData{Name: name, Module: o.module, PM: pm.name}
	if data.Module == "" {
		data.Module = name
	}

	project := *env
	project.Dir = dir

	if err := writeFinanceScaffold(&project, data, std.license != ""); err != nil {
		return err
	}

	if err := writeFinanceGoMod(&project, data); err != nil {
		return err
	}

	if err := writeFinanceStandards(&project, newStandardsData(data, st.kind, std, env.Now().Year()), std); err != nil {
		return err
	}

	if err := resolveModules(ctx, &project, st, o.framework); err != nil {
		return err
	}

	if err := writeAppKey(&project, config.GenerateKey(), false); err != nil {
		return err
	}

	if err := writeEdition(&project, o.edition); err != nil {
		return err
	}

	if o.seed {
		if err := seedFinance(ctx, &project); err != nil {
			return err
		}
	}

	gitInit(ctx, env, &project)
	jsInstall(ctx, &project, pm, o.noInstall)

	// The app always ships Dockerfile and compose.yaml.
	std.docker = true

	desc := "vukem-finances " + financeAppVersion() + ", Inertia + react, SQLite"
	fmt.Fprint(env.Stdout, nextSteps(name, desc, st.kind, std, pm, true))

	return nil
}

// writeFinanceScaffold renders the exported tree into project.Dir. Files with
// the .tmpl suffix are rendered with data and lose the suffix; the others are
// copied byte for byte. MANIFEST.sha256 and VERSION describe the export and
// are not written. The app's MIT text becomes NOTICE, unless the project gets
// a LICENSE of its own (--license), when it stays as LICENSE.vukem-finances.
func writeFinanceScaffold(project *Env, data starterData, ownLicense bool) error {
	files, err := loadFinanceScaffold()
	if err != nil {
		return err
	}

	for _, f := range files {
		rel, content := f.rel, f.content

		switch rel {
		case financeManifestName, financeVersionName:
			continue
		case financeLicenseName:
			if !ownLicense {
				rel = "NOTICE"
			}
		}

		if stub, ok := strings.CutSuffix(rel, ".tmpl"); ok {
			rel = stub

			if content, err = renderStarterFile(rel, content, data); err != nil {
				return err
			}
		}

		dst := project.path(filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}

		perm := os.FileMode(0o600)
		if f.exec {
			perm = 0o700
		}

		if err := os.WriteFile(dst, content, perm); err != nil {
			return err
		}
	}

	// The export carries no storage/ (runtime data); the SQLite file needs the folder.
	if err := os.MkdirAll(project.path("storage"), 0o750); err != nil {
		return err
	}

	fmt.Fprintf(project.Stdout, "finance: wrote vukem-finances %s\n", financeAppVersion())

	return nil
}

// writeFinanceGoMod writes go.mod (the exported tree has none) with the
// starter's Go directive and the contrib/stripe release the app pins. The
// framework requirement is added by resolveModules.
func writeFinanceGoMod(project *Env, data starterData) error {
	src, err := starterFS.ReadFile("starter/go.mod.tmpl")
	if err != nil {
		return err
	}

	mod, err := renderStarterFile("go.mod", src, data)
	if err != nil {
		return err
	}

	mod = append(mod, fmt.Sprintf("\nrequire %s %s\n", stripeModule, financeStripeVersion)...)

	return writeFile(project.path("go.mod"), mod)
}

// writeFinanceStandards writes the files the standards flags select, except
// those the app already ships: it keeps its own compose.yaml, SECURITY.md,
// .editorconfig and so on, and says so in one line. package.json, the README
// and .env.example are the app's and are not patched.
func writeFinanceStandards(project *Env, data standardsData, std standardsOpts) error {
	var kept []string

	for _, f := range standardsSet(std) {
		if exists(project, f.dst) {
			// .editorconfig and .gitattributes are always in the set: the line is for what a flag asked for.
			if f.dst != ".editorconfig" && f.dst != ".gitattributes" {
				kept = append(kept, f.dst)
			}

			continue
		}

		if f.dst == ".github/CODEOWNERS" && data.Owner == "" {
			continue
		}

		if err := renderStandards(project, f, data); err != nil {
			return err
		}
	}

	if len(kept) > 0 {
		fmt.Fprintf(project.Stdout, "finance: kept the app's %s (the flags do not overwrite them)\n",
			strings.Join(kept, ", "))
	}

	return nil
}

// writeEdition sets APP_EDITION in .env. The default (no --edition) is what
// .env.example already says, personal.
func writeEdition(project *Env, edition string) error {
	if edition == "" {
		return nil
	}

	p := project.path(".env")

	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	text := string(src)

	re := envLine("APP_EDITION")
	if re.MatchString(text) {
		return writeFile(p, []byte(setEnvValue(re, text, edition)))
	}

	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}

	return writeFile(p, []byte(text+"APP_EDITION="+edition+"\n"))
}

// seedFinance migrates the new finance app and runs its idempotent local
// seeders. It is the --seed path of vukem new. A second db:seed keeps the
// existing password, credential file and demo rows.
func seedFinance(ctx context.Context, env *Env) error {
	fmt.Fprintln(env.Stdout, "finance: migrating and seeding local admin and demo data")

	if err := delegate(ctx, env, []string{"migrate"}); err != nil {
		return err
	}

	return delegate(ctx, env, []string{"db:seed"})
}
