package cli

import (
	"bytes"
	"embed"
	"fmt"
	"path/filepath"
	"text/template"
)

// dockerFS holds the Dockerfile and .dockerignore every starter ships.
//
//go:embed docker
var dockerFS embed.FS

// Base images are pinned to exact versions so a rebuild never picks up an
// unreviewed image; bump them with the toolchain.
const (
	nodeImage = "node:24.22.0-alpine3.24"
	bunImage  = "oven/bun:1.4.3-alpine"
)

type dockerData struct {
	Assets   bool // the stack builds Vite assets
	Templ    bool
	JSImage  string
	Corepack bool // pnpm and yarn come from Node's corepack
	Lockfile string
	Install  string
	// ByHand is the command the developer runs to write the lockfile the
	// image installs from.
	ByHand string
	Build  string
}

// writeDocker writes the production Dockerfile and .dockerignore for a
// starter of kind built with pm.
func writeDocker(dir, kind string, pm jsPM) error {
	d := dockerData{
		Assets:   kind != "api",
		Templ:    kind == "templ",
		JSImage:  nodeImage,
		Lockfile: pm.lockfile,
		ByHand:   pm.install(),
		Build:    pm.name + " run build",
	}

	switch pm.name {
	case "bun":
		// The Dockerfile copies "bun.lock*", which also matches the binary bun.lockb.
		d.JSImage, d.Install = bunImage, "bun install --frozen-lockfile"
	case "pnpm":
		d.Corepack, d.Install = true, "pnpm install --frozen-lockfile"
	case "yarn":
		d.Corepack, d.Install = true, "yarn install --frozen-lockfile"
	default:
		d.Install = "npm ci --no-audit --no-fund"
	}

	src, err := dockerFS.ReadFile("docker/Dockerfile.tmpl")
	if err != nil {
		return err
	}

	t, err := template.New("Dockerfile").Option("missingkey=error").Parse(string(src))
	if err != nil {
		return fmt.Errorf("dockerfile: %w", err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return fmt.Errorf("dockerfile: %w", err)
	}

	if err := writeFile(filepath.Join(dir, "Dockerfile"), b.Bytes()); err != nil {
		return err
	}

	// Stored without the dot, like the starter's gitignore.
	ignore, err := dockerFS.ReadFile("docker/dockerignore")
	if err != nil {
		return err
	}

	return writeFile(filepath.Join(dir, ".dockerignore"), ignore)
}
