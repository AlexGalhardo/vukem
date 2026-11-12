package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// financeFramework is the Inertia adapter of the finance example: its pages,
// admin and emails are React (vukem new rejects other stacks).
const financeFramework = "react"

// financeDropped lists the starter files the finance app does not keep: the
// Posts demo, whose pages, controller, model and seeders the app replaces.
var financeDropped = []string{
	"app/controllers/post_controller.go", "app/models/post.go", "app/requests/post.go",
	"database/factories/post_factory.go", "database/seeders/posts_seeder.go",
	"database/migrations/20260101000000_create_posts_table.go", "tests/posts_test.go",
	"resources/js/components/PostForm.tsx", "resources/js/pages/Posts",
	// The mails of auth-core are React Email components here (resources/js/emails).
	"resources/views/mail/verify_email.html", "resources/views/mail/reset_password.html",
}

// financeEdits are the edits to files of the starter and of the auth and
// admin installers that the finance app needs.
func financeEdits() []exampleEdit {
	return []exampleEdit{
		{"database/migrations/registry.go", "\t\tm20260101000000CreatePostsTable(),\n", ""},
		{
			"database/seeders/registry.go",
			"\treturn []vukem.Seeder{\n\t\tPostsSeeder{},\n\t}\n",
			"\treturn []vukem.Seeder{}\n",
		},
		// No resource has a policy yet: the parameter would be unused.
		{"app/admin/registry.go", "func Policies(admins Admins)", "func Policies(_ Admins)"},
		// The panel's feature flags page needs install:flags, which this app does not use.
		{"tests/admin_test.go", "\tc.Get(\"/admin-api/_flags\").AssertOK().AssertSee(\"Feature flags\")\n", ""},
		// /dashboard is the finance dashboard (routes/finance.go, inside a workspace).
		{"routes/auth.go", "\n\tr.Get(\"/dashboard\", h.Dashboard).Name(\"dashboard\").Use(required, verified)\n", ""},
		{
			"routes/auth.go",
			"\t// Signed-in pages also need a verified email; unverified users are sent\n" +
				"\t// to the verification notice.\n\tverified := auth.Verified(acc.Web)\n", "",
		},
		{
			"app/controllers/account/account.go",
			"\n// Dashboard is the first page after login; the user arrives as the shared\n" +
				"// auth.user prop.\n" +
				"func (*Handlers) Dashboard(c *web.Ctx) error { return pages.Dashboard.Render(c, struct{}{}) }\n",
			"",
		},
		{
			"app/pages/auth.go", `Dashboard      = inertia.Define[struct{}]("Dashboard")`,
			`Dashboard      = inertia.Define[DashboardProps]("Dashboard")`,
		},
		{"app/mail/auth.go", `mail.View("mail/verify_email", m)`, `mail.React("verify_email", m)`},
		{"app/mail/auth.go", `mail.View("mail/reset_password", m)`, `mail.React("reset_password", m)`},
		{
			"bootstrap/app.go", "\t\tvukem.WithVite(resources.Public()),\n",
			"\t\tvukem.WithVite(resources.Public()),\n\t\tvukem.WithReactEmails(resources.Emails()),\n",
		},
		{
			"resources/resources.go", "//go:embed views all:public\n", "//go:embed views all:public emails\n",
		},
		{
			"resources/resources.go", "// Public returns the static assets served at /.\n",
			"// Emails returns the React Email templates exported by npm run build:emails.\n" +
				"func Emails() fs.FS { return dirOrEmbedded(\"emails\") }\n\n" +
				"// Public returns the static assets served at /.\n",
		},
		{
			"package.json", `"typecheck": "tsc --noEmit"`,
			`"typecheck": "tsc --noEmit",` + "\n    " +
				`"build:emails": "email export --dir resources/js/emails --outDir resources/emails"`,
		},
		{
			"package.json", "\"devDependencies\": {\n",
			"\"devDependencies\": {\n    \"@react-email/components\": \"" + reactEmailComponentsVersion + "\",\n" +
				"    \"react-email\": \"" + reactEmailCLIVersion + "\",\n",
		},
	}
}

// financeEnv is appended to .env.example: the finance settings beside the
// admin and Stripe ones the installers add.
const financeEnv = `
# Mail. Development writes mails to the log; production sends them with Resend:
# MAIL_MAILER=resend, a verified sending domain in MAIL_FROM, an API key and
# the signing secret of the webhook that reports bounces and complaints
# (Resend dashboard, Webhooks: POST /webhooks/resend, email.bounced and
# email.complained).
MAIL_MAILER=log
MAIL_FROM="Finance <no-reply@example.com>"
RESEND_API_KEY=
RESEND_WEBHOOK_SECRET=
`

