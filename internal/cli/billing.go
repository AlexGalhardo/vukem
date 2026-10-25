package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	stripeModule = frameworkModule + "/contrib/stripe"
	// stripeVersion is the contrib/stripe release install:billing pins: the
	// latest contrib/stripe tag (TestStripeVersionIsLatestTag). Bump it
	// after tagging the module.
	stripeVersion = "v0.4.1"
)

// billingEnv is appended to .env.example; values stay empty (never real keys).
const billingEnv = `
# Stripe (vukem install:billing). Develop in a sandbox: stripe sandbox create.
# A restricted key (rk_...) with only the billing permissions.
STRIPE_KEY=
# stripe listen --forward-to localhost:3000/stripe/webhook prints whsec_...
STRIPE_WEBHOOK_SECRET=
# Recurring price ids (price_...) of the plans in config/billing.go.
STRIPE_PRICE_BASIC=
STRIPE_PRICE_PRO=
`

func installBilling(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")

	return func(ctx context.Context, env *Env, _ []string) error {
		if !exists(env, filepath.Join("app", "accounts", "accounts.go")) ||
			!exists(env, filepath.Join("routes", "auth.go")) {
			return usagef("install:billing bills the users of install:auth: run vukem install:auth first")
		}

		data, err := scaffoldData(env)
		if err != nil {
			return err
		}

		names := []string{"billing", "billing-web"}
		if fw := inertiaFramework(env); fw != "" {
			names = []string{"billing", "billing-inertia", "billing-inertia-" + fw}
		}

		for _, name := range names {
			if err := installScaffold(env, name, data, *force); err != nil {
				return err
			}
		}

		if err := appendEnvBlock(env, "STRIPE_KEY=", billingEnv); err != nil {
			return err
		}

		if err := requireStripe(ctx, env); err != nil {
			return err
		}

		regs := []registration{
			{"database/migrations/registry.go", "All", "m" + data["TS5"] + "AddBillingTables()"},
			{"routes/registry.go", "All", "Billing"},
		}
		if err := finishInstall(ctx, env, regs); err != nil {
			return err
		}

		fmt.Fprint(env.Stdout, "\nBilling installed: GET /billing, POST /billing/checkout/{plan}, "+
			"POST /billing/portal, POST /stripe/webhook. Next:\n\n"+
			"  vukem migrate\n"+
			"  stripe sandbox create, then set STRIPE_KEY (a restricted rk_ key) and STRIPE_PRICE_* in .env\n"+
			"  stripe listen --forward-to localhost:3000/stripe/webhook, and set STRIPE_WEBHOOK_SECRET\n"+
			"  protect routes with b.Subscribed() or b.OnPlan(\"pro\") (see routes/billing.go)\n"+
			"  go test ./tests/...\n\n")

		return nil
	}
}

// requireStripe adds contrib/stripe to go.mod: the release pinned above,
// or the module next to a local framework checkout (vukem new --framework).
func requireStripe(ctx context.Context, env *Env) error {
	mod, err := os.ReadFile(env.path("go.mod"))
	if err != nil {
		return err
	}

	if bytes.Contains(mod, []byte(stripeModule+" ")) {
		return nil
	}

	local := localFramework(mod)
	if local == "" {
		return tool(ctx, env, "go", "get", stripeModule+"@"+stripeVersion)
	}

	f, err := os.OpenFile(env.path("go.mod"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(f, "\nrequire %s v0.0.0\n\nreplace %s => %s/contrib/stripe\n", stripeModule, stripeModule,
		strings.TrimSuffix(local, "/"))

	return errors.Join(err, f.Close())
}

// localFramework returns the path of a `replace` of the framework module to
// a local directory, or "".
func localFramework(mod []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(mod))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		rest, ok := strings.CutPrefix(line, "replace "+frameworkModule+" => ")
		if !ok {
			continue
		}

		if strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "/") || filepath.IsAbs(rest) {
			return rest
		}
	}

	return ""
}

// appendEnvBlock appends block to .env.example unless it already has marker.
func appendEnvBlock(env *Env, marker, block string) error {
	p := env.path(".env.example")

	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	if bytes.Contains(b, []byte(marker)) {
		return nil
	}

	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}

	if bytes.Contains(b, []byte("\r\n")) {
		block = strings.ReplaceAll(block, "\n", "\r\n")
	}

	return writeFile(p, append(b, block...))
}
