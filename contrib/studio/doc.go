// Package studio is the vukem-studio data browser: a read-only web interface
// for the databases of a Vukem project. It runs as its own binary
// (cmd/vukem-studio, started by `vukem studio`) and listens on loopback only,
// behind a session token, strict security headers and a refusal to run
// against production unless asked.
//
// This module is experimental. It serves two interfaces: the pages rendered
// with html/template and htmx, and a React interface under /ui/ that talks to
// the JSON API under /api/v1 (docs/api.md). The React build is committed in
// ui/ and embedded, so installing the binary needs no JavaScript toolchain.
package studio
