package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// minimalStarterRemove lists the HTMX starter files a minimal project drops:
// everything that needs a database, and the examples built on it.
var minimalStarterRemove = []string{
	"database", "app/models", "app/requests",
	"app/controllers/post_controller.go", "app/controllers/upload_controller.go",
	"resources/views/posts", "resources/views/partials", "resources/views/uploads", "resources/views/components",
	"tests/posts_test.go", "tests/uploads_test.go",
}

// minimalStarterEdits rewrites the starter files that reference the
// removed code; each old text must be found, so starter changes that break
// --minimal fail TestNewMinimalProjectEndToEnd.
var minimalStarterEdits = []struct{ file, old, new string }{
	{"bootstrap/app.go", "\t\"{{MODULE}}/database/migrations\"\n\t\"{{MODULE}}/database/seeders\"\n", ""},
	{"bootstrap/app.go", "\t\tvukem.WithMigrations(migrations.All()),\n\t\tvukem.WithSeeders(seeders.All()...),\n", ""},
	{
		"routes/web.go",
		"\tdb, err := app.DB()\n\tif err != nil {\n\t\treturn err\n\t}\n\n",
		"",
	},
	{
		"routes/web.go",
		"\tr.Resource(\"posts\", controllers.NewPostController(db))\n\n" +
			"\tuploads, err := controllers.NewUploadController(app)\n\tif err != nil {\n\t\treturn err\n\t}\n\n" +
			"\tr.Get(\"/uploads\", uploads.Index).Name(\"uploads.index\")\n" +
			"\tr.Post(\"/uploads\", uploads.Store).Name(\"uploads.store\")\n",
		"",
	},
	{
		"resources/views/home.html",
		"\t<p><a class=\"button\" href=\"{{route \"posts.index\"}}\">See the example CRUD</a></p>\n",
		"",
	},
	{
		"resources/views/layouts/app.html",
		"\t\t<nav><a href=\"{{route \"posts.index\"}}\">Posts</a> <a href=\"{{route \"uploads.index\"}}\">Uploads</a></nav>\n",
		"",
	},
	{".env.example", "DATABASE_URL=file:storage/database.sqlite\n", ""},
	{"README.md", "vukem migrate      # apply migrations\n", ""},
}

// minimalBuildTags leaves the SQLite driver out of every vukem build.
const minimalBuildTags = `# Go build tags for vukem build and vukem serve (see docs/cli.md).
# vukem_nosqlite leaves the SQLite driver out of the binary; remove it
# before using a SQLite database.
vukem_nosqlite
`

// makeMinimalStarter turns the starter into a database-less HTMX app.
func makeMinimalStarter(env *Env) error {
	for _, rel := range minimalStarterRemove {
		if err := os.RemoveAll(env.path(filepath.FromSlash(rel))); err != nil {
			return err
		}
	}

	module, err := modulePath(env)
	if err != nil {
		return err
	}

	for _, e := range minimalStarterEdits {
		p := env.path(filepath.FromSlash(e.file))

		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		old := bytes.ReplaceAll([]byte(e.old), []byte("{{MODULE}}"), []byte(module))
		if !bytes.Contains(src, old) {
			return fmt.Errorf("minimal starter: %s no longer contains %q", e.file, old)
		}

		if err := writeFile(p, bytes.Replace(src, old, []byte(e.new), 1)); err != nil {
			return err
		}
	}

	return writeFile(env.path(buildTagsFile), []byte(minimalBuildTags))
}
