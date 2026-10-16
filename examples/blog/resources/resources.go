// Package resources holds views and public assets. They are embedded in
// the binary; during development the files on disk are used instead so
// template edits show up on refresh.
package resources

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed views public
var embedded embed.FS

// Views returns the html/template views.
func Views() fs.FS { return dirOrEmbedded("views") }

// Public returns the static assets served at /.
func Public() fs.FS { return dirOrEmbedded("public") }

func dirOrEmbedded(dir string) fs.FS {
	if info, err := os.Stat("resources/" + dir); err == nil && info.IsDir() {
		return os.DirFS("resources/" + dir)
	}

	sub, err := fs.Sub(embedded, dir)
	if err != nil {
		panic(err) // unreachable: the directory is embedded above
	}

	return sub
}
