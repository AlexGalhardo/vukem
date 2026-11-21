#!/usr/bin/env bash
# Proves that `go install` of vukem-studio needs no JavaScript toolchain: the
# module is copied without web/ (and so without node_modules), built with
# GOWORK=off, started on a free port and asked for /ui/ with the start token.
set -euo pipefail

module_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
pid=""

cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

mkdir "$work/module"
cp -R "$module_dir"/. "$work/module/"
rm -rf "$work/module/web"

bin="$work/vukem-studio$(go env GOEXE)"
(cd "$work/module" && GOWORK=off go build -o "$bin" ./cmd/vukem-studio)

APP_ENV=development "$bin" --no-open --port 0 --database-url ":memory:" \
  >"$work/stdout.log" 2>"$work/stderr.log" &
pid=$!

url=""
for _ in $(seq 1 100); do
  url="$(sed -n 's/^  open  \(http:[^ ]*\)$/\1/p' "$work/stdout.log" | tr -d '\r')"
  [ -n "$url" ] && break
  kill -0 "$pid" 2>/dev/null || { echo "vukem-studio exited early:" >&2; cat "$work/stderr.log" >&2; exit 1; }
  sleep 0.1
done
[ -n "$url" ] || { echo "no start URL printed" >&2; exit 1; }

base="${url%%/\?token=*}"
jar="$work/cookies.txt"
curl -sS -o /dev/null -c "$jar" "$url"
status="$(curl -sS -o "$work/ui.html" -w '%{http_code}' -b "$jar" "$base/ui/")"
if [ "$status" != "200" ]; then
  echo "GET /ui/ = $status, want 200" >&2
  exit 1
fi
grep -q '<div id="root">' "$work/ui.html" || { echo "GET /ui/ did not return the app shell" >&2; exit 1; }

echo "ok: built without web/ and served /ui/ with 200"
