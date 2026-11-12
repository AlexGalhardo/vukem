package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestChooseExample(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interactive bool
		input       string
		example     string
		noExample   bool
		minimal     bool
		want        string
		wantErr     string
		wantPrompt  bool
	}{
		{name: "no tty takes the default", want: "todo"},
		{name: "example todo", example: "todo", want: "todo"},
		{name: "example finance", example: "Finance", want: "finance"},
		{name: "example none", example: "none", want: "none"},
		{name: "no-example", noExample: true, want: "none"},
		{name: "bad value", example: "blog", wantErr: "todo, finance or none"},
		{name: "both flags", example: "todo", noExample: true, wantErr: "choose one"},
		{name: "flags skip the prompt", interactive: true, noExample: true, want: "none"},
		{name: "prompt default", interactive: true, input: "\n", want: "todo", wantPrompt: true},
		{name: "prompt finance", interactive: true, input: "2\n", want: "finance", wantPrompt: true},
		{name: "prompt none by name", interactive: true, input: "none\n", want: "none", wantPrompt: true},
		{name: "prompt at EOF", interactive: true, want: "todo", wantPrompt: true},
		{name: "minimal has none", interactive: true, minimal: true, want: "none"},
		{name: "minimal refuses todo", example: "todo", minimal: true, wantErr: "no database"},
		{name: "minimal accepts none", example: "none", minimal: true, want: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			env := &Env{Stdin: strings.NewReader(tt.input), Stdout: &out, Interactive: tt.interactive}

			got, err := chooseExample(env, tt.example, tt.noExample, tt.minimal)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("chooseExample() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if err != nil || got != tt.want {
				t.Fatalf("chooseExample() = %q, %v, want %q", got, err, tt.want)
			}

			if prompted := strings.Contains(out.String(), "Example app?"); prompted != tt.wantPrompt {
				t.Errorf("prompted = %v, want %v (output %q)", prompted, tt.wantPrompt, out.String())
			}
		})
	}
}

func TestFinanceStack(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		o       newOpts
		wantErr bool
	}{
		"no stack flag": {},
		"inertia react": {o: newOpts{inertiaFW: "React"}},
		"inertia vue":   {o: newOpts{inertiaFW: "vue"}, wantErr: true},
		"api":           {o: newOpts{apiOnly: true}, wantErr: true},
		"templ":         {o: newOpts{templ: true}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st, err := financeStack(tt.o)
			if (err != nil) != tt.wantErr || (err == nil && st != stack{kind: "inertia", framework: "react"}) {
				t.Errorf("financeStack(%+v) = %+v, %v", tt.o, st, err)
			}
		})
	}
}

// exampleProject writes the starter of a stack and its example into a new
// folder, without the network: the file-only half of vukem new.
func exampleProject(t *testing.T, st stack, module string) string {
	t.Helper()

	dir := t.TempDir()

	if err := writeStarter(dir, starterData{Name: "todo", Module: module}); err != nil {
		t.Fatal(err)
	}

	env := &Env{Dir: dir, Stdout: io.Discard, Stderr: io.Discard, Now: fixedNow}
	data := starterData{Name: "todo", Module: module}

	if err := applyStack(env, st, data); err != nil {
		t.Fatal(err)
	}

	if err := applyExample(env, st); err != nil {
		t.Fatalf("applyExample(%+v): %v", st, err)
	}

	return dir
}

