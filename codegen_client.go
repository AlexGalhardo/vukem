package vukem

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlexGalhardo/vukem/openapi"
)

const (
	defaultClientTS = "resources/js/api.ts"
	defaultClientGo = "internal/apiclient/client.go"
)

func codegenClientCommand() Command {
	var lang, out, pkg *string

	return CommandFunc{
		CommandName: "codegen:client",
		Desc:        "Generate a typed API client from the OpenAPI document (-lang ts|go; -o - prints to stdout)",
		DefineFlags: func(fs *flag.FlagSet) {
			lang = fs.String("lang", "ts", "client language: ts or go")
			out = fs.String("o", "", `write to this file ("-" for stdout; default `+
				defaultClientTS+" or "+defaultClientGo+")")
			pkg = fs.String("package", "apiclient", "Go package name (-lang go)")
		},
		Fn: func(_ context.Context, app *App, _ []string) error {
			doc, err := app.OpenAPI()
			if err != nil {
				return err
			}

			var (
				src    []byte
				target string
			)

			switch *lang {
			case "ts":
				ts, err := openapi.TypeScriptClient(doc)
				if err != nil {
					return err
				}

				src, target = []byte(ts), defaultClientTS
			case "go":
				if src, err = openapi.GoClient(doc, *pkg); err != nil {
					return err
				}

				target = defaultClientGo
			default:
				return fmt.Errorf("codegen:client: unknown -lang %q (want ts or go)", *lang)
			}

			if *out == "-" {
				_, err := app.stdout.Write(src)
				return err
			}

			if *out != "" {
				target = *out
			}

			path := filepath.FromSlash(target)
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return fmt.Errorf("codegen:client: %w", err)
			}

			return os.WriteFile(path, src, 0o600)
		},
	}
}
