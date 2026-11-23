#!/usr/bin/env bash
# Replaces the embedded finance scaffold with the archive of a published
# release of github.com/AlexGalhardo/vukem-finances and pins that tag.
#
#   scripts/update-finance-scaffold.sh v0.7.0
#
# Needs gh (authenticated for the public repository), tar and sha256sum.
# It never reads an unreleased branch: the tag must have a release with the
# scaffold-<tag>.tar.gz and scaffold-<tag>.MANIFEST.sha256 assets.
set -euo pipefail

tag="${1:-}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "usage: $0 vX.Y.Z" >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dest="$root/internal/cli/scaffolds/finance"
const="$root/internal/cli/finance_scaffold.go"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

gh release download "$tag" -R AlexGalhardo/vukem-finances -p 'scaffold-*' -D "$work"
archive="$work/scaffold-$tag.tar.gz"
manifest="$work/scaffold-$tag.MANIFEST.sha256"

mkdir "$work/tree"
tar -xzf "$archive" -C "$work/tree"

if [ "$(tr -d '\r\n' <"$work/tree/VERSION")" != "$tag" ]; then
	echo "VERSION inside the archive is not $tag" >&2
	exit 1
fi

# The manifest asset must be the one inside the archive, and every file must match it.
cmp "$manifest" "$work/tree/MANIFEST.sha256"
(cd "$work/tree" && sha256sum --quiet -c "$manifest")

listed="$(wc -l <"$manifest")"
found="$(cd "$work/tree" && find . -type f ! -name MANIFEST.sha256 | wc -l)"
if [ "$listed" -ne "$found" ]; then
	echo "the manifest lists $listed files, the archive has $found" >&2
	exit 1
fi

cp "$archive" "$dest/scaffold.tar.gz"
cp "$manifest" "$dest/MANIFEST.sha256"
sed -i -E "s/^(const financeScaffoldTag = )\"v[0-9]+\.[0-9]+\.[0-9]+\"/\1\"$tag\"/" "$const"

grep -q "financeScaffoldTag = \"$tag\"" "$const"
echo "finance scaffold pinned to $tag"
echo "check financeStripeVersion in $const against go.mod of the app at $tag, then run:"
echo "  go test ./internal/cli/... -run TestFinanceScaffold"
