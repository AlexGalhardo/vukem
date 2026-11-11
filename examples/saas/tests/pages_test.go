package tests

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/examples/saas/app/pages"
	"github.com/AlexGalhardo/vukem/examples/saas/bootstrap"
	"github.com/AlexGalhardo/vukem/vukemtest"
)

// Every page component in resources/js/pages has typed props in Go (the Error
// page is rendered by the adapter itself), and the app registers exactly the
// pages of pages.All, so page-props.ts cannot miss one.
func TestEveryPageIsTyped(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, bootstrap.New)

	var registered []string
	for _, p := range app.Inertia.Pages() {
		registered = append(registered, p.Component)
	}

	var declared []string
	for _, p := range pages.All() {
		declared = append(declared, p.Component)
	}

	slices.Sort(registered)
	slices.Sort(declared)

	if !slices.Equal(registered, declared) {
		t.Errorf("Config.Pages = %v, pages.All() = %v", registered, declared)
	}

	root := filepath.Join("..", "resources", "js", "pages")

	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".tsx" {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if name := strings.TrimSuffix(filepath.ToSlash(rel), ".tsx"); name != "Error" {
			files = append(files, name)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(files)

	if !slices.Equal(files, declared) {
		t.Errorf("pages in resources/js/pages = %v, typed pages = %v", files, declared)
	}

	if _, err := os.Stat(filepath.Join("..", "resources", "js", "page-props.ts")); err != nil {
		t.Errorf("page-props.ts: %v (run vukem codegen:pages)", err)
	}
}
