#!/usr/bin/env bash
# pre-push: regenerate site/content/last-updated.json so the pushed tree never
# fails the "Last updated" check. Git fixed the pushed refs before this hook
# ran, so a refresh commit cannot join this push: it is committed and the push
# stops, and the next `git push` carries it.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)/site"
if [ ! -d node_modules ]; then
	echo "pre-push: run npm ci in site/ so the doc dates can be refreshed" >&2
	exit 1
fi

npm run -s last-updated >/dev/null
if git diff --quiet -- content/last-updated.json; then
	exit 0
fi

git commit -q -m "chore(site): refresh last-updated" -- content/last-updated.json
echo "pre-push: committed the refreshed doc dates (site/content/last-updated.json); run git push again" >&2
exit 1
