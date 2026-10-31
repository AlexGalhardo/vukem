package vukem

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/openapi"
)

// defaultFuzzImage is the pinned Schemathesis image api:fuzz runs. Pass
// -image to pin a digest (name@sha256:...) or a newer tag.
const defaultFuzzImage = "schemathesis/schemathesis:4.29.0"

const (
	fuzzAuthEnv   = "VUKEM_FUZZ_AUTH"
	fuzzTokenEnv  = "VUKEM_FUZZ_TOKEN" //nolint:gosec // env var name, not a credential
	fuzzHostAlias = "host.docker.internal"
)

//go:embed fuzz_hook.py
var fuzzHookPy []byte

var fuzzHeaderName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// FuzzAuth is the credential api:fuzz sends on every request.
type FuzzAuth struct {
	// Header is the HTTP header name, e.g. "Authorization" or "X-Api-Key".
	Header string
	// Value is the full header value, e.g. "Bearer hx_live_...".
	Value string
}

// WithFuzzAuth sets how api:fuzz obtains a test credential. The function runs
// inside the command with the booted app (database connected), so it can mint
// an API key through the app's own code instead of raw SQL. Without it,
// api:fuzz reads a bearer token from VUKEM_FUZZ_TOKEN, or scans anonymously.
func WithFuzzAuth(fn func(ctx context.Context, app *App) (FuzzAuth, error)) Option {
	return func(o *options) error { o.fuzzAuth = fn; return nil }
}

// fuzzRunner runs the scan container; tests inject a fake.
type fuzzRunner interface {
	Run(ctx context.Context, argv, env []string, stdout, stderr io.Writer) error
}

type execFuzzRunner struct{}

func (execFuzzRunner) Run(ctx context.Context, argv, env []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // operator flags
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return cmd.Run()
}

type fuzzSettings struct {
	MaxExamples int
	Workers     int
	ReadOnly    bool
	AuthHeader  string
	Bypass      bool
}

type fuzzRun struct {
	URL, Image, ReportDir, Docker string
	DryRun                        bool
}

func fuzzAPICommand() Command { return fuzzCommand(execFuzzRunner{}) }

func fuzzCommand(runner fuzzRunner) Command {
	var (
		prefix, target, image, reportDir, dockerBin string
		maxExamples, workers                        int
		bypass, dryRun                              bool
	)

	return CommandFunc{
		CommandName: "api:fuzz",
		Desc:        "Fuzz the running API with Schemathesis in Docker (-url, -dry-run)",
		DefineFlags: func(fs *flag.FlagSet) {
			openAPIPrefixFlag(fs, &prefix)
			fs.StringVar(&target, "url", "", "base URL of the running app (default http://127.0.0.1:$PORT)")
			fs.StringVar(&image, "image", defaultFuzzImage, "Schemathesis image (pin a digest in CI)")
			fs.StringVar(
				&reportDir,
				"report-dir",
				"reports/schemathesis",
				"directory for the JUnit and cassette reports",
			)
			fs.StringVar(&dockerBin, "docker", "docker", "docker executable")
			fs.IntVar(&maxExamples, "max-examples", 50, "generated cases per operation")
			fs.IntVar(&workers, "workers", 1, "parallel Schemathesis workers")
			fs.BoolVar(&bypass, "rate-limit-bypass", true,
				"send a distinct X-Forwarded-For per call (the app must trust the Docker bridge in TRUSTED_PROXIES)")
			fs.BoolVar(
				&dryRun,
				"dry-run",
				false,
				"print the docker command and the generated config without running it",
			)
		},
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("api:fuzz: unexpected argument %q: %w", args[0], errUsage)
			}

			doc, err := openAPIDocument(app, prefix)
			if err != nil {
				return err
			}

			if len(doc.Paths) == 0 {
				return fmt.Errorf(
					"api:fuzz: the OpenAPI document has no operations (set -prefix or OPENAPI_PREFIX): %w",
					errUsage,
				)
			}

			if target == "" {
				target = "http://127.0.0.1:" + strconv.Itoa(app.Config.Port)
			}

			scanURL, err := fuzzDockerURL(target)
			if err != nil {
				return err
			}

			auth, err := app.fuzzCredential(ctx)
			if err != nil {
				return err
			}

			set := fuzzSettings{
				MaxExamples: maxExamples, Workers: workers, ReadOnly: fuzzReadOnly(doc),
				AuthHeader: auth.Header, Bypass: bypass,
			}

			return execFuzz(ctx, app, runner, doc, set, auth, fuzzRun{
				URL: scanURL, Image: image, ReportDir: reportDir, Docker: dockerBin, DryRun: dryRun,
			})
		},
	}
}

