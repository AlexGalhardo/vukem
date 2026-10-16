// Package resources holds the email views, embedded in the binary; during
// development the files on disk are used so edits show up immediately.
package resources

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed views
var embedded embed.FS

// Views returns the html/template views (emails).
func Views() fs.FS {
	if info, err := os.Stat("resources/views"); err == nil && info.IsDir() {
		return os.DirFS("resources/views")
	}

	sub, err := fs.Sub(embedded, "views")
	if err != nil {
		panic(err) // unreachable: the directory is embedded above
	}

	return sub
}
