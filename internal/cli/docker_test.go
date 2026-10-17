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
				"FROM node:24.22.0-alpine3.24 AS assets", "COPY package.json package-lock.json* ./", "RUN npm ci",
				"RUN npm run build", "COPY --from=assets /src/resources/public/build resources/public/build",
			},
			notWant: []string{"templ generate"},
		},
		{
			name: "inertia bun",
			kind: "inertia",
			pm:   "bun",
			want: []string{
				"FROM oven/bun:1.4.3-alpine AS assets", "COPY package.json bun.lock* ./",
				"RUN bun install --frozen-lockfile", "RUN bun run build",
			},
		},
		{
			name: "templ pnpm",
			kind: "templ",
			pm:   "pnpm",
			want: []string{
				"RUN corepack enable", "COPY package.json pnpm-lock.yaml* ./", "RUN pnpm install --frozen-lockfile",
				"RUN go tool templ generate",
			},
		},
		{
			name: "htmx yarn",
			kind: "htmx",
			pm:   "yarn",
			want: []string{
				"COPY package.json yarn.lock* ./",
				"RUN yarn install --frozen-lockfile",
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
