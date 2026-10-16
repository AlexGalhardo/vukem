package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		msg     string
		wantErr string // substring; empty means valid
	}{
		{name: "simple", msg: "feat: add router groups"},
		{name: "with scope", msg: "fix(router): reject conflicting patterns"},
		{name: "nested scope", msg: "build(drivers/postgres): bump pgx"},
		{name: "breaking bang", msg: "feat(orm)!: rename Find to Get"},
		{name: "breaking footer", msg: "feat(orm): rename Find\n\nBody text.\n\nBREAKING CHANGE: Find was removed"},
		{name: "trailing newline", msg: "docs: fix typo\n"},
		{
			name: "git comments ignored",
			msg:  "chore: tidy\n# Please enter the commit message\n# Lines starting with '#' are ignored",
		},
		{
			name: "scissors ignored",
			msg:  "test: cover ctx\n# ------------------------ >8 ------------------------\ndiff --git a/x b/x",
		},
		{name: "merge", msg: "Merge branch 'main' into feat/x"},
		{name: "git revert", msg: "Revert \"feat: add x\""},
		{name: "fixup", msg: "fixup! feat: add x"},
		{name: "squash", msg: "squash! feat: add x"},
		{name: "every type", msg: "perf: speed up matching"},

		{name: "empty", msg: "", wantErr: "empty"},
		{name: "only comments", msg: "# nothing\n", wantErr: "empty"},
		{name: "no type", msg: "add router groups", wantErr: "format"},
		{name: "unknown type", msg: "feature: add x", wantErr: `unknown type "feature"`},
		{name: "uppercase type", msg: "Feat: add x", wantErr: "unknown type"},
		{name: "missing space", msg: "feat:add x", wantErr: "format"},
		{name: "empty scope", msg: "feat(): add x", wantErr: "format"},
		{name: "bad scope", msg: "feat(Router): add x", wantErr: "scope"},
		{name: "uppercase description", msg: "feat: Add x", wantErr: "lowercase"},
		{name: "trailing period", msg: "feat: add x.", wantErr: "period"},
		{name: "empty description", msg: "feat: ", wantErr: "format"},
		{name: "too long", msg: "feat: " + strings.Repeat("a", 100), wantErr: "100"},
		{name: "no blank line before body", msg: "feat: add x\nbody", wantErr: "blank line"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Validate(tt.msg)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate(%q) = %v, want nil", tt.msg, err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("Validate(%q) = nil, want error containing %q", tt.msg, tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("Validate(%q) = %v, want error containing %q", tt.msg, err, tt.wantErr)
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := writeFile(t, dir, "good", "feat: add x\n")
	bad := writeFile(t, dir, "bad", "bad message\n")

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "valid", args: []string{good}, want: 0},
		{name: "invalid", args: []string{bad}, want: 1},
		{name: "missing file", args: []string{dir + "/nope"}, want: 1},
		{name: "no args", args: nil, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder
			if got := run(tt.args, &out); got != tt.want {
				t.Fatalf("run(%v) = %d, want %d (output: %s)", tt.args, got, tt.want, out.String())
			}
		})
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}
