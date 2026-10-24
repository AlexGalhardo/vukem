#!/usr/bin/env bash
# Regenerates the README GIFs. Run from the repository root: docs/media/record.sh [name...]
# Needs Docker. See docs/media/README.md.
set -euo pipefail

root="$(pwd)"
names=("$@")
if [ ${#names[@]} -eq 0 ]; then
	names=(new-api new-htmx new-inertia-react tests seeds-migrations scaffold)
fi

GOOS=linux GOARCH=amd64 go build -o bin/vukem ./cmd/vukem
docker build -t vukem-vhs:local -f docs/media/Dockerfile bin

for n in "${names[@]}"; do
	MSYS_NO_PATHCONV=1 docker run --rm -v "$root/docs/media:/vhs" -v "$root:/src" -v vukem-vhs-gomod:/root/go -v vukem-vhs-gobuild:/root/.cache -w /vhs \
		vukem-vhs:local "tapes/$n.tape"
done
