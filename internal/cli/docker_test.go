package cli

import (
	"regexp"
	"strings"
	"testing"
)

func TestWriteDocker(t *testing.T) {
	t.Parallel()

	common := []string{
		"FROM golang:1.27.2-alpine3.24 AS build",
		"COPY go.mod go.sum ./",
		// .buildtags is read at build time, so later edits reach the image.
		`.buildtags`,
		`go build -trimpath -ldflags="-s -w" -tags "$tags" -o /out/app ./cmd/app`,
		"USER app",
		"ENV APP_ENV=production HOST=0.0.0.0 PORT=3000",
		// Without it, APP_ENV=production stops every command at boot (A81).
		"MAIL_MAILER=log",
		`ENTRYPOINT ["/app/app"]`,
	}

	tests := []struct {
		name    string
		kind    string
		pm      string
		want    []string
		notWant []string
	}{
		{
			name: "htmx npm",
			kind: "htmx",
			pm:   "npm",
			want: []string{
				"FROM node:24.22.0-alpine3.24 AS assets",
				"COPY package.json package-lock.json* ./",
				"&& npm ci --no-audit --no-fund",
				"RUN npm run build",
				"COPY --from=assets /src/resources/public/build resources/public/build",
			},
			notWant: []string{"templ generate"},
		},
		{
			name: "inertia bun",
			kind: "inertia",
			pm:   "bun",
			want: []string{
				// bun.lock* also matches the older binary bun.lockb.
				"FROM oven/bun:1.4.3-alpine AS assets", "COPY package.json bun.lock* ./",
				"&& bun install --frozen-lockfile", "RUN bun run build",
			},
		},
		{
			name: "templ pnpm",
			kind: "templ",
			pm:   "pnpm",
			want: []string{
				"RUN corepack enable", "COPY package.json pnpm-lock.yaml* ./", "&& pnpm install --frozen-lockfile",
				"RUN go tool templ generate",
			},
		},
		{
			name: "htmx yarn",
			kind: "htmx",
			pm:   "yarn",
			want: []string{
				"COPY package.json yarn.lock* ./",
				"&& yarn install --frozen-lockfile",
				"RUN yarn run build",
			},
		},
		{
			name:    "minimal",
			kind:    "minimal",
			pm:      "npm",
			want:    []string{"AS assets", "RUN npm run build"},
			notWant: []string{"templ generate"},
		},
		{
			name:    "api has no assets",
			kind:    "api",
			pm:      "npm",
			notWant: []string{"AS assets", "--from=assets", "package.json", "templ"},
		},
	}

	// Floating tags would make rebuilds pick up unreviewed images.
	floating := regexp.MustCompile(`(?m)^FROM \S+:(latest|edge|nightly|lts|alpine)\b|^FROM [^:\s]+(\s|$)`)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := writeDocker(dir, tt.kind, pmByName[tt.pm]); err != nil {
				t.Fatal(err)
			}

			got := read(t, dir, "Dockerfile")
			for _, w := range append(common, tt.want...) {
				if !strings.Contains(got, w) {
					t.Errorf("Dockerfile lacks %q:\n%s", w, got)
				}
			}

			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("Dockerfile has %q:\n%s", w, got)
				}
			}

			if m := floating.FindString(got); m != "" {
				t.Errorf("Dockerfile uses a floating image tag: %q", m)
			}

			ignore := read(t, dir, ".dockerignore")
			for _, w := range []string{".env", "node_modules/", "storage/", ".git/"} {
				if !strings.Contains(ignore, w) {
					t.Errorf(".dockerignore lacks %q", w)
				}
			}
		})
	}
}

func TestSecurity_A02_DockerIgnoresSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := writeDocker(dir, "htmx", pmByName["npm"]); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(read(t, dir, ".dockerignore"), "\n")
	for _, want := range []string{".env", ".env.*"} {
		found := false
		for _, l := range lines {
			found = found || strings.TrimSpace(l) == want
		}

		if !found {
			t.Errorf(".dockerignore does not exclude %s; got %q", want, lines)
		}
	}

	if regexp.MustCompile(`(?m)^(ENV|ARG)\s.*APP_KEY`).MatchString(read(t, dir, "Dockerfile")) {
		t.Error("Dockerfile bakes APP_KEY into the image")
	}
}

// The assets stage installs the tracked lockfile, so the lockfile is a
// required build input. Verified against Docker 29.8.1: without one,
// "npm ci" fails with EUSAGE and "pnpm install --frozen-lockfile" with
// ERR_PNPM_NO_LOCKFILE, while bun 1.4.3 and yarn 1.22.22 install anyway and
// resolve fresh versions without a word. The silent half is the dangerous
// one, so the Dockerfile states the requirement itself and every package
// manager fails the same way, naming the command that fixes it.
func TestDockerAssetsStageRequiresTheLockfile(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"npm", "bun", "pnpm", "yarn"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pm := pmByName[name]

			dir := t.TempDir()
			if err := writeDocker(dir, "htmx", pm); err != nil {
				t.Fatal(err)
			}

			got := read(t, dir, "Dockerfile")
			for _, want := range []string{
				"if ! ls " + pm.lockfile + "* >/dev/null 2>&1; then",
				`echo "` + pm.lockfile + ` not found: run '` + pm.install() + `', commit it, then build again" >&2`,
				"exit 1;",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("Dockerfile lacks %q:\n%s", want, got)
				}
			}

			// The guard runs before the install, not after it.
			if i, j := strings.Index(got, "exit 1;"), strings.Index(got, "&& "+pm.name); i < 0 || j < 0 || i > j {
				t.Errorf("the lockfile guard does not precede the install:\n%s", got)
			}
		})
	}

	// An API project builds no assets, so it needs no lockfile and must not
	// grow a check for one.
	dir := t.TempDir()
	if err := writeDocker(dir, "api", pmByName["npm"]); err != nil {
		t.Fatal(err)
	}

	if got := read(t, dir, "Dockerfile"); strings.Contains(got, "not found: run") {
		t.Errorf("the API Dockerfile checks for a lockfile:\n%s", got)
	}
}
