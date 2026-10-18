// Package resources holds views and public assets. They are embedded in
// the binary; during development the files on disk are used instead so
// template edits show up on refresh. With APP_ENV=production (in the
// process environment or .env) the embedded files are always used, even
// when a resources/ directory exists next to the binary.
package resources

import (
	"embed"
	"io/fs"
	"os"

	"github.com/AlexGalhardo/vukem/config"
)

// all: keeps build/.vite/manifest.json (a dot directory) in the binary.
//
//go:embed views all:public
var embedded embed.FS

// Views returns the html/template views.
func Views() fs.FS { return dirOrEmbedded("views") }

// Public returns the static assets served at /.
func Public() fs.FS { return dirOrEmbedded("public") }

func dirOrEmbedded(dir string) fs.FS {
	if production() {
		return embeddedDir(dir)
	}

	if info, err := os.Stat("resources/" + dir); err == nil && info.IsDir() {
		return os.DirFS("resources/" + dir)
	}

	return embeddedDir(dir)
}

func embeddedDir(dir string) fs.FS {
	sub, err := fs.Sub(embedded, dir)
	if err != nil {
		panic(err) // unreachable: the directory is embedded above
	}

	return sub
}

// production reports whether the app runs with APP_ENV=production, read
// from the same places the app reads it: the process environment, then
// .env. When .env cannot be parsed it answers true, so the embedded files
// are used (the app itself refuses to boot then). @file: references are
// not read here: only APP_ENV matters.
func production() bool {
	src, err := config.LoadSource(os.DirFS("."), os.LookupEnv, func(string) ([]byte, error) { return nil, nil })

	return err != nil || src.Env() == config.Production
}
