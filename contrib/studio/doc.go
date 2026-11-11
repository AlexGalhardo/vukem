// Package studio is the vukem-studio data browser: a read-only web interface
// for the databases of a Vukem project. It runs as its own binary
// (cmd/vukem-studio, started by `vukem studio`) and listens on loopback only,
// behind a session token, strict security headers and a refusal to run
// against production unless asked.
//
// This module is experimental. Rendering uses html/template and htmx; there is
// no JavaScript build.
package studio
