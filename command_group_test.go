package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type noBootFunc struct{ CommandFunc }

func (noBootFunc) NoBoot() bool { return true }

func groupTestApp(got *[]string) (app *App, stdout, stderr *bytes.Buffer) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	app = &App{stdout: stdout, stderr: stderr}

	leaf := func(name string, aliases ...string) Command {
		return noBootFunc{CommandFunc{
			CommandName:    name,
			Desc:           "run " + name,
			CommandAliases: aliases,
			DefineFlags:    func(fs *flag.FlagSet) { fs.Bool("dry", false, "dry run") },
			Fn: func(_ context.Context, _ *App, args []string) error {
				*got = append([]string{name}, args...)
				return nil
			},
		}}
	}

	app.AddCommand(
		CommandGroup{
			GroupName:    "image",
			Desc:         "image tools",
			GroupAliases: []string{"img"},
			Commands: []Command{
				leaf("edit", "image:edit"),
				CommandGroup{GroupName: "batch", Desc: "batches", Commands: []Command{leaf("run", "image:batch")}},
			},
		},
		leaf("lint", "check"),
		leaf("taken"),
		leaf("other", "taken"),
	)

	return app, stdout, stderr
}

func TestCommandGroupsAndAliasesDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{"a child", []string{"image", "edit", "a.png"}, []string{"edit", "a.png"}},
		{"a child with a flag", []string{"image", "edit", "-dry", "a.png"}, []string{"edit", "a.png"}},
		{"a group alias", []string{"img", "edit"}, []string{"edit"}},
		{"a nested child", []string{"image", "batch", "run", "x"}, []string{"run", "x"}},
		{"a child alias at the top level", []string{"image:edit", "b.png"}, []string{"edit", "b.png"}},
		{"a nested child alias at the top level", []string{"image:batch"}, []string{"run"}},
		{"a command alias", []string{"check", "y"}, []string{"lint", "y"}},
		{"a real name beats an alias", []string{"taken"}, []string{"taken"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			app, _, _ := groupTestApp(&got)

			if err := runCommand(t.Context(), app, tt.argv[0], tt.argv[1:]); err != nil {
				t.Fatalf("runCommand(%v) = %v, want nil", tt.argv, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("run (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCommandGroupUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		argv       []string
		wantErr    error
		wantStdout string
		wantStderr string
	}{
		{name: "no child", argv: []string{"image"}, wantErr: ErrUsage, wantStderr: "Commands of image:"},
		{name: "help", argv: []string{"image", "-h"}, wantStdout: "Commands of image:"},
		{
			name:       "a close child",
			argv:       []string{"image", "edti"},
			wantErr:    ErrUsage,
			wantStderr: `did you mean "image edit"?`,
		},
		{
			name:       "an unknown child",
			argv:       []string{"image", "zzzzzzzz"},
			wantErr:    ErrUsage,
			wantStderr: `unknown command "image zzzzzzzz"`,
		},
		{
			name:    "a child name is not a top-level command",
			argv:    []string{"edit"},
			wantErr: ErrUsage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			app, stdout, stderr := groupTestApp(&got)

			err := runCommand(t.Context(), app, tt.argv[0], tt.argv[1:])
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("runCommand(%v) = %v, want %v", tt.argv, err, tt.wantErr)
			}

			if got != nil {
				t.Errorf("ran %v, want nothing", got)
			}

			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout, tt.wantStdout)
			}

			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestCommandListShowsGroupsAndAliases(t *testing.T) {
	t.Parallel()

	var got []string

	app, stdout, _ := groupTestApp(&got)

	if err := runCommand(t.Context(), app, "list", nil); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"  image ", "(aliases: img)", "    edit ", "(aliases: image:edit)", "      run "} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("list = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestDispatchBeforeBuildResolvesAGroupChild(t *testing.T) {
	t.Parallel()

	var ran []string

	child := noBootFunc{CommandFunc{
		CommandName:    "edit",
		CommandAliases: []string{"image:edit"},
		Fn: func(_ context.Context, _ *App, args []string) error {
			ran = args
			return nil
		},
	}}
	opts := []Option{
		WithCommands(CommandGroup{GroupName: "image", Commands: []Command{child}}),
		WithStdout(&bytes.Buffer{}),
		WithStderr(&bytes.Buffer{}),
	}

	for _, argv := range [][]string{{"image", "edit", "a"}, {"image:edit", "a"}} {
		ran = nil

		handled, err := dispatchBeforeBuild(t.Context(), argv[0], argv[1:], opts)
		if !handled || err != nil {
			t.Fatalf("dispatchBeforeBuild(%v) = %v, %v; want true, nil", argv, handled, err)
		}

		if diff := cmp.Diff([]string{"a"}, ran); diff != "" {
			t.Errorf("args of %v (-want +got):\n%s", argv, diff)
		}
	}
}
