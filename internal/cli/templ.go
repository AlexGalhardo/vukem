package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// templTool is the templ generator the templ starter pins with a go.mod
// tool directive; the generated *_templ.go files must match its version.
const templTool = "github.com/a-h/templ/cmd/templ@v0.3.1070"

// templReplaced lists the HTMX starter's html/template views; the templ
// scaffold rewrites the controllers, routes and resources package.
// shortcut: the upload demo is html/template only, port it to templ when asked.
var templReplaced = []string{"resources/views", "app/controllers/upload_controller.go", "tests/uploads_test.go"}

const withViews = "\t\tvukem.WithViews(resources.Views()),\n"

// applyTempl turns the HTMX starter in env.Dir into a templ app: the same
// pages as templ components in resources/views. The caller adds the templ
// tool and generates the components.
func applyTempl(env *Env) error {
	app := env.path(filepath.Join("bootstrap", "app.go"))

	src, err := os.ReadFile(app)
	if err != nil {
		return err
	}

	if !bytes.Contains(src, []byte(withViews)) {
		return errors.New("bootstrap/app.go: no vukem.WithViews(resources.Views()) line to remove")
	}

	for _, rel := range templReplaced {
		if err := os.RemoveAll(env.path(filepath.FromSlash(rel))); err != nil {
			return err
		}
	}

	data, err := scaffoldData(env)
	if err != nil {
		return err
	}

	if err := installScaffold(env, "templ", data, true); err != nil {
		return err
	}

	return writeFile(app, bytes.Replace(src, []byte(withViews), nil, 1))
}

// addTempl pins the templ generator as a go tool of the project.
func addTempl(ctx context.Context, env *Env) error {
	return tool(ctx, env, "go", "get", "-tool", templTool)
}

// hasTempl reports whether the project has templ components.
func hasTempl(env *Env) bool {
	found := false

	_ = walkSources(env.dirOrDot(), func(p string, _ fs.FileInfo) {
		found = found || strings.HasSuffix(p, ".templ")
	})

	return found
}

// templGenerate compiles the project's .templ files into *_templ.go files
// with the templ version pinned in go.mod.
func templGenerate(ctx context.Context, env *Env) error {
	return tool(ctx, env, "go", "tool", "templ", "generate", "-log-level", "warn")
}
