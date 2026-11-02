package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInstallBillingNeedsAuth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"go.mod": "module example.com/app\n\ngo 1.27\n"})

	code, out, errOut := vukem(t, dir, "install:billing")
	if code != 2 || !strings.Contains(errOut, "run vukem install:auth first") {
		t.Fatalf("install:billing = %d\n%s%s", code, out, errOut)
	}

	if _, err := os.Stat(filepath.Join(dir, "routes", "billing.go")); err == nil {
		t.Error("install:billing wrote files without install:auth")
	}
}

func TestLocalFramework(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, mod, want string
	}{
		{"release", "module a\n\nrequire github.com/AlexGalhardo/vukem v0.19.0\n", ""},
		{"local", "module a\n\nreplace github.com/AlexGalhardo/vukem => C:/src/vukem\n", "C:/src/vukem"},
		{"relative", "module a\n\nreplace github.com/AlexGalhardo/vukem => ../vukem\n", "../vukem"},
		{"other module", "module a\n\nreplace github.com/AlexGalhardo/vukem/contrib/stripe => ../s\n", ""},
		{"version", "module a\n\nreplace github.com/AlexGalhardo/vukem => github.com/fork/vukem v1.0.0\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.name == "local" && !filepath.IsAbs("C:/src/vukem") {
				t.Skip("drive paths are absolute on Windows only")
			}

			if got := localFramework([]byte(tt.mod)); got != tt.want {
				t.Errorf("localFramework(%q) = %q, want %q", tt.mod, got, tt.want)
			}
		})
	}
}

func TestAppendEnvBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{".env.example": "APP_NAME=a\r\nAPP_KEY=\r\n"})
	env := &Env{Dir: dir}

	for range 2 {
		if err := appendEnvBlock(env, "STRIPE_KEY=", billingEnvFor([]string{"basic", "pro"})); err != nil {
			t.Fatal(err)
		}
	}

	got := read(t, dir, ".env.example")
	if strings.Count(got, "STRIPE_KEY=") != 1 || !strings.Contains(got, "STRIPE_WEBHOOK_SECRET=\r\n") ||
		strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf(".env.example = %q", got)
	}
}

// The generated webhook runs outside sessions and CSRF (Stripe is a
// server) but only with a verified Stripe-Signature, each event once, and
// it refuses every request while no signing secret is set. Checkout never
// pins payment methods, and only tests can point the client away from
// api.stripe.com.
func TestSecurity_A08_BillingWebhookOutsideCSRFButSigned(t *testing.T) {
	t.Parallel()

	files := map[string][]string{
		"scaffolds/billing/routes/billing.go.tmpl": {
			"r.UseServer(b.Webhook())",
			"events.Listen(app.Events, b.HandleWebhook)",
			`h.Checkout).Name("billing.checkout").Use(required, verified)`,
			`h.Portal).Name("billing.portal").Use(required, verified)`,
		},
		"scaffolds/billing/app/billing/billing.go.tmpl": {
			"stripe.WebhookHandler(stripe.WebhookConfig{",
			"Secret: cfg.WebhookSecret.Reveal(), Store: c.Store(), Bus: app.Events",
			"if b.webhook == nil {",
			"http.StatusServiceUnavailable)",
			`base != "" && app.Kind == vukem.KindTest`,
			`if app.Config.IsProduction() {`,
			`cs.PaymentStatus != "paid" && cs.PaymentStatus != "no_payment_required"`,
			`WhereNull("stripe_customer_id")`,
			`return ok && sub.Valid() && allowed(sub)`,
		},
		"scaffolds/billing/tests/billing_test.go.tmpl": {
			"AssertStatus(http.StatusBadRequest)",
			"the duplicate delivery was processed again",
			"Coming back from Checkout grants nothing",
		},
	}

	for src, wants := range files {
		b, err := scaffoldFS.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range wants {
			if !bytes.Contains(b, []byte(want)) {
				t.Errorf("%s lacks %q", src, want)
			}
		}
	}

	svc, err := scaffoldFS.ReadFile("scaffolds/billing/app/billing/billing.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(svc, []byte(`"payment_method_types"`)) {
		t.Error("checkout pins payment_method_types; dynamic payment methods come from the Dashboard")
	}

	if !regexp.MustCompile(`"mode":\s+"subscription"`).Match(svc) {
		t.Error("checkout is not in subscription mode")
	}
}

// TestStripeVersionIsLatestTag keeps install:billing on the newest
// contrib/stripe release: bump stripeVersion after tagging the module. It
// skips without git or tags locally, but fails on GitHub Actions, whose
// test job fetches the tags: a skip there would hide a stale pin.
func TestStripeVersionIsLatestTag(t *testing.T) {
	t.Parallel()

	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(stripeVersion) {
		t.Fatalf("stripeVersion = %q, want a release tag vX.Y.Z", stripeVersion)
	}

	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}

	out, err := exec.CommandContext(t.Context(), git, "tag", "-l", "contrib/stripe/v*", "--sort=-v:refname").Output()
	if err != nil {
		t.Skipf("git tag: %v", err)
	}

	tags := strings.Fields(string(out))
	if len(tags) == 0 {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("no contrib/stripe tags in this CI checkout: fetch them (actions/checkout fetch-depth: 0)")
		}

		t.Skip("no contrib/stripe tags in this checkout")
	}

	if latest := strings.TrimPrefix(tags[0], "contrib/stripe/"); stripeVersion != latest {
		t.Errorf("stripeVersion = %s, want the latest contrib/stripe tag %s", stripeVersion, latest)
	}
}

func TestBillingPlans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"basic,pro", []string{"basic", "pro"}, false},
		{" Pro , business_plus ", []string{"pro", "business_plus"}, false},
		{"solo", []string{"solo"}, false},
		{"", nil, true},
		{"pro,pro", nil, true},
		{"pro,gold-tier", nil, true},
		{"9lives", nil, true},
		{"pro,,team", nil, true},
	}

	for _, tt := range tests {
		got, err := billingPlans(tt.in)
		if (err != nil) != tt.wantErr || strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("billingPlans(%q) = %v, %v; want %v (error %v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}

	data := map[string]string{}
	planData(data, []string{"pro", "business_plus"})

	for key, want := range map[string]string{
		"Plan": "pro", "PlanLabel": "Pro",
		"PlanList": "\t\t{Name: \"pro\", Label: \"Pro\"},\n\t\t{Name: \"business_plus\", Label: \"Business plus\"},",
		"PriceEnv": `"STRIPE_PRICE_PRO": "price_pro", "STRIPE_PRICE_BUSINESS_PLUS": "price_business_plus",`,
	} {
		if data[key] != want {
			t.Errorf("planData %s = %q, want %q", key, data[key], want)
		}
	}

	if env := billingEnvFor(
		[]string{"pro", "business_plus"},
	); !strings.Contains(
		env,
		"STRIPE_PRICE_PRO=\nSTRIPE_PRICE_BUSINESS_PLUS=\n",
	) ||
		!strings.Contains(env, "STRIPE_CURRENCIES=\n") {
		t.Errorf("billingEnvFor = %q", env)
	}
}
