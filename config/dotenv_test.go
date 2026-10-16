package config

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    []Entry
		wantErr string
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "simple",
			in:   "A=1\nB=two",
			want: []Entry{{Key: "A", Value: "1", Expand: true}, {Key: "B", Value: "two", Expand: true}},
		},
		{
			name: "comments and blanks",
			in:   "# c\n\n  # indented\nA=1\n",
			want: []Entry{{Key: "A", Value: "1", Expand: true}},
		},
		{name: "export prefix", in: "export A=1", want: []Entry{{Key: "A", Value: "1", Expand: true}}},
		{
			name: "spaces around",
			in:   "  A = hello world  ",
			want: []Entry{{Key: "A", Value: "hello world", Expand: true}},
		},
		{name: "inline comment", in: "A=1 # note", want: []Entry{{Key: "A", Value: "1", Expand: true}}},
		{name: "hash without space", in: "A=a#b", want: []Entry{{Key: "A", Value: "a#b", Expand: true}}},
		{name: "empty value", in: "A=", want: []Entry{{Key: "A", Value: "", Expand: true}}},
		{name: "single quotes literal", in: `A='x $B \n # y'`, want: []Entry{{Key: "A", Value: `x $B \n # y`}}},
		{
			name: "double quotes escapes",
			in:   `A="a\nb\t\"c\" \\ \$D"`,
			want: []Entry{{Key: "A", Value: "a\nb\t\"c\" \\ \\$D", Expand: true}},
		}, // \$ stays escaped until expansion
		{name: "double quotes comment after", in: `A="x" # c`, want: []Entry{{Key: "A", Value: "x", Expand: true}}},
		{
			name: "multiline double quotes",
			in:   "A=\"line1\nline2\"\nB=3",
			want: []Entry{{Key: "A", Value: "line1\nline2", Expand: true}, {Key: "B", Value: "3", Expand: true}},
		},
		{name: "dotted key", in: "a.b_C=1", want: []Entry{{Key: "a.b_C", Value: "1", Expand: true}}},
		{
			name: "crlf",
			in:   "A=1\r\nB=2\r\n",
			want: []Entry{{Key: "A", Value: "1", Expand: true}, {Key: "B", Value: "2", Expand: true}},
		},

		{name: "missing equals", in: "A", wantErr: "line 1: expected KEY=VALUE"},
		{name: "invalid key", in: "1A=2", wantErr: `line 1: invalid key "1A"`},
		{name: "unterminated double", in: `A="x`, wantErr: "line 1: unterminated double-quoted value"},
		{name: "unterminated single", in: `A='x`, wantErr: "line 1: unterminated single-quoted value"},
		{name: "garbage after quote", in: `A="x" y`, wantErr: "line 1: unexpected characters after quoted value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseEnv(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseEnv() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEnv() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseEnv() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func FuzzParseEnv(f *testing.F) {
	for _, s := range []string{"A=1", `A="x\ny"`, "A='z'", "export B=${A}", "# c\nC=1 # d"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		entries, err := ParseEnv(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, e := range entries {
			if !validKey(e.Key) {
				t.Fatalf("ParseEnv(%q) produced invalid key %q", in, e.Key)
			}
		}
	})
}
