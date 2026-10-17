# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.21.1] - 2026-10-17

### Added

- **logging:** Aggregate repeated identical errors
- **otel:** Add trace and span ids to log records

### Fixed

- **crypt:** Bound integer arithmetic flagged by CodeQL

### Security

- **cli:** Pin govulncheck as a go tool in new apps

## [0.21.0] - 2026-10-17

### Added

- **site:** Version the docs per minor with a header switcher
- **examples:** Add the JSON API example with typed task routes
- **examples:** Add the SaaS example with Inertia, auth and billing

### Changed

- **BREAKING:** **dbtest:** Unexport the conformance fixture models

### Deprecated

- **orm:** Rename create hook interfaces to BeforeCreator and AfterCreator
- **mail:** Rename WithAttachments to Attachable

### Fixed

- **web:** Reject same-host redirects to scheme-relative paths
- **graphql:** Bound introspection selections
- **graphql:** Mask 4xx errors that wrap a cause
- **web:** Read every X-Forwarded-For line in Ctx.IP
- **middleware:** Parse the media type before treating a body as JSON
- **httpclient:** Block IPv6 ranges that embed IPv4 addresses
- **grpc:** Hide the text of server-fault connect errors
- **BREAKING:** **app:** Refuse to boot with an unknown APP_ENV
- **BREAKING:** **ratelimit:** Key by client IP unless a verified token is available
- **cli:** Require the second factor for generated API tokens
- **cli:** Normalize login throttle keys and revoke tokens on reset
- **upload:** Verify only keys that Presign could have issued
- **upload:** Strip format characters from sanitized file names
- **upload:** Count nameless multipart parts toward the limits
- **middleware:** Handle an empty path in the maintenance bypass check
- **mail:** Reject attachment content types with line breaks
- **auth:** Consume remember-me tokens atomically

### Security

- **web:** Add package overviews and examples for the http core
- **repo:** Publish the 1.x stability policy, release cadence and upgrade guides
- **repo:** Rename the agent security rules to secure-coding.md
- **BREAKING:** **hash:** Replace DefaultArgon2 var with DefaultArgon2Params
- **repo:** Add fuzz targets for untrusted-input parsers
- **web:** Check the cleaned redirect path in SafeRedirect
- **storage:** Hide dot directories and sandbox served files
- **repo:** Name the test or mechanism behind every security default
- **security:** Record the B.5.2 security review

## [0.20.1] - 2026-10-17

### Changed

- **web:** Allocate the response writer inside Ctx and add WriteString
- **orm:** Reuse scan buffers across rows and presize limited Get

### Fixed

- **release:** Ignore driver and contrib tags when finding the previous release

### Security

- **docs:** Document release signing, sboms and verification

## [0.20.0] - 2026-10-17

### Added

- **graphql:** Add the contrib/graphql module with lexer and parser
- **graphql:** Add schemas, validation, execution and introspection
- **graphql:** Add the batching loader with ORM helpers
- **cli:** Add install:billing for Stripe subscriptions
- **graphql:** Add subscriptions over graphql-ws and SSE
- **app:** Serve http/2 cleartext outside production
- **grpc:** Add the contrib/grpc module on connect-go
- **cli:** Add make:proto and codegen:rpc
- **admin:** Add the admin panel package
- **cli:** Add install:admin and make:admin-resource

### Changed

- **BREAKING:** **grpc:** Name the production reflection opt-out insecure

### Fixed

- **session:** Flush through writers that only implement Unwrap

### Security

- **graphql:** Serve GraphQL over HTTP with persisted queries
- **graphql:** Document the contrib/graphql module

## [0.19.0] - 2026-10-16

### Added

- **social:** Add Apple, LinkedIn and X sign-in
- **stripe:** Add the Stripe API client and webhook module
- **s3:** Add the S3 storage driver module
- **database:** Add the mysql dialect and a url-to-dsn driver hook
- **schema:** Generate mysql ddl and drop tables on mysql
- **orm:** Support the mysql dialect in inserts and queries
- **queue:** Run the database queue on mysql
- **cache:** Run the database cache store on mysql
- **session:** Add a mysql option to the sql session store
- **boost:** Read the database schema on mysql
- **mysql:** Add the drivers/mysql module for mysql 8 and mariadb 11
- **openapi:** Generate typed TypeScript and Go API clients
- **cli:** Add the codegen:client app command
- **web:** Add context session helpers and Partial for components
- **vite:** Carry the Vite instance in the request context
- **cli:** Add the templ starter
- **database:** Add a query observer hook
- **queue:** Add a job observer hook
- **events:** Add a dispatch observer hook
- **devtools:** Add the /_vukem development dashboard
- **broadcast:** Add channel broadcasting with an SSE transport
- **websocket:** Serve the broadcast protocol over WebSockets
- **web:** Let a route body limit replace the router default
- **BREAKING:** **middleware:** Read _method from the query for multipart forms
- **z:** Add z.File for uploaded files
- **cli:** Add an upload demo with progress to the HTMX starter
- **cli:** Add install:auth --2fa for TOTP two-factor authentication

