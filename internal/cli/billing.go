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
	"regexp"
	"slices"
	"strings"
)

const (
	stripeModule = frameworkModule + "/contrib/stripe"
	// stripeVersion is the contrib/stripe release install:billing pins: the
	// latest contrib/stripe tag (TestStripeVersionIsLatestTag). Bump it
	// after tagging the module.
	stripeVersion = "v0.4.1"
)

// billingEnvFor is the block appended to .env.example; values stay empty
// (never real keys).
func billingEnvFor(plans []string) string {
	var prices strings.Builder
	for _, p := range plans {
		prices.WriteString("STRIPE_PRICE_" + strings.ToUpper(p) + "=\n")
	}

	return `
# Stripe (vukem install:billing). Develop in a sandbox: stripe sandbox create.
# A restricted key (rk_...) with only the billing permissions.
STRIPE_KEY=
# stripe listen --forward-to localhost:3000/stripe/webhook prints whsec_...
STRIPE_WEBHOOK_SECRET=
# Recurring price ids (price_...) of the plans in config/billing.go.
` + prices.String() + `# Currencies a checkout may ask for (e.g. brl,usd,eur); empty uses each
# price's default currency. Prices need currency_options for the others.
STRIPE_CURRENCIES=
`
}

var planNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// billingPlans parses the -plans flag: lower-case names that are valid in
// a URL and in a STRIPE_PRICE_<NAME> variable.
func billingPlans(list string) ([]string, error) {
	var plans []string

	for p := range strings.SplitSeq(list, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if !planNameRe.MatchString(p) {
			return nil, usagef(
				"install:billing: plan name %q must be a lower-case letter followed by letters, digits or _",
				p,
			)
		}

		if slices.Contains(plans, p) {
			return nil, usagef("install:billing: plan %q is listed twice", p)
		}

		plans = append(plans, p)
	}

	return plans, nil
}

// planData adds the template values for plans: the plans() entries, the
// test price variables and the first plan, which the generated tests use.
func planData(data map[string]string, plans []string) {
	list := make([]string, len(plans))
	env := make([]string, len(plans))

	for i, p := range plans {
		list[i] = fmt.Sprintf("\t\t{Name: %q, Label: %q},", p, planLabel(p))
		env[i] = fmt.Sprintf("%q: %q,", "STRIPE_PRICE_"+strings.ToUpper(p), "price_"+p)
	}

	data["Plan"], data["PlanLabel"] = plans[0], planLabel(plans[0])
	data["PlanList"] = strings.Join(list, "\n")
	data["PriceEnv"] = strings.Join(env, " ")
}

// planLabel turns business_plus into "Business plus".
func planLabel(name string) string {
	label := strings.ReplaceAll(name, "_", " ")
	return strings.ToUpper(label[:1]) + label[1:]
}

func installBilling(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite existing files")
	planList := set.String("plans", "basic,pro", "comma-separated plan names to sell, e.g. pro,business")

	return func(ctx context.Context, env *Env, _ []string) error {
		if !exists(env, filepath.Join("app", "accounts", "accounts.go")) ||
			!exists(env, filepath.Join("routes", "auth.go")) {
			return usagef("install:billing bills the users of install:auth: run vukem install:auth first")
		}

		plans, err := billingPlans(*planList)
		if err != nil {
			return err
		}

		data, err := scaffoldData(env)
		if err != nil {
			return err
		}

		planData(data, plans)

		names := []string{"billing", "billing-web"}
		if fw := inertiaFramework(env); fw != "" {
			names = []string{"billing", "billing-inertia", "billing-inertia-" + fw}
		}

		for _, name := range names {
			if err := installScaffold(env, name, data, *force); err != nil {
				return err
			}
		}

		if err := appendEnvBlock(env, "STRIPE_KEY=", billingEnvFor(plans)); err != nil {
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
			"  protect routes with b.Subscribed() or b.OnPlan(\""+plans[0]+"\") (see routes/billing.go)\n"+
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
