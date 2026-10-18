package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseWorkUse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		work string
		want map[string]bool
	}{
		"block": {
			work: "go 1.27.2\n\nuse (\n\t.\n\t./drivers/postgres\n\t./contrib/grpc\n)\n",
			want: map[string]bool{".": true, "drivers/postgres": true, "contrib/grpc": true},
		},
		"single": {
			work: "go 1.27.2\n\nuse ./drivers/redis\n",
			want: map[string]bool{"drivers/redis": true},
		},
		"quoted and commented": {
			work: "use (\n\t\"./drivers/s3\" // object storage\n\t// ./drivers/gone\n)\n",
			want: map[string]bool{"drivers/s3": true},
		},
		"no use": {work: "go 1.27.2\n", want: map[string]bool{}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, parseWorkUse([]byte(tt.work))); diff != "" {
				t.Errorf("parseWorkUse() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConstraintTags(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want []string
	}{
		// The tag the postgres defect hid behind.
		"integration": {src: "//go:build integration\n\npackage p\n", want: []string{"integration"}},
		"and":         {src: "//go:build integration && linux\n\npackage p\n", want: []string{"integration"}},
		"parens": {
			src:  "//go:build (integration || e2e) && cgo\n\npackage p\n",
			want: []string{"integration", "e2e"},
		},
		"negated":       {src: "//go:build !vukem_nosqlite\n\npackage p\n", want: nil},
		"environment":   {src: "//go:build unix && amd64\n\npackage p\n", want: nil},
		"go version":    {src: "//go:build go1.27\n\npackage p\n", want: nil},
		"no constraint": {src: "package p\n", want: nil},
		// A //go:build after the package clause is not a constraint.
		"after package": {src: "package p\n\n//go:build integration\n", want: nil},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, constraintTags([]byte(tt.src))); diff != "" {
				t.Errorf("constraintTags() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiscoverModulesAndTags(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	write := func(rel, body string) {
		t.Helper()

		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("go.work", "use (\n\t.\n\t./drivers/redis\n)\n")
	write("go.mod", "module example.com/core\n")
	write("drivers/redis/go.mod", "module example.com/core/drivers/redis\n")
	write("drivers/redis/redis.go", "package redis\n")
	write("drivers/redis/integration_test.go", "//go:build integration\n\npackage redis\n")
	write("contrib/grpc/go.mod", "module example.com/core/contrib/grpc\n")
	write("contrib/grpc/grpc.go", "package grpc\n")
	// A directory without a go.mod is part of another module, not its own.
	write("drivers/shared/helper.go", "package shared\n")
	// internal/* is never published, so it is not a nested module here.
	write("internal/bench/go.mod", "module example.com/core/internal/bench\n")

	mods, err := discoverModules(root)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"contrib/grpc", "drivers/redis"}, mods); diff != "" {
		t.Errorf("discoverModules() diff (-want +got):\n%s", diff)
	}

	tags, err := customTags(root, "drivers/redis")
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"integration"}, tags); diff != "" {
		t.Errorf("customTags(drivers/redis) diff (-want +got):\n%s", diff)
	}

	if tags, err = customTags(root, "contrib/grpc"); err != nil || len(tags) != 0 {
		t.Errorf("customTags(contrib/grpc) = %v, %v, want none", tags, err)
	}

	// contrib/grpc is not in this go.work, which the gate reports.
	used, err := workspaceModules(root)
	if err != nil {
		t.Fatal(err)
	}

	if !used["drivers/redis"] || used["contrib/grpc"] {
		t.Errorf("workspaceModules() = %v", used)
	}
}
