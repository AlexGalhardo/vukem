package main

import (
	"context"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"strings"
)

// exportImporter resolves imports from the compiler export data that
// "go list -export" builds (and caches); importing from source type-checks
// the whole dependency graph and takes minutes.
func exportImporter(fset *token.FileSet) (types.Importer, error) {
	cmd := exec.CommandContext(
		context.Background(),
		"go",
		"list",
		"-export",
		"-deps",
		"-f",
		"{{.ImportPath}} {{.Export}}",
		"./...",
	)
	cmd.Stderr = os.Stderr

	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -export: %w", err)
	}

	files := map[string]string{}

	for line := range strings.SplitSeq(string(raw), "\n") {
		if path, file, ok := strings.Cut(strings.TrimSpace(line), " "); ok && file != "" {
			files[path] = file
		}
	}

	return importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %q", path)
		}

		return os.Open(file)
	}), nil
}
