package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Plugin versions suggested by make:proto and codegen:rpc; they match the
// connect-go release contrib/grpc is built and tested with.
const (
	protobufVersion = "v1.36.12"
	connectVersion  = "v1.21.1"
	bufVersion      = "v1.74.0"

	protocGenGo        = "google.golang.org/protobuf/cmd/protoc-gen-go"
	protocGenConnectGo = "connectrpc.com/connect/cmd/protoc-gen-connect-go"
	bufTool            = "github.com/bufbuild/buf/cmd/buf"
)

func makeProto(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	force := set.Bool("force", false, "overwrite an existing .proto file")

	return func(_ context.Context, env *Env, args []string) error {
		name, err := oneArg(args, "service name")
		if err != nil {
			return err
		}

		d, err := newStubData(env, className(name, "Service"))
		if err != nil {
			return err
		}

		d.GoPkg = strings.ReplaceAll(d.Snake, "_", "") + "v1"
		d.ProtobufVersion, d.ConnectVersion = protobufVersion, connectVersion

		rel := fmt.Sprintf("proto/%s/v1/%s.proto", d.Snake, d.Snake)
		if err := generate(env, "service.proto.tmpl", rel, d, *force); err != nil {
			return err
		}

		for stub, file := range map[string]string{"buf.yaml.tmpl": "buf.yaml", "buf.gen.yaml.tmpl": "buf.gen.yaml"} {
			if exists(env, file) {
				continue
			}

			if err := generate(env, stub, file, d, false); err != nil {
				return err
			}
		}

		fmt.Fprintf(env.Stdout, `
Next:
  go get -tool %s@%s
  go get -tool %s@%s
  go get github.com/AlexGalhardo/vukem/contrib/grpc
  vukem codegen:rpc     # writes gen/%s/v1 (buf, or protoc when buf is missing)
`, protocGenGo, protobufVersion, protocGenConnectGo, connectVersion, d.Snake)

		return nil
	}
}

func codegenRPC(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	protoDir := set.String("proto", "proto", "directory of the .proto files (protoc mode)")
	out := set.String("out", "gen", "output directory (protoc mode)")
	useProtoc := set.Bool("protoc", false, "use protoc even when buf.gen.yaml exists")

	return func(ctx context.Context, env *Env, args []string) error {
		if len(args) != 0 {
			return usagef("codegen:rpc takes no arguments")
		}

		gomod, err := os.ReadFile(env.path("go.mod"))
		if err != nil {
			return fmt.Errorf("no go.mod here: run vukem inside a project (%w)", err)
		}

		if missing := missingTools(gomod, protocGenGo, protocGenConnectGo); len(missing) > 0 {
			var b strings.Builder
			for _, t := range missing {
				fmt.Fprintf(&b, "\n  go get -tool %s@%s", t, toolVersion(t))
			}

			return fmt.Errorf("codegen:rpc runs the code generators as Go tools; add them with:%s", b.String())
		}

		if !*useProtoc && exists(env, "buf.gen.yaml") {
			if hasTool(gomod, bufTool) {
				return tool(ctx, env, "go", "tool", "buf", "generate")
			}

			if _, err := exec.LookPath("buf"); err == nil {
				return tool(ctx, env, "buf", "generate")
			}
		}

		if _, err := exec.LookPath("protoc"); err != nil {
			return fmt.Errorf("codegen:rpc needs buf or protoc: go get -tool %s@%s (or install buf or protoc)",
				bufTool, bufVersion)
		}

		return protoc(ctx, env, *protoDir, *out)
	}
}

// protoc runs protoc with the Go tool plugins on every .proto file under
// dir, writing next to their import path in out.
func protoc(ctx context.Context, env *Env, dir, out string) error {
	var files []string

	root := env.path(dir)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".proto" {
			return err
		}

		rel, err := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))

		return err
	})
	if err != nil {
		return fmt.Errorf("codegen:rpc: read %s: %w", dir, err)
	}

	if len(files) == 0 {
		return fmt.Errorf("codegen:rpc: no .proto files in %s (vukem make:proto creates one)", dir)
	}

	args := []string{"-I", dir}

	for _, plugin := range []struct{ name, flag string }{
		{"protoc-gen-go", "go"},
		{"protoc-gen-connect-go", "connect-go"},
	} {
		bin, err := toolPath(ctx, env, plugin.name)
		if err != nil {
			return err
		}

		args = append(args,
			"--plugin="+plugin.name+"="+bin,
			"--"+plugin.flag+"_out="+out,
			"--"+plugin.flag+"_opt=paths=source_relative",
		)
	}

	if err := os.MkdirAll(env.path(out), 0o750); err != nil {
		return err
	}

	return tool(ctx, env, "protoc", append(args, files...)...)
}

// toolPath builds a Go tool of the project and returns its binary.
func toolPath(ctx context.Context, env *Env, name string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "tool", "-n", name)
	cmd.Dir = env.Dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go tool -n %s: %w\n%s", name, err, stderr.String())
	}

	return strings.TrimSpace(string(b)), nil
}

// hasTool reports whether go.mod declares pkg in a tool directive.
func hasTool(gomod []byte, pkg string) bool {
	return slices.Contains(tools(gomod), pkg)
}

func missingTools(gomod []byte, pkgs ...string) []string {
	declared := tools(gomod)

	var missing []string

	for _, p := range pkgs {
		if !slices.Contains(declared, p) {
			missing = append(missing, p)
		}
	}

	return missing
}

// tools lists the packages of go.mod's tool directives (single lines and
// blocks).
func tools(gomod []byte) []string {
	var out []string

	inBlock := false

	for line := range strings.SplitSeq(strings.ReplaceAll(string(gomod), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)

		switch {
		case inBlock && line == ")":
			inBlock = false
		case inBlock:
			if f := strings.Fields(line); len(f) > 0 && !strings.HasPrefix(f[0], "//") {
				out = append(out, f[0])
			}
		case line == "tool (":
			inBlock = true
		case strings.HasPrefix(line, "tool "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "tool ")))
		}
	}

	return out
}

func toolVersion(pkg string) string {
	switch pkg {
	case protocGenGo:
		return protobufVersion
	case protocGenConnectGo:
		return connectVersion
	default:
		return bufVersion
	}
}
