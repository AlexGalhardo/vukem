# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.10.0] - 2026-10-16

### Added

- **cli:** Add the JSON API starter and install:api

## [0.9.0] - 2026-10-16

### Added

- **cli:** Add install:auth --social with account linking

## [0.8.0] - 2026-10-16

### Added

- **cli:** Add install:auth with registration, verification and password resets

## [0.7.0] - 2026-10-16

### Added

- **mail:** Add mailables with SMTP, Resend, log and array transports

## [0.6.0] - 2026-10-16

### Added

- **authz:** Add gates and policies with fail-closed authorization

## [0.5.0] - 2026-10-16

### Added

- **auth:** Add hashers, session and token guards, and auth middleware
- **social:** Add OAuth2/OIDC social login with PKCE

## [0.4.0] - 2026-10-16

### Added

- **orm:** Add relations, factories, seeders and database rules

## [0.3.0] - 2026-10-16

### Added

- **postgres:** Add the PostgreSQL driver module and conformance suite

### Changed

- **BREAKING:** Rename the framework from Lumek to Vukem

## [0.2.0] - 2026-10-16

### Added

- **api:** Add REST toolkit with pagination, query conventions and rate limiting

## [0.1.1] - 2026-10-16

### Added

- **cli:** Answer JSON from the starter's post controller

## [0.1.0] - 2026-10-16

### Changed

- Maintenance release (no user-facing changes).

## [0.0.12] - 2026-10-16

### Added

- **BREAKING:** **cli:** Add lumek new with the HTMX + SQLite starter

## [0.0.11] - 2026-10-16

### Added

- **cli:** Add the lumek command with generators, serve, build and delegation

## [0.0.10] - 2026-10-16

### Added

- **lumektest:** Add testing toolkit with fluent client and DB assertions

## [0.0.9] - 2026-10-16

### Added

- **view:** Add html/template views, htmx 4 helpers and fragments

## [0.0.8] - 2026-10-16

### Added

- **orm:** Add database connections, generic query builder and migrations

## [0.0.7] - 2026-10-16

### Added

- **session:** Add sessions, flash, old input and csrf protection

## [0.0.6] - 2026-10-16

### Added

- **z:** Add zod-style typed schemas and web parsing
- **site:** Add documentation site with Next.js 16, Tailwind v4 and Base UI

### Fixed

- **deps:** Require go1.27.2 toolchain for standard library security fixes

## [0.0.5] - 2026-10-16

### Added

- **middleware:** Add built-in middleware, app HTTP wiring and commands

## [0.0.4] - 2026-10-16

### Added

- **router:** Add ServeMux router, web.Ctx, errs and problem details

## [0.0.3] - 2026-10-16

### Added

- **app:** Add App, provider lifecycle, runners and HTTP server

## [0.0.2] - 2026-10-16

### Added

- **config:** Add .env parser, typed struct loading, secrets and app key

## [0.0.1] - 2026-10-16

Project foundation. No framework code yet; see [PLAN.md](PLAN.md) for the roadmap.

### Added

- Research on Laravel 13, AdonisJS v7, Goravel, the Go ecosystem, Inertia v3 and htmx 4 (`docs/golang-web-search.md`).
- Design decision log (`docs/brainstorming.md`) and design docs: architecture, configuration, HTTP, REST APIs, security (OWASP Top 10:2025), databases, authentication, frontend, CLI, subsystems, testing and tooling.
- Phased plan (`PLAN.md`): Prototype, ALPHA, BETA and 1.0 Stable, with verifiable tasks and exit criteria.
- Conventional Commits validator (`internal/tools/commitlint`) used by the `commit-msg` hook.
- Tooling: golangci-lint v2 (gofumpt, gci, golines, depguard), markdownlint-cli2, lefthook hooks, Taskfile, git-cliff, `.editorconfig` and `.gitattributes`.
- GitHub workflows (CI, PR title, CodeQL), Dependabot, issue forms and PR template.
- README, MIT license, CONTRIBUTING, SECURITY and CODE_OF_CONDUCT.
- Agent configuration: `AGENTS.md`, `CLAUDE.md`, path-scoped `.claude/rules/`, Go skills and project settings.

[Unreleased]: https://github.com/AlexGalhardo/vukem/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/AlexGalhardo/vukem/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/AlexGalhardo/vukem/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/AlexGalhardo/vukem/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/AlexGalhardo/vukem/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/AlexGalhardo/vukem/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/AlexGalhardo/vukem/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/AlexGalhardo/vukem/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/AlexGalhardo/vukem/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/AlexGalhardo/vukem/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/AlexGalhardo/vukem/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/AlexGalhardo/vukem/compare/v0.0.12...v0.1.0
[0.0.12]: https://github.com/AlexGalhardo/vukem/compare/v0.0.11...v0.0.12
[0.0.11]: https://github.com/AlexGalhardo/vukem/compare/v0.0.10...v0.0.11
[0.0.10]: https://github.com/AlexGalhardo/vukem/compare/v0.0.9...v0.0.10
[0.0.9]: https://github.com/AlexGalhardo/vukem/compare/v0.0.8...v0.0.9
[0.0.8]: https://github.com/AlexGalhardo/vukem/compare/v0.0.7...v0.0.8
[0.0.7]: https://github.com/AlexGalhardo/vukem/compare/v0.0.6...v0.0.7
[0.0.6]: https://github.com/AlexGalhardo/vukem/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/AlexGalhardo/vukem/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/AlexGalhardo/vukem/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/AlexGalhardo/vukem/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/AlexGalhardo/vukem/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/AlexGalhardo/vukem/releases/tag/v0.0.1