// scaffoldFinance turns the Inertia + React starter in env.Dir into the
// personal finance SaaS: auth with two-factor, the admin panel, workspaces
// (tenancy), Stripe billing per workspace, Resend mail with React Email, and
// the accounts, transactions, budgets, dashboard and CSV import. It runs
// before the Go module is resolved, so it only writes files and registers
// code; go mod tidy and vukem codegen:pages run after it.
func scaffoldFinance(_ context.Context, env *Env, _ string, _ starterData) error {
	if inertiaFramework(env) != financeFramework {
		return usagef("the finance example needs the Inertia + React stack")
	}

	for _, rel := range financeDropped {
		if err := os.RemoveAll(env.path(filepath.FromSlash(rel))); err != nil {
			return err
		}
	}

	data, err := authData(env, "")
	if err != nil {
		return err
	}

	// The overlay first: it replaces the starter's pages.go, routes and
	// schedule, and the installers below register their own parts after it.
	if err := installScaffold(env, "finance", data, true); err != nil {
		return err
	}

	regs := []registration{
		{"database/migrations/registry.go", "All", "m" + data["TS6"] + "CreateFinanceTables()"},
		{"routes/registry.go", "All", "Finance"},
	}

	more, err := installFinanceAuth(env, data)
	if err != nil {
		return err
	}

	regs = append(regs, more...)

	more, err = installFinanceAdmin(env, data)
	if err != nil {
		return err
	}

	regs = append(regs, more...)

	if err := installFinanceBilling(env, data); err != nil {
		return err
	}

	// The React pages last: they replace the starter's Home and the
	// installers' Dashboard and layout.
	if err := installScaffold(env, "finance-inertia-react", data, true); err != nil {
		return err
	}

	for _, e := range financeEdits() {
		if err := patchFile(env, e.file, e.old, e.new); err != nil {
			return err
		}
	}

	for _, reg := range regs {
		if err := register(env, reg.file, reg.fn, reg.expr); err != nil {
			return err
		}
	}

	if err := appendEnvBlock(env, "RESEND_API_KEY=", financeEnv); err != nil {
		return err
	}

	fmt.Fprintln(env.Stdout, "finance: workspaces, billing, mail, import and admin added")

	return nil
}

// installFinanceAuth is install:auth --2fa without the go mod tidy and
// codegen the installer runs at its end.
func installFinanceAuth(env *Env, data map[string]string) ([]registration, error) {
	regs, err := installCore(env, data, false)
	if err != nil {
		return nil, err
	}

	more, err := installAuthParts(env, data, nil, authOpts{twoFactor: true})
	if err != nil {
		return nil, err
	}

	return append(regs, more...), nil
}

// installFinanceAdmin is install:admin --stack inertia without the tidy.
func installFinanceAdmin(env *Env, data map[string]string) ([]registration, error) {
	data["AdminStack"] = "inertia"

	for _, name := range []string{"admin", "admin-inertia-react"} {
		if err := installScaffold(env, name, data, true); err != nil {
			return nil, err
		}
	}

	if err := appendEnvExample(env, []string{"ADMIN_EMAILS"}); err != nil {
		return nil, err
	}

	regs := []registration{
		{"database/migrations/registry.go", "All", "m" + data["TS1"] + "CreateAdminAuditLogsTable()"},
		{"routes/registry.go", "All", "Admin"},
	}

	return append(regs, pageRegistrations(env, "AdminDashboard", "AdminResourceList", "AdminResourceShow")...), nil
}

// installFinanceBilling writes config/billing.go (the Stripe settings and the
// Pro plan) from the billing scaffold; the billing logic is the finance app's
// own, per workspace. contrib/stripe is pinned in go.mod and go mod tidy,
// which runs after the module is resolved, fetches it.
func installFinanceBilling(env *Env, data map[string]string) error {
	planData(data, []string{"pro"})

	files, err := collectScaffold("billing", data)
	if err != nil {
		return err
	}

	for _, f := range files {
		if path.Base(f.rel) != "billing.go" || path.Dir(f.rel) != "config" {
			continue
		}

		dst := env.path(filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}

		if err := writeFile(dst, f.content); err != nil {
			return err
		}

		fmt.Fprintln(env.Stdout, "created", f.rel)
	}

	if err := appendEnvBlock(env, "STRIPE_KEY=", billingEnvFor([]string{"pro"})); err != nil {
		return err
	}

	return pinStripe(env)
}

// pinStripe requires contrib/stripe at the release install:billing pins. It
// only edits go.mod: this runs before the framework is resolved, and the go
// mod tidy that follows fetches the module. With --framework the stripe
// module comes from the proxy and builds against the local framework replace.
func pinStripe(env *Env) error {
	mod, err := os.ReadFile(env.path("go.mod"))
	if err != nil {
		return err
	}

	if bytes.Contains(mod, []byte(stripeModule+" ")) {
		return nil
	}

	if !bytes.HasSuffix(mod, []byte("\n")) {
		mod = append(mod, '\n')
	}

	mod = append(mod, fmt.Sprintf("\nrequire %s %s\n", stripeModule, stripeVersion)...)

	return writeFile(env.path("go.mod"), mod)
}
