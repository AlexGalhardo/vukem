package cli

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

// dockerFS holds the Dockerfile and .dockerignore every starter ships.
//
//go:embed docker
var dockerFS embed.FS

// Base images are pinned to exact versions so a rebuild never picks up an
// unreviewed image, even if a tag is republished; the tag and its digest are
// bumped together with the toolchain.
const (
	nodeImage = "node:24.22.3-alpine3.24@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5"
	bunImage  = "oven/bun:1.4.4-alpine@sha256:4cf82f5686b196504fe119db3fcd9185b191f82ece82c40680e597228ec6070a"
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
// starter of kind built with pm. assetsImage, when set, replaces only the
// assets-stage base image; the lockfile install, the Vite build copy and
// the Alpine runtime stay as they are.
func writeDocker(dir, kind string, pm jsPM, assetsImage string) error {
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

	pinned, err := pinnedAssetsImage(assetsImage)
	if err != nil {
		return err
	}

	if pinned != "" {
		if !d.Assets {
			return errors.New("assets image: this starter has no assets stage")
		}

		d.JSImage = pinned
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

// pinnedAssetsImage rejects a floating tag. An empty ref means the package
// manager's default image. A digest (name@sha256:64 hex) is pinned.
func pinnedAssetsImage(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}

	if strings.ContainsAny(ref, " \t\r\n'\"") {
		return "", fmt.Errorf("assets image %q: must be one reference", ref)
	}

	if name, digest, ok := strings.Cut(ref, "@"); ok {
		hex, isSHA := strings.CutPrefix(digest, "sha256:")
		if !isSHA || name == "" || len(hex) != 64 || strings.Trim(hex, "0123456789abcdef") != "" {
			return "", fmt.Errorf("assets image %q: pin an exact tag or a sha256 digest", ref)
		}

		return ref, nil
	}

	name, tag, ok := strings.Cut(ref, ":")
	if !ok || name == "" || tag == "" {
		return "", fmt.Errorf("assets image %q: pin an exact tag or a sha256 digest", ref)
	}

	switch strings.ToLower(tag) {
	case "latest", "edge", "nightly", "lts", "stable", "alpine":
		return "", fmt.Errorf("assets image %q: floating tag", ref)
	}

	return ref, nil
}
