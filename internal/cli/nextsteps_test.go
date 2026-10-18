package cli

import (
	"strings"
	"testing"
)

// TestStarterREADMEDocumentationLinks keeps the generated README pointing at
// every documentation category of the site (site/content/navigation.ts).
func TestStarterREADMEDocumentationLinks(t *testing.T) {
	t.Parallel()

	b, err := starterFS.ReadFile("starter/README.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	readme := string(b)

	for name, href := range map[string]string{
		"Getting started": "/docs/introduction",
		"Architecture":    "/docs/architecture/lifecycle",
		"The basics":      "/docs/basics/routing",
		"APIs":            "/docs/rest-apis/overview",
		"Database":        "/docs/database/overview",
		"Security":        "/docs/security/overview",
		"Digging deeper":  "/docs/digging-deeper/mail",
		"Frontend":        "/docs/frontend/htmx",
		"Testing":         "/docs/testing/overview",
		"CLI":             "/docs/cli/overview",
		"Deployment":      "/docs/deployment/single-binary",
	} {
		if !strings.Contains(readme, "["+name+"](https://vukem.dev"+href+")") {
			t.Errorf("README does not link %q to %s", name, href)
		}
	}
}

// TestNextStepsCoversTheHandover checks the post-install output answers the
// questions a new project has, each with its documentation link.
func TestNextStepsCoversTheHandover(t *testing.T) {
	t.Parallel()

	out := nextSteps("blog", "htmx, Tailwind, SQLite", "htmx", standardsOpts{docker: true}, pmByName["bun"], true)

	for _, want := range []string{
		".env", docs + "/configuration",
		"pick a database", docs + "/database/overview",
		"vukem migrate", docs + "/database/migrations",
		"go test ./...", docs + "/testing/overview",
		"MAIL_MAILER", docs + "/digging-deeper/mail",
		"docker compose up", "vukem serve", docs + "/deployment/docker-and-railway",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("next steps do not mention %q:\n%s", want, out)
		}
	}

	// A database-less project has nothing to migrate.
	out = nextSteps("blog", "no database", "minimal", standardsOpts{}, pmByName["bun"], true)
	if strings.Contains(out, "vukem migrate") {
		t.Errorf("--minimal next steps mention migrate:\n%s", out)
	}
}

// --docker with --no-install (or without the package manager on PATH)
// leaves the project without a lockfile, and the image's assets stage
// installs from one. The handover has to name the command that writes it,
// before the step that tells the reader to build the image.
func TestNextStepsNameTheInstallTheImageNeeds(t *testing.T) {
	t.Parallel()

	out := nextSteps("blog", "htmx, Tailwind, SQLite", "htmx", standardsOpts{docker: true}, pmByName["npm"], false)

	install, compose := strings.Index(out, "npm install"), strings.Index(out, "docker compose up")
	if install < 0 {
		t.Fatalf("next steps do not name the install the image needs:\n%s", out)
	}

	if compose < 0 || install > compose {
		t.Errorf("the install step does not come before the image build:\n%s", out)
	}

	// With a lockfile there is nothing to say, and an API project builds no
	// assets at all.
	for _, out := range []string{
		nextSteps("blog", "htmx", "htmx", standardsOpts{docker: true}, pmByName["npm"], true),
		nextSteps("shop", "JSON API", "api", standardsOpts{docker: true}, pmByName["npm"], false),
	} {
		if strings.Contains(out, "npm install") {
			t.Errorf("next steps ask for an install that is not needed:\n%s", out)
		}
	}
}
