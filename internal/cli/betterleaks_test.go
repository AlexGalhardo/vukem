package cli

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fakeSecrets builds secrets in the formats the starter config must catch.
// They are random at run time, so no detectable value sits in the repository.
func fakeSecrets(t *testing.T) map[string]string {
	t.Helper()

	b32 := func() string {
		return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randBytes(t, 20))[:26]
	}
	alnum := strings.NewReplacer("+", "", "/", "", "=", "").Replace(base64.StdEncoding.EncodeToString(randBytes(t, 40)))

	return map[string]string{
		"vukem-app-key":         "APP_KEY=base64:" + base64.StdEncoding.EncodeToString(randBytes(t, 32)),
		"vukem-api-token":       "token := \"vk_" + b32() + "." + b32() + "\"",
		"stripe-secret-key":     "STRIPE_KEY=rk_live_" + alnum[:30],
		"stripe-webhook-secret": "STRIPE_WEBHOOK_SECRET=whsec_" + alnum[:32],
	}
}

func randBytes(t *testing.T, n int) []byte {
	t.Helper()

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}

	return b
}

// envExamplePlaceholders must stay quiet: they are what .env.example and the
// docs show. The Stripe placeholder is built at run time because GitHub push
// protection blocks the literal.
func envExamplePlaceholders() string {
	return `APP_KEY=
APP_PREVIOUS_KEYS=
STRIPE_KEY=sk_test_` + strings.Repeat("x", 24) + `
STRIPE_WEBHOOK_SECRET=whsec_placeholder_value_0000000000
API_TOKEN=vk_FAKE_FAKE_FAKE_FAKE_FAKE_FA.FAKE_FAKE_FAKE_FAKE_FAKE_FA
`
}

// TestStarterBetterleaksRules checks the starter rules with Go's RE2, the
// engine Betterleaks uses: each fake secret matches its rule.
func TestStarterBetterleaksRules(t *testing.T) {
	t.Parallel()

	cfg, err := renderStandardsFile("betterleaks.toml", standardsData{})
	if err != nil {
		t.Fatal(err)
	}

	rules := map[string]*regexp.Regexp{}
	id := ""

	for line := range strings.SplitSeq(string(cfg), "\n") {
		if v, ok := strings.CutPrefix(line, "id = "); ok {
			id = strings.Trim(v, `"`)
		}

		if v, ok := strings.CutPrefix(line, "regex = '''"); ok {
			rules[id] = regexp.MustCompile(strings.TrimSuffix(v, "'''"))
		}
	}

	for rule, secret := range fakeSecrets(t) {
		re, ok := rules[rule]
		if !ok {
			t.Errorf("rule %s missing", rule)
			continue
		}

		if !re.MatchString(secret) {
			t.Errorf("rule %s does not match %q", rule, secret)
		}
	}
}