func TestApplyExample(t *testing.T) {
	t.Parallel()

	web := []string{
		"app/models/task.go", "app/requests/task.go", "app/controllers/task_controller.go", "tests/task_test.go",
		"database/migrations/20261018003311_create_tasks_table.go",
	}

	tests := []struct {
		st    stack
		files []string
		// routed is the file that wires the tasks routes.
		routed, route string
	}{
		{
			stack{kind: "htmx"},
			append([]string{"resources/views/tasks/index.html", "resources/views/tasks/edit.html"}, web...),
			"routes/web.go",
			`"tasks"`,
		},
		{stack{kind: "templ"}, append([]string{"resources/views/tasks.templ"}, web...), "routes/web.go", `"tasks"`},
		{
			stack{kind: "inertia", framework: "react"},
			append(
				[]string{
					"app/pages/tasks.go",
					"resources/js/pages/Tasks/Index.tsx",
					"resources/js/pages/Tasks/Edit.tsx",
				},
				web...),
			"routes/web.go",
			`"tasks"`,
		},
		{
			stack{kind: "inertia", framework: "vue"},
			append([]string{"resources/js/pages/Tasks/Index.vue", "resources/js/pages/Tasks/Edit.vue"}, web...),
			"app/pages/pages.go",
			"TasksEdit.Info()",
		},
		{
			stack{kind: "inertia", framework: "svelte"},
			append([]string{"resources/js/pages/Tasks/Index.svelte", "resources/js/pages/Tasks/Edit.svelte"}, web...),
			"app/pages/pages.go",
			"TasksIndex.Info()",
		},
		{stack{kind: "api"}, []string{
			"app/models/task.go", "app/requests/task.go", "app/controllers/apiv1/tasks.go", "app/resources/task.go",
			"routes/tasks.go", "tests/tasks_test.go", "database/migrations/20261018003311_create_tasks_table.go",
		}, "routes/registry.go", "Tasks,"},
	}

	for _, tt := range tests {
		t.Run(tt.st.kind+tt.st.framework, func(t *testing.T) {
			t.Parallel()

			dir := exampleProject(t, tt.st, "example.com/todo")

			for _, f := range tt.files {
				requireFile(t, dir, f)
			}

			if got := read(t, dir, tt.routed); !strings.Contains(got, tt.route) {
				t.Errorf("%s lacks %s:\n%s", tt.routed, tt.route, got)
			}

			if reg := read(t, dir, "database/migrations/registry.go"); !strings.Contains(reg, exampleMigration) {
				t.Errorf("migrations registry lacks the tasks migration:\n%s", reg)
			}
		})
	}
}

// fence matches a titled code block of an MDX page.
var fence = regexp.MustCompile("(?s)```\\w+ title=\"([^\"]+)\"\n(.*?)\n```")

// dedent drops the leading tabs of every line, so a fragment shown without
// its surrounding indentation still matches.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimLeft(l, "\t")
	}

	return strings.Join(lines, "\n")
}

// TestFirstAppTutorialMatchesExample keeps the first-app tutorial and the
// HTMX to-do example in step: every file block of the tutorial must be the
// file vukem new --example writes, or a fragment of it (routes, navigation).
func TestFirstAppTutorialMatchesExample(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "site", "content", "docs", "getting-started", "first-app.mdx"))
	if err != nil {
		t.Fatal(err)
	}

	// The tutorial names its project example.com/todo.
	dir := exampleProject(t, stack{kind: "htmx"}, "example.com/todo")

	blocks := fence.FindAllStringSubmatch(string(raw), -1)
	if len(blocks) < 9 {
		t.Fatalf("found %d titled blocks in first-app.mdx, want at least 9 (did the fence format change?)", len(blocks))
	}

	for _, b := range blocks {
		rel, code := b[1], b[2]

		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("first-app.mdx shows %s, which the example does not write: %v", rel, err)

			continue
		}

		if !strings.Contains(string(got), code) && !strings.Contains(dedent(string(got)), dedent(code)) {
			t.Errorf("first-app.mdx block for %s differs from the example; the example has:\n%s", rel, got)
		}
	}
}

// TestNewExampleNoneEndToEnd checks that --example none leaves the starter
// as it was, and that the project still builds and passes its tests.
func TestNewExampleNoneEndToEnd(t *testing.T) {
	t.Parallel()
	requireGo(t)

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	code, out, errOut := vukem(t, dir, "new", "app", "--example", "none", "--module", "example.com/app",
		"--framework", repo, "--no-install")
	if code != 0 {
		t.Fatalf("vukem new --example none = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "app")
	requireNoFile(t, project, "app/controllers/task_controller.go")
	requireNoFile(t, project, "database/migrations/20261018003311_create_tasks_table.go")

	if nav := read(t, project, "resources/views/layouts/app.html"); strings.Contains(nav, "tasks.index") {
		t.Errorf("layout links to tasks:\n%s", nav)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if out, err := combinedOutput(cmd); err != nil {
		t.Fatalf("starter tests: %v\n%s", err, out)
	}
}