func (a *App) fuzzCredential(ctx context.Context) (FuzzAuth, error) {
	if a.fuzzAuth != nil {
		auth, err := a.fuzzAuth(ctx, a)
		if err != nil {
			return FuzzAuth{}, fmt.Errorf("api:fuzz: credential hook: %w", err)
		}

		if auth.Header != "" && !fuzzHeaderName.MatchString(auth.Header) {
			return FuzzAuth{}, fmt.Errorf("api:fuzz: invalid header name %q: %w", auth.Header, errUsage)
		}

		return auth, nil
	}

	if tok, ok := lookupEnv(fuzzTokenEnv); ok && tok != "" {
		return FuzzAuth{Header: "Authorization", Value: "Bearer " + tok}, nil
	}

	return FuzzAuth{}, nil
}

func execFuzz(
	ctx context.Context, app *App, runner fuzzRunner, doc *openapi.Document,
	set fuzzSettings, auth FuzzAuth, r fuzzRun,
) error {
	work, err := os.MkdirTemp("", "vukem-fuzz-")
	if err != nil {
		return err
	}

	if !r.DryRun {
		defer os.RemoveAll(work)
	}

	spec, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	toml := fuzzTOML(set)

	files := map[string][]byte{"openapi.json": spec, "schemathesis.toml": []byte(toml)}
	if set.Bypass {
		files["hook.py"] = fuzzHookPy
	}

	for name, data := range files {
		if err := os.WriteFile(filepath.Join(work, name), data, 0o600); err != nil {
			return err
		}
	}

	report, err := filepath.Abs(r.ReportDir)
	if err != nil {
		return err
	}

	// The image runs as a non-root user; a 0755 directory owned by the caller
	// makes its crash-cache mkdir fail after a real finding.
	if err := os.MkdirAll(report, 0o755); err != nil { //nolint:gosec // see above
		return err
	}

	_ = os.Chmod(report, 0o777) //nolint:gosec // see above

	argv := fuzzArgv(r, set, filepath.ToSlash(work), filepath.ToSlash(report), auth.Header != "")

	if r.DryRun {
		_, err := fmt.Fprintf(app.Stdout(), "# %s (kept for inspection)\n%s\n# schemathesis.toml\n%s",
			work, strings.Join(argv, " "), toml)

		return err
	}

	var env []string
	if auth.Header != "" {
		env = []string{fuzzAuthEnv + "=" + auth.Value}
	}

	if err := runner.Run(ctx, argv, env, app.Stdout(), app.stderr); err != nil {
		return fmt.Errorf("api:fuzz: schemathesis (reports in %s): %w", report, err)
	}

	return nil
}

func fuzzArgv(r fuzzRun, set fuzzSettings, work, report string, hasAuth bool) []string {
	argv := []string{r.Docker, "run", "--rm", "-e", "HOME=/tmp", "--add-host=" + fuzzHostAlias + ":host-gateway"}
	if hasAuth {
		argv = append(argv, "-e", fuzzAuthEnv)
	}

	if set.Bypass {
		argv = append(argv, "-e", "SCHEMATHESIS_HOOKS=/work/hook.py")
	}

	return append(argv,
		"-v", work+":/work:ro", "-v", report+":/reports", "-w", "/reports",
		r.Image,
		"--config-file", "/work/schemathesis.toml",
		"run", "/work/openapi.json", "--url", r.URL,
		"--report", "junit,vcr", "--report-dir", "/reports",
	)
}

// fuzzTOML derives the Schemathesis config from the API shape. Auth is an
// environment reference, never the secret.
func fuzzTOML(s fuzzSettings) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# generated by vukem api:fuzz\nworkers = %d\nrequest-timeout = 15\nmax-failures = 20\n", s.Workers)

	if s.AuthHeader != "" {
		fmt.Fprintf(&b, "headers = { %q = \"${%s}\" }\n", s.AuthHeader, fuzzAuthEnv)
	}

	fmt.Fprintf(&b, "\n[generation]\nmax-examples = %d\n\n[checks]\n", s.MaxExamples)
	b.WriteString("# An undeclared method can answer 400/404 in front of the router instead of 405.\n")
	b.WriteString("unsupported_method.enabled = false\nallow_header_conformance.enabled = false\n")

	if s.ReadOnly {
		b.WriteString("# Both checks need a create/delete link between operations.\n")
		b.WriteString("use_after_free.enabled = false\nensure_resource_availability.enabled = false\n")
	}

	return b.String()
}

func fuzzReadOnly(doc *openapi.Document) bool {
	for _, ops := range doc.Paths {
		for method := range ops {
			switch strings.ToLower(method) {
			case "get", "head", "options":
			default:
				return false
			}
		}
	}

	return true
}

// fuzzDockerURL rewrites a loopback origin to the host alias the container
// reaches (Docker Desktop has no host network).
func fuzzDockerURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("api:fuzz: -url %q must be an http(s) URL: %w", raw, errUsage)
	}

	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0":
		host := fuzzHostAlias
		if p := u.Port(); p != "" {
			host += ":" + p
		}

		u.Host = host
	}

	return strings.TrimRight(u.String(), "/"), nil
}
