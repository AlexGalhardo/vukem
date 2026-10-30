package vukem

import (
	"context"
	"flag"
	"fmt"

	"github.com/AlexGalhardo/vukem/ssg"
)

func ssgGenerateCommand() Command {
	var (
		out   string
		paths []string
	)

	return CommandFunc{
		CommandName: "ssg:generate",
		Desc:        "Prerender handler paths to static HTML (not Inertia SSR)",
		DefineFlags: func(fs *flag.FlagSet) {
			paths = nil
			fs.StringVar(&out, "out", "dist", "directory to write static files into")
			fs.Func("path", "absolute URL path to prerender (repeatable)", func(s string) error {
				paths = append(paths, s)

				return nil
			})
		},
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("ssg:generate: unexpected argument %q: %w", args[0], errUsage)
			}

			if len(paths) == 0 {
				return fmt.Errorf("ssg:generate: at least one -path is required: %w", errUsage)
			}

			h, err := app.Handler()
			if err != nil {
				return err
			}

			pages := make([]ssg.Page, len(paths))
			for i, p := range paths {
				pages[i] = ssg.Page{Path: p}
			}

			report, err := ssg.Generate(ctx, h, pages, ssg.Options{OutDir: out})
			if err != nil {
				return fmt.Errorf("ssg:generate: %w", err)
			}

			for _, f := range report.Files {
				if _, err := fmt.Fprintf(app.Stdout(), "wrote %s (%d bytes)\n", f.Path, f.Bytes); err != nil {
					return err
				}
			}

			return nil
		},
	}
}
