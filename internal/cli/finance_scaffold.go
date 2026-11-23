package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
)

// financeScaffoldTag is the release of github.com/AlexGalhardo/vukem-finances
// whose exported tree is embedded. Only scripts/update-finance-scaffold.sh
// changes it, together with the archive and the manifest.
const financeScaffoldTag = "v0.8.0"

// financeStripeVersion is the contrib/stripe release that app release pins;
// the exported tree carries no go.mod, so the CLI writes the pin itself.
const financeStripeVersion = "v0.5.3"

// financeImageRepo is the registry path of the app's own image.
const financeImageRepo = "ghcr.io/alexgalhardo/vukem-finances"

const (
	financeArchivePath  = "scaffolds/finance/scaffold.tar.gz"
	financeManifestPath = "scaffolds/finance/MANIFEST.sha256"

	// financeManifestName and financeVersionName are files of the archive that
	// describe the export and do not belong to the generated project.
	financeManifestName = "MANIFEST.sha256"
	financeVersionName  = "VERSION"

	// financeLicenseName is the app's MIT text inside the archive; a generated
	// project keeps it as NOTICE.
	financeLicenseName = "LICENSE.vukem-finances"
)

// financeAppVersion is the app version without the "v", as the image tags it.
func financeAppVersion() string { return strings.TrimPrefix(financeScaffoldTag, "v") }

// financeFile is one file of the exported tree, named as in the archive.
type financeFile struct {
	rel     string
	content []byte
	exec    bool
}

// financeScaffoldFiles reads the embedded archive in the order it stores
// them. Names that are not plain relative paths and entries that are not
// regular files are refused: the archive is data, not trusted code.
func financeScaffoldFiles() ([]financeFile, error) {
	raw, err := scaffoldFS.ReadFile(financeArchivePath)
	if err != nil {
		return nil, err
	}

	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("finance scaffold: %w", err)
	}

	defer gz.Close()

	var files []financeFile

	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}

		if err != nil {
			return nil, fmt.Errorf("finance scaffold: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("finance scaffold: %s is not a regular file", hdr.Name)
		}

		if !fs.ValidPath(hdr.Name) || strings.Contains(hdr.Name, "\\") {
			return nil, fmt.Errorf("finance scaffold: unsafe name %q", hdr.Name)
		}

		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("finance scaffold: %s: %w", hdr.Name, err)
		}

		files = append(files, financeFile{rel: hdr.Name, content: content, exec: hdr.Mode&0o111 != 0})
	}
}

// parseManifest reads "sha256  path" lines into a map.
func parseManifest(src []byte) (map[string]string, error) {
	out := map[string]string{}

	for line := range strings.SplitSeq(strings.TrimSpace(string(src)), "\n") {
		sum, name, ok := strings.Cut(strings.TrimRight(line, "\r"), "  ")
		if !ok || len(sum) != sha256.Size*2 || name == "" {
			return nil, fmt.Errorf("manifest: bad line %q", line)
		}

		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("manifest: %s listed twice", name)
		}

		out[name] = sum
	}

	return out, nil
}

// verifyFinanceScaffold checks the archive against the embedded manifest:
// every file but the manifest itself is listed with its hash, and nothing
// else is. The generator calls it, so a tampered archive never renders.
func verifyFinanceScaffold(files []financeFile, manifest []byte) error {
	want, err := parseManifest(manifest)
	if err != nil {
		return err
	}

	seen := map[string]bool{}

	for _, f := range files {
		if f.rel == financeManifestName {
			if !bytes.Equal(bytes.TrimSpace(f.content), bytes.TrimSpace(manifest)) {
				return errors.New("finance scaffold: the archive's MANIFEST.sha256 differs from the embedded one")
			}

			continue
		}

		sum := sha256.Sum256(f.content)

		switch got := hex.EncodeToString(sum[:]); {
		case want[f.rel] == "":
			return fmt.Errorf("finance scaffold: %s is not in the manifest", f.rel)
		case want[f.rel] != got:
			return fmt.Errorf("finance scaffold: %s has hash %s, the manifest says %s", f.rel, got, want[f.rel])
		}

		if seen[f.rel] {
			return fmt.Errorf("finance scaffold: %s is stored twice", f.rel)
		}

		seen[f.rel] = true
	}

	for name := range want {
		if !seen[name] {
			return fmt.Errorf("finance scaffold: %s is in the manifest but not in the archive", name)
		}
	}

	return nil
}

// loadFinanceScaffold returns the verified files of the exported app.
func loadFinanceScaffold() ([]financeFile, error) {
	files, err := financeScaffoldFiles()
	if err != nil {
		return nil, err
	}

	manifest, err := scaffoldFS.ReadFile(financeManifestPath)
	if err != nil {
		return nil, err
	}

	if err := verifyFinanceScaffold(files, manifest); err != nil {
		return nil, err
	}

	slices.SortFunc(files, func(a, b financeFile) int { return strings.Compare(a.rel, b.rel) })

	return files, nil
}