// TestStarterBetterleaksScan runs the real scanner on a project with fake
// secrets and an .env.example of placeholders. It skips where Betterleaks is
// not installed; TestStarterBetterleaksRules covers the regexes everywhere.
func TestStarterBetterleaksScan(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("runs the betterleaks binary")
	}

	bin, err := exec.LookPath("betterleaks")
	if err != nil {
		t.Skip("betterleaks not installed")
	}

	dir := t.TempDir()
	cfg, err := renderStandardsFile("betterleaks.toml", standardsData{})
	if err != nil {
		t.Fatal(err)
	}

	secrets := fakeSecrets(t)
	var leaky strings.Builder

	for _, s := range secrets {
		leaky.WriteString(s + "\n")
	}

	for name, body := range map[string]string{
		".betterleaks.toml": string(cfg),
		"config.txt":        leaky.String(),
		".env.example":      envExamplePlaceholders(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.CommandContext(t.Context(), bin, "dir", dir, "--no-banner", "--redact",
		"--exit-code", "0", "--report-format", "json", "--report-path", report)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("betterleaks: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}

	var findings []struct {
		RuleID string `json:"RuleID"`
		File   string `json:"File"`
	}
	if err := json.Unmarshal(raw, &findings); err != nil {
		t.Fatal(err)
	}

	var got []string

	for _, f := range findings {
		if filepath.Base(f.File) != "config.txt" {
			t.Errorf("finding %s in %s, want only config.txt", f.RuleID, f.File)
		}

		got = append(got, f.RuleID)
	}

	for rule := range secrets {
		if !slices.Contains(got, rule) {
			t.Errorf("rule %s found nothing; findings %v", rule, got)
		}
	}
}

// TestBetterleaksLabelPassesCredentialStays is the false-positive rule: a
// password label is not a secret, and a credential in another field still is.
// The config must not ignore tests, docs, catalogs or every password.
func TestBetterleaksLabelPassesCredentialStays(t *testing.T) {
	t.Parallel()

	cfg, err := renderStandardsFile("betterleaks.toml", standardsData{})
	if err != nil {
		t.Fatal(err)
	}

	text := string(cfg)
	for _, banned := range []string{"generic-password", "paths", "tests/", "docs/", "catalog"} {
		if strings.Contains(text, banned) {
			t.Errorf("config opens a wide exception %q", banned)
		}
	}

	rules := map[string]*regexp.Regexp{}
	id := ""

	for line := range strings.SplitSeq(text, "\n") {
		if v, ok := strings.CutPrefix(line, "id = "); ok {
			id = strings.Trim(v, `"`)
		}

		if v, ok := strings.CutPrefix(line, "regex = '''"); ok {
			rules[id] = regexp.MustCompile(strings.TrimSuffix(v, "'''"))
		}
	}

	labels := "<label>Password</label>\n<label>Senha</label>\n"
	for rule, re := range rules {
		if re.MatchString(labels) {
			t.Errorf("rule %s matches a password label", rule)
		}
	}

	key := "note := \"base64:" + base64.StdEncoding.EncodeToString(randBytes(t, 32)) + "\""
	re, ok := rules["vukem-app-key"]
	if !ok || !re.MatchString(key) {
		t.Fatal("vukem-app-key missed the credential in another field")
	}

	hook, err := standardsFS.ReadFile("standards/githooks/pre-commit")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(hook), "betterleaks not installed, secret scan skipped (CI still runs it)") {
		t.Error("hook does not warn when the binary is missing")
	}

	ci, err := renderStandardsFile("workflows/ci.yml.tmpl", standardsData{Name: "blog", CI: true})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(ci), "run: betterleaks git . --no-banner --redact") {
		t.Error("CI does not scan the history")
	}

	if testing.Short() {
		return
	}

	bin, err := exec.LookPath("betterleaks")
	if err != nil {
		t.Skip("betterleaks not installed")
	}

	dir := t.TempDir()
	body := labels + "password := \"the password field\"\n" + key + "\n"
	for name, file := range map[string]string{".betterleaks.toml": text, "labels.txt": body} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.CommandContext(t.Context(), bin, "dir", dir, "--no-banner", "--redact",
		"--exit-code", "0", "--report-format", "json", "--report-path", report)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("betterleaks: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}

	var findings []struct {
		RuleID    string `json:"RuleID"`
		StartLine int    `json:"StartLine"`
	}
	if err := json.Unmarshal(raw, &findings); err != nil {
		t.Fatal(err)
	}

	var sawKey, sawPassword bool

	for _, f := range findings {
		if f.StartLine == 1 || f.StartLine == 2 {
			t.Errorf("label line %d reported as %s", f.StartLine, f.RuleID)
		}

		if f.RuleID == "vukem-app-key" && f.StartLine == 4 {
			sawKey = true
		}

		if f.RuleID == "generic-password" && f.StartLine == 3 {
			sawPassword = true
		}
	}

	if !sawKey {
		t.Error("credential in another field was not detected")
	}

	if !sawPassword {
		t.Error("generic-password did not fire; the password rule looks disabled")
	}
}
