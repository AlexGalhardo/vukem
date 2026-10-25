package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePlanStatus(t *testing.T) {
	tasks := "\n- [x] **B.5.1** Review.\n- [ ] **S.3.1** Tag.\n- [ ] **S.3.2** Announce.\n- [ ] Exit criterion.\n"
	tests := []struct{ name, plan, want string }{
		{
			name: "next task is the first unchecked task",
			plan: "| Next task | `B.5.1` (review) |\n| Updated | 2026-10-17 |\n" + tasks,
			want: "| Next task | `S.3.1` |\n| Updated | 2026-11-01 |\n" + tasks,
		},
		{
			name: "no unchecked task",
			plan: "| Next task | `S.3.2` |\n| Updated | 2026-10-17 |\n\n- [x] **S.3.2** Announce.\n",
			want: "| Next task | none |\n| Updated | 2026-11-01 |\n\n- [x] **S.3.2** Announce.\n",
		},
		{
			name: "no next task row",
			plan: "| Updated | 2026-10-17 |\n",
			want: "| Updated | 2026-11-01 |\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := updatePlanStatus(tt.plan, "2026-11-01")
			if err != nil {
				t.Fatal(err)
			}

			if got != tt.want {
				t.Errorf("updatePlanStatus() got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}

	if _, err := updatePlanStatus("no table", "2026-11-01"); err == nil {
		t.Error("expected an error without status rows")
	}
}

func TestBumpRequire(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{
			"block",
			"require (\n\tgithub.com/AlexGalhardo/vukem v0.25.0\n\tgithub.com/x/y v0.25.0\n)\n",
			"require (\n\tgithub.com/AlexGalhardo/vukem v0.31.0\n\tgithub.com/x/y v0.25.0\n)\n",
		},
		{
			"single",
			"require github.com/AlexGalhardo/vukem v0.20.0\nreplace github.com/AlexGalhardo/vukem => ../..\n",
			"require github.com/AlexGalhardo/vukem v0.31.0\nreplace github.com/AlexGalhardo/vukem => ../..\n",
		},
		{
			"placeholder kept",
			"require github.com/AlexGalhardo/vukem v0.0.0\n",
			"require github.com/AlexGalhardo/vukem v0.0.0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bumpRequire(tt.in, "v0.31.0"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBumpModules(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()

		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("drivers/pg/go.mod", "require github.com/AlexGalhardo/vukem v0.29.0\n")
	write("contrib/new/go.mod", "require github.com/AlexGalhardo/vukem v0.31.0\n")
	write("internal/bench/go.mod", "require github.com/AlexGalhardo/vukem v0.1.0\n")

	changed, err := bumpModules(root, "v0.31.0")
	if err != nil {
		t.Fatal(err)
	}

	if len(changed) != 1 || filepath.Base(changed[0]) != "pg" {
		t.Fatalf("changed = %v, want only drivers/pg", changed)
	}

	b, _ := os.ReadFile(filepath.Join(root, "drivers", "pg", "go.mod"))
	if !strings.Contains(string(b), "v0.31.0") {
		t.Errorf("drivers/pg not bumped: %s", b)
	}
}
