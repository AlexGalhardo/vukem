package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// exampleMigration is the to-do example's migration; its timestamp is fixed
// so the files match the first-app tutorial (TestFirstAppTutorialMatchesExample).
const exampleMigration = "m20261018003311CreateTasksTable()"

// Example kinds of vukem new.
const (
	exampleTodo    = "todo"
	exampleFinance = "finance"
	exampleNone    = "none"
)

// chooseExample resolves which example app the new project includes: a flag
// settles it, a terminal is asked, and without a terminal the answer is the
// to-do example. The minimal stack has no database, so it never has one.
func chooseExample(env *Env, flagValue string, noExample, minimal bool) (string, error) {
	flagValue = strings.ToLower(strings.TrimSpace(flagValue))

	switch {
	case flagValue != "" && noExample:
		return "", usagef("choose one of --example or --no-example")
	case flagValue != "" && !slices.Contains([]string{exampleTodo, exampleFinance, exampleNone}, flagValue):
		return "", usagef("--example must be todo, finance or none, not %q", flagValue)
	case minimal && (flagValue == exampleTodo || flagValue == exampleFinance):
		return "", usagef("--minimal has no database: drop --example")
	case minimal || noExample:
		return exampleNone, nil
	case flagValue != "":
		return flagValue, nil
	case !env.Interactive || env.Stdin == nil:
		return exampleTodo, nil
	}

	return ask(env.Stdout, env.reader(), "Example app?", []option{
		{exampleTodo, "to-do: tasks CRUD with validation, migration and tests"},
		{exampleFinance, "finance: personal finance SaaS (Inertia React, auth, admin, workspaces, Stripe, Resend)"},
		{exampleNone, "none: empty starter"},
	}), nil
}

// financeStack is the stack of the finance example: Inertia React. A stack
// flag that says otherwise is an error.
func financeStack(o newOpts) (stack, error) {
	if o.apiOnly || o.templ || (o.inertiaFW != "" && !strings.EqualFold(strings.TrimSpace(o.inertiaFW), "react")) {
		return stack{}, usagef("--example finance is an Inertia React app: drop --api, --templ and --inertia %s",
			o.inertiaFW)
	}

	return stack{kind: "inertia", framework: "react"}, nil
}

// exampleScaffolds lists the scaffolds that make up the example of a stack.
func exampleScaffolds(st stack) []string {
	switch st.kind {
	case "api":
		return []string{"example", "example-api"}
	case "inertia":
		return []string{"example", "example-inertia", "example-inertia-" + st.framework}
	case "templ":
		return []string{"example", "example-web", "example-templ"}
	}

	return []string{"example", "example-web", "example-htmx"}
}

// inertiaNav is the navigation entry of each Inertia framework: the layout
// line it goes after, and the link.
var inertiaNav = map[string]struct{ file, after, link string }{
	"react": {
		"resources/js/layouts/AppLayout.tsx", "<nav className=\"flex items-center gap-4\">\n",
		"          <Link href=\"/tasks\" className={link}>\n            Tasks\n          </Link>\n",
	},
	"vue": {
		"resources/js/layouts/AppLayout.vue", "<nav class=\"flex items-center gap-4\">\n",
		"      <Link href=\"/tasks\" :class=\"link\">Tasks</Link>\n",
	},
	"svelte": {
		"resources/js/layouts/AppLayout.svelte", "<nav class=\"flex items-center gap-4\">\n",
		"    <Link href=\"/tasks\" class={link}>Tasks</Link>\n",
	},
}

// applyExample adds the to-do example of the stack in env.Dir to a project
// the stack is already applied to: model, migration, request, controller,
// views or pages, routes, navigation link and tests.
func applyExample(env *Env, st stack) error {
	data, err := scaffoldData(env)
	if err != nil {
		return err
	}

	for _, name := range exampleScaffolds(st) {
		if err := installScaffold(env, name, data, true); err != nil {
			return err
		}
	}

	for _, p := range exampleEdits(st) {
		if err := patchFile(env, p.file, p.old, p.new); err != nil {
			return err
		}
	}

	regs := []registration{{"database/migrations/registry.go", "All", exampleMigration}}
	if st.kind == "api" {
		regs = append(regs, registration{"routes/registry.go", "All", "Tasks"})
	}

	if st.kind == "inertia" {
		regs = append(regs, registration{"app/pages/pages.go", "All", "TasksIndex.Info()"},
			registration{"app/pages/pages.go", "All", "TasksEdit.Info()"})
	}

	for _, reg := range regs {
		if err := register(env, reg.file, reg.fn, reg.expr); err != nil {
			return err
		}
	}

	return nil
}

type exampleEdit struct{ file, old, new string }

const (
	tasksRoute = "\tr.Resource(\"tasks\", controllers.NewTaskController(db))\n"
	postsRoute = "\tr.Resource(\"posts\", controllers.NewPostController(db))\n"
)

// exampleEdits lists the edits to files the starter owns: the navigation
// and the route of the example.
func exampleEdits(st stack) []exampleEdit {
	if st.kind == "api" {
		return nil
	}

	routes := exampleEdit{"routes/web.go", postsRoute, postsRoute + tasksRoute}

	switch st.kind {
	case "inertia":
		n := inertiaNav[st.framework]

		return []exampleEdit{routes, {n.file, n.after, n.after + n.link}}
	case "templ":
		const posts = `<nav><a href={ route(ctx, "posts.index") }>Posts</a></nav>`

		return []exampleEdit{routes, {
			"resources/views/layout.templ", posts,
			`<nav>` + "\n\t\t\t\t\t" + `<a href={ route(ctx, "posts.index") }>Posts</a>` +
				"\n\t\t\t\t\t" + `<a href={ route(ctx, "tasks.index") }>Tasks</a>` + "\n\t\t\t\t" + `</nav>`,
		}}
	}

	return []exampleEdit{routes, {
		"resources/views/layouts/app.html",
		`<nav><a href="{{route "posts.index"}}">Posts</a> <a href="{{route "uploads.index"}}">Uploads</a></nav>`,
		"<nav>\n\t\t\t" + `<a href="{{route "posts.index"}}">Posts</a>` + "\n\t\t\t" +
			`<a href="{{route "tasks.index"}}">Tasks</a>` + "\n\t\t\t" +
			`<a href="{{route "uploads.index"}}">Uploads</a>` + "\n\t\t</nav>",
	}}
}

// patchFile replaces the first old with new in the file rel of the project,
// and fails when the starter no longer has old.
func patchFile(env *Env, rel, old, replacement string) error {
	p := env.path(filepath.FromSlash(rel))

	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	if !bytes.Contains(src, []byte(old)) {
		return fmt.Errorf("%s: no %q to add the example after", rel, old)
	}

	return writeFile(p, bytes.Replace(src, []byte(old), []byte(replacement), 1))
}
