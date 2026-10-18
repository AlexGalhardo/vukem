package codemod

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestAppendToReturnGolden(t *testing.T) {
	t.Parallel()

	const local = "example.com/blog"

	tests := []struct {
		name, fn, expr string
		imports        []string
	}{
		{"providers", "Providers", "&providers.BillingProvider{}", []string{"example.com/blog/app/providers"}},
		{"empty_list", "Commands", "commands.SendReports()", []string{"example.com/blog/app/commands"}},
		{"no_trailing_comma", "All", "m20261016120000CreatePostsTable()", nil},
		{"single_import", "Providers", "providers.Mail{}", []string{"example.com/blog/app/providers"}},
		{"no_imports", "Providers", "providers.Mail{}", []string{"example.com/blog/app/providers"}},
		{"already_present", "Providers", "providers.Mail{ }", []string{"example.com/blog/app/providers"}},
		// An existing block already split into gci's groups keeps them:
		// each added import joins its own group instead of starting a new
		// one at the end of the block.
		{"grouped_block", "All", "authz.For[*models.Comment](CommentPolicy{})", []string{
			"github.com/AlexGalhardo/vukem/events", "example.com/blog/app/requests",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src, err := os.ReadFile(filepath.Join("testdata", tt.name+".input"))
			if err != nil {
				t.Fatal(err)
			}

			got, err := AppendToReturn(src, tt.fn, tt.expr, local, tt.imports...)
			if err != nil {
				t.Fatal(err)
			}

			golden := filepath.Join("testdata", tt.name+".golden")
			if *update {
				if err := os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(strings.ReplaceAll(string(want), "\r\n", "\n"), string(got)); diff != "" {
				t.Fatalf("AppendToReturn (-want +got):\n%s", diff)
			}

			again, err := AppendToReturn(got, tt.fn, tt.expr, local, tt.imports...)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("second run is not idempotent:\n%s", again)
			}
		})
	}
}

func TestAppendToReturnErrors(t *testing.T) {
	t.Parallel()

	src := []byte(
		"package p\n\nfunc Providers() []int { x := []int{}; return x }\n\nfunc (T) All() []int { return []int{} }\n",
	)

	for _, fn := range []string{"Providers", "All", "Missing"} {
		if _, err := AppendToReturn(src, fn, "1", ""); !errors.Is(err, ErrMarkerNotFound) {
			t.Errorf("AppendToReturn(%s) = %v, want ErrMarkerNotFound", fn, err)
		}
	}

	if _, err := AppendToReturn([]byte("package p\nfunc F() []int { return []int{"), "F", "1", ""); err == nil {
		t.Error("broken source accepted")
	}

	if _, err := AppendToReturn(src, "F", "1 +", ""); err == nil {
		t.Error("broken expression accepted")
	}

	if _, err := AppendToReturn([]byte("package p\nfunc F() []int { return []int{} }\n"), "F", "}{", ""); err == nil {
		t.Error("expression breaking the file accepted")
	}
}
