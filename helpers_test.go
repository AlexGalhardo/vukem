package vukem

import (
	"io/fs"
	"log/slog"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// emptyFS has no files, so only the process environment is used.
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
