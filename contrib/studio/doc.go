// Package studio is the vukem-studio data browser: a web interface for the
// databases of a Vukem project (SQL, MongoDB and Redis). It runs as its own
// binary (cmd/vukem-studio, started by `vukem studio`) and listens on loopback
// only, behind a one-time session token, strict security headers and a refusal
// to run against production unless asked. It is read-only unless started with
// --write.
//
// The interface is a React application built into static files that are
// committed (ui/) and embedded with go:embed; it talks to the JSON API under
// /api/v1 (docs/api.md). There is no JavaScript toolchain at install time: the
// source is in web/ and is rebuilt only by whoever changes it.
//
// This module is experimental.
package studio
