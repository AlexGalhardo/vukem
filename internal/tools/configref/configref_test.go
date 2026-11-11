package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCollect(t *testing.T) {
	t.Parallel()

	got, err := collect(filepath.Join("testdata", "pkg"))
	if err != nil {
		t.Fatal(err)
	}

	want := []variable{
		{Name: "APP_DEBUG", Type: "bool", Field: "Config.Debug"},
		{Name: "APP_KEY", Default: "hunter2", Type: "config.Secret", Doc: "Key signs cookies.", Field: "Config.Key"},
		{Name: "APP_NAME", Default: "Demo", Type: "string", Doc: "Name is the application name.", Field: "Config.Name"},
		{Name: "APP_NOTE", Type: "string", Doc: "Note keeps a | in its text.", Field: "Config.Note"},
		{Name: "EXTRA_VAR", Type: "string", Field: "config.go:extra"},
		{Name: "PORT", Default: "3000", Type: "int", Doc: "Port is the port.", Field: "Nested.Port"},
		{Name: "WAIT", Default: "5s", Type: "time.Duration", Field: "Config.Wait"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("collect() mismatch (-want +got):\n%s", diff)
	}
}

func TestRender(t *testing.T) {
	t.Parallel()

	got := render([]variable{
		{Name: "APP_DEBUG", Type: "bool", Field: "Config.Debug"},
		{Name: "APP_KEY", Default: "hunter2", Type: "config.Secret", Doc: "Key signs cookies.", Field: "Config.Key"},
		{Name: "APP_NOTE", Type: "string", Doc: "Note keeps a | in its text.", Field: "Config.Note"},
		{Name: "WAIT", Default: "5s", Type: "time.Duration", Field: "Config.Wait"},
	})

	want := strings.Join([]string{
		"| Variable | Type | Default | Description | Source |",
		"| --- | --- | --- | --- | --- |",
		"| `APP_DEBUG` | `bool` | — | — | `Config.Debug` |",
		"| `APP_KEY` | `config.Secret` | — | Key signs cookies. | `Config.Key` |",
		"| `APP_NOTE` | `string` | — | Note keeps a \\| in its text. | `Config.Note` |",
		"| `WAIT` | `time.Duration` | `5s` | — | `Config.Wait` |",
		"",
	}, "\n")

	if got != want {
		t.Errorf("render() =\n%s\nwant\n%s", got, want)
	}

	if strings.Contains(got, "hunter2") {
		t.Error("render() printed the default of a config.Secret")
	}
}

func TestSplice(t *testing.T) {
	t.Parallel()

	const (
		begin = "<!-- configref:begin -->"
		end   = "<!-- configref:end -->"
	)

	tests := []struct {
		name    string
		doc     string
		want    string
		wantErr bool
	}{
		{
			name: "replaces the block and keeps the rest",
			doc:  "before\n" + begin + "\nold\n" + end + "\nafter\n",
			want: "before\n" + begin + "\nTABLE\n" + end + "\nafter\n",
		},
		{name: "missing begin", doc: "x\n" + end + "\n", wantErr: true},
		{name: "missing end", doc: begin + "\nx\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := splice(tt.doc, "TABLE\n")
			if (err != nil) != tt.wantErr {
				t.Fatalf("splice() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("splice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckDetectsDrift(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "configuration.md")
	synced, err := splice("a\n<!-- configref:begin -->\n<!-- configref:end -->\nb\n", "TABLE\n")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(synced), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := check(path, "TABLE\n"); err != nil {
		t.Fatalf("check() on a synced file = %v", err)
	}

	err = check(path, "OTHER TABLE\n")
	if err == nil || !strings.Contains(err.Error(), "is out of date") {
		t.Fatalf("check() on a drifted file = %v, want an out of date error", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != synced {
		t.Error("check() modified the file")
	}
}