### Fixed

- **cli:** Drop the html/template upload demo from the templ starter

### Security

- **websocket:** Add the WebSocket driver module
- **upload:** Add streaming, resumable and direct uploads

## [0.18.0] - 2026-10-16

### Added

- **vite:** Add the vite package and view tag functions
- **inertia:** Add the Inertia v3 core adapter
- **cli:** Add Vukem Boost for coding agents
- **inertia:** Add prop types and partial reload filtering
- **inertia:** Add history encryption, flash, prefetch and precognition
- **inertia:** Add the SSR client with client-side fallback
- **cli:** Ship Vite and Tailwind v4 in the HTMX starter and prompt for the stack
- **vukemtest:** Add WithInertia and Inertia assertions
- **cli:** Add codegen:routes for typed frontend routes
- **cli:** Add the Inertia React starter and install:inertia
- **cli:** Add Inertia auth pages to install:auth
- **web:** Add a youch-style debug error page
- **sse:** Add server-sent event streams and a topic hub
- **cli:** Add maintenance mode with vukem down and up
- **auth:** Add the JWT guard with JWKS rotation and refresh tokens
- **crypt:** Add encryption with key rotation and encrypted cookies
- **orm:** Add the Encrypted column type
- **z:** Record message keys and localize issues
- **web:** Localize validation issues from the request context
- **i18n:** Add JSON catalogs, plurals and locale resolution
- **view:** Add the t and locale template functions
- **i18n:** Wire translations into the app
- **cli:** Add the Inertia Vue starter
- **auth:** Add the TOTP package with a stdlib QR encoder
- **cli:** Add the Inertia Svelte starter

### Fixed

- **site:** Align the docs TOC with the header and drop the pre-1.0 callouts

### Security

- **otel:** Add the OpenTelemetry driver module

## [0.17.0] - 2026-10-16

### Added

- **site:** Add the Vukem icon, align the header and open on the docs
- **redis:** Add the Redis driver module
- **mongo:** Add the MongoDB driver module

### Security

- **repo:** Rewrite the README after the Laravel layout

## [0.16.0] - 2026-10-16

### Added

- **queue:** Add typed jobs, workers and the sync and memory drivers
- **queue:** Add the database driver, DispatchTx and failed-job commands
- **queue:** Add make:job, queue.Fake, queued mail and queued listeners
- **schedule:** Add the scheduler with cron expressions and overlap locks

## [0.15.0] - 2026-10-16

### Added

- **api:** Add typed handlers with api.Register
- **openapi:** Generate OpenAPI 3.1 documents from the router
- **vukemtest:** Assert responses match the OpenAPI document
- **openapi:** Export JSON Schema and generate TypeScript and Zod types
- **api:** Add the Idempotency-Key middleware
- **apidocs:** Serve a Scalar API reference for the OpenAPI document

## [0.14.0] - 2026-10-16

### Added

- **examples:** Add the blog example and auth.Optional

### Security

- **site:** Document every alpha feature

## [0.13.0] - 2026-10-16

### Security

- **security:** Add SSRF-safe client, webhook signatures and security events

## [0.12.0] - 2026-10-16

### Added

- **logging:** Add the Telegram slog handler

## [0.11.0] - 2026-10-16

### Added

- **subsystems:** Add cache, file storage and typed events

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

- **BREAKING:** Rename the framework to Vukem

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

- **BREAKING:** **cli:** Add vukem new with the HTMX + SQLite starter

## [0.0.11] - 2026-10-16

### Added

- **cli:** Add the vukem command with generators, serve, build and delegation

## [0.0.10] - 2026-10-16

### Added

- **vukemtest:** Add testing toolkit with fluent client and DB assertions

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

[Unreleased]: https://github.com/AlexGalhardo/vukem/compare/v0.21.1...HEAD
[0.21.1]: https://github.com/AlexGalhardo/vukem/compare/v0.21.0...v0.21.1
[0.21.0]: https://github.com/AlexGalhardo/vukem/compare/v0.20.1...v0.21.0
[0.20.1]: https://github.com/AlexGalhardo/vukem/compare/v0.20.0...v0.20.1
[0.20.0]: https://github.com/AlexGalhardo/vukem/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/AlexGalhardo/vukem/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/AlexGalhardo/vukem/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/AlexGalhardo/vukem/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/AlexGalhardo/vukem/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/AlexGalhardo/vukem/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/AlexGalhardo/vukem/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/AlexGalhardo/vukem/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/AlexGalhardo/vukem/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/AlexGalhardo/vukem/compare/v0.10.0...v0.11.0
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
