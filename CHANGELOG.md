# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.26.0] - 2026-10-17

### Added

- **stripe:** Add typed Accounts v2, refunds and transfer reversals
- **BREAKING:** **cache:** Add Sharer contract and fail closed on undeclared stores
- **redis:** Declare the cache store shared
- **cache:** Bound Unwrap chains with cache.Layers
- **database:** Add RebindChecked that rejects unterminated SQL and mixed placeholders
- **BREAKING:** **queue:** Pop same-millisecond database jobs in push order
- **signed:** Verify links signed with previous keys
- **social:** Accept state cookies signed with previous keys
- **session:** Open cookie sessions sealed with previous keys
- **middleware:** Accept maintenance bypass cookies from previous keys
- **cli:** Add key:rotate and pass previous keys in auth scaffolds
- **grpc:** Add ErrorContext to translate keyed errors into the request locale
- **schema:** Add Schema.HasColumn for upgrade migrations
- **BREAKING:** **auth:** Soft revoke API tokens with revoked_at
- **cli:** Add revoked_at to the install:auth access_tokens table
- **cli:** Revoke API tokens with Tokens.RevokeAll on password reset
- **BREAKING:** **i18n:** Return the config error from Middleware instead of panicking
- **mail:** Add the Mailtrap Email API transport
- **vukem:** Select the Mailtrap transport with MAIL_MAILER=mailtrap

### Changed

- **graphql:** Derive the 5xx check from errs.Status
- **BREAKING:** **orm:** Remove the deprecated BeforeCreater and AfterCreater aliases
- **BREAKING:** **mail:** Remove the deprecated WithAttachments alias
- **BREAKING:** **stripe:** Remove the deprecated Client.Do

### Fixed

- **BREAKING:** **stripe:** Map v2 errors and validate Call paths and object ids
- **BREAKING:** **schedule:** Check OnOneServer locks with cache.Shared
- **BREAKING:** **vukem:** Require a declared shared cache for RATE_LIMIT_STORE=cache
- **schedule:** Name cache.Sharer in ErrLocksNotShared
- **database:** Quote only an 80-byte prefix of the query in RebindChecked errors
- **queue:** Add the seq column only on an unknown-column probe error
- **cli:** Keep the starter upload test off the filesystem
- **cli:** Make key:rotate atomic and keep .env line syntax
- **graphql:** Keep loader batches whole while siblings make progress
- **graphql:** Translate keyed resolver errors into the request locale
- **graphql:** Hide field errors wrapped in a 5xx HTTPError
- **BREAKING:** **grpc:** Send 5xx HTTPErrors as "internal error" and log their cause
- **graphql:** Cap how long a pending loader batch waits for progress
- **BREAKING:** **grpc:** Mask unknown Validate errors and 5xx headers
- **auth:** Answer 503 when the token store fails
- **auth:** Keep revoking possible on access_tokens without revoked_at
- **mail:** Harden Mailtrap error decoding and redirects

### Security

- **cache:** Document the cache.Sharer contract
- **vukem:** Honour APP_PREVIOUS_KEYS in every APP_KEY-derived feature
- **web:** Add AuditEvent for routine audit records at info
- **BREAKING:** **cli:** Make the install:auth sign-up enumeration-safe
- **BREAKING:** **cli:** Require verified email and rate limit sign-up in install:auth
- **BREAKING:** **cli:** End squatter access when the email is proven

## [0.25.0] - 2026-10-17

### Added

- **middleware:** Log peer address and resolved client IP per request
- **middleware:** Add HSTSForce and HSTS_FORCE for untrusted TLS proxies
- **stripe:** Add connect resources and per-request idempotency and account options
- **stripe:** Accept connect webhook secrets and expose event account
- **router:** Report whether routes were compiled
- **vukem:** Add WithRoutesAfterBoot for routes that need booted services
- **vukem:** Let seeders reach the app with WithAppSeeders
- **vukem:** Name seeders through an optional Name method
- **export:** Stream CSV, JSON and NDJSON downloads
- **errs:** Add message keys and a request translator to errors
- **web:** Show keyed http errors in the request locale
- **i18n:** Ship built-in en and pt-BR framework messages
- **auth:** Translate guard errors through message keys
- **social:** Add message keys to callback errors
- **orm:** Translate unique and exists rule messages
- **mail:** Build envelopes from the send context with ContextEnveloper
- **telegram:** Add SendSync/ContactSync, Filter and RateLimit
- **vukem:** Add TELEGRAM_LOG, TELEGRAM_RATE_LIMIT and WithTelegram
- **webhook:** Add DeliveryLogFunc and document DeliveryLog as the extension point
- **grpc:** Serve rpc services on their own port as an app runner
- **s3:** Add idempotent region-aware CreateBucket
- **totp:** Encode qr codes up to version 40
- **api:** Add Op.ValidateResponse to check typed handler output
- **BREAKING:** **openapi:** Emit zod 4 schemas and inline request body schemas
- **vukem:** Run codegen:types without a database or APP_KEY
- **BREAKING:** **i18n:** Add locale source order and allowed locales to MiddlewareConfig
- **vukem:** Add MiddlewareOptions.I18n and APIMiddlewareWith
- **inertia:** Add RenderStatus for pages with a non-200 status
- **i18n:** Fail closed on invalid middleware config and vary on cookie
- **vukem:** Install the locale resolver in APIMiddleware
- **vukem:** Add ServeOpenAPIWith for prefixed, production-opted documents
- **BREAKING:** **schema:** Add partial indexes with Index/UniqueIndex(...).Where(expr)
- **database:** Add Rebind for portable raw SQL placeholders
- **database:** Fan out query observers and add vukem.WithQueryObservers
- **uuid:** Add stdlib UUID v4/v7 package
- **session:** List a user's sessions and revoke one by handle
- **vukem:** Read SESSION_RECORD_CLIENT into the session config
- **auth:** End sessions on every driver with a per-user session version
- **vukemtest:** Add WithSession and stamp the session version in ActingAs
- **redis:** Implement session.ListStore on the session store
- **cli:** End every session on password reset with a session version
- **queue:** Add Queue.SetClock and the queue.Clock driver interface
- **queue:** Bound the worker drain with WorkOptions.StopTimeout
- **queue:** Add DispatchKindTx
- **redis:** Implement queue.Clock on the queue driver
- **mail:** Add Mailer.SendLaterTx
- **schedule:** Add Task.OnOneServer
- **BREAKING:** **app:** Run a queue worker in serve for persistent queues
- **app:** Add MailLaterTx
- **app:** Refuse OnOneServer schedules on the in-process cache
- **schedule:** Namespace task locks with Options.Namespace
- **httpclient:** Add Options.WrapTransport for tracing and metrics
- **vukem:** Add App.Instrument hooks for database, queue and mail
- **otel:** Add metrics, data-layer spans and OTEL_* settings from app.Source
- **otel:** Export metrics over OTLP/HTTP from OTEL_* settings

### Changed

- **stripe:** Deprecate Do in favour of Call

### Fixed

- **middleware:** Warn when HSTS_FORCE is ignored and document its scope and log privacy
- **tools:** Report compatible apidiff changes as unchecked in gate mode
- **stripe:** Refuse account scope on platform calls and decode expanded ids
- **router:** Mark the router compiled before building it
- **vukem:** Fail closed around routes after boot and keep the boot error
- **middleware:** Hijack through wrapped writers in gzip
- **web:** Sanitize Download file names
- **export:** Renew write deadline per write and always abort mid-stream
- **i18n:** Fill only existing catalogs of the same locale with built-in messages
- **errs:** Copy the params given to NewKey
- **broadcast:** Show keyed sse refusals in the request locale
- **sse:** Release a topic's broker subscription when its last subscriber leaves
- **telegram:** Report errors outside the queue lock and never forward OnError records
- **sse:** Make every subscriber wait for its topic relay and fail closed
- **vukem:** Show telegram log forwarding off in about
- **graphql:** Fold go names of json-tagged fields in the default resolver
- **graphql:** Free a websocket subscription id before its terminal message
- **grpc:** Refuse a cleartext production port in Server too
- **BREAKING:** **z:** Treat required on a list as present, not non-empty
- **openapi:** Emit minLength 1 for required strings
- **BREAKING:** **openapi:** Suffix component names that shadow typescript globals
- **vukem:** Never boot a warmup app and export openapi in warmup
- **openapi:** Reserve the fetch types the generated client uses
- **openapi:** Fail typescript codegen on colliding generated names
- **web:** Answer 422 for z.FieldError and z.Issues returned by handlers
- **BREAKING:** **z:** Hide the text of plain Refine and Custom errors
- **web:** Fail closed on z issues and skip redirect-back for GET
- **upload:** Send z validation messages from tus completion
- **apidocs:** Keep the nonce-only CSP and pass axe serious checks
- **auth:** Answer problem+json on token routes and use the app clock
- **auth:** Force JSON only on the error path of API guards
- **openapi:** Match PathPrefix on whole path segments
- **apidocs:** Limit the CSP shim to plain style values and hide scalar.com links
- **orm:** Share the Rebind lexer for WhereRaw placeholder counting
- **dbtest:** Bound each conformance subtest and shrink the MySQL bulk insert
- **orm:** Lex MySQL -- comments, backticks and SQLite brackets per dialect
- **orm:** Reject $n in WhereRaw and fail closed on unterminated WHERE SQL
- **database:** Contain panicking query observers and ignore typed nils
- **auth:** Bind remember-me tokens to the user's session version
- **cli:** Delete remember-me tokens when ending a user's other sessions
- **redis:** Key sessions by SHA-256 of the id and re-key legacy keys
- **auth:** Harden session version checks and remember-me deletion
- **session:** Strip control and format runes from the recorded user agent
- **app:** Follow the app clock in the queue and its drivers
- **app:** Stop waiting for runners that ignore ctx after SHUTDOWN_TIMEOUT
- **cli:** Queue the verification mail in the auth scaffold
- **queue:** Return reserve errors from Work with StopWhenEmpty
- **app:** Create the database queue tables at boot
- **app:** Namespace schedule locks by APP_NAME
- **queue:** Free only shutdown-caused interruptions, at most three in a row
- **app:** Give runners three seconds of grace after SHUTDOWN_TIMEOUT
- **app:** Name the database queue in its boot error
- **httpclient:** Fail closed when WrapTransport returns nil
- **otel:** Harden provider boot, shutdown and globals; add db.system.name
- **vukem:** Chain Instrument query hooks through database observers
- **queue:** Contain panicking job observers and add AddObserver
- **vukem:** Add Instrument job hooks through queue.AddObserver
- **tools:** Exempt the sessiontest conformance suite from the coverage gate

### Security

- **site:** Expand cache, scheduler, idempotency, two-factor, redis and mongodb pages

## [0.24.0] - 2026-10-17

### Added

- **schema:** Add named CHECK constraints with Check and DropCheck
- **orm:** Add row locks with LockForUpdate, ForShare, SkipLocked and NoWait
- **orm:** Add CreateMany for batched multi-row inserts
- **z:** Add FieldErrorKey for translatable refine messages
- **z:** Add StringSchema.Preprocess to normalize before checks
- **errs:** Add ValidationError.Append without a result
- **vukemtest:** Add Build to assert that an app fails to build or boot
- **vukemtest:** Add flash assertions for web and Inertia responses
- **vukemtest:** Add AssertNoFlash and require a present flash value
- **vukem:** Run the scheduler inside serve
- **BREAKING:** **vukem:** Run the scheduler inside serve by default
- **s3:** Bind presigned PUT URLs to content type and length
- **mongo:** Add OpenLazy and document MONGO_DATABASE and authSource
- **redis:** Add OpenLazy so the app can boot while Redis is down
- **middleware:** Add CSPSources and ReportURI to SecureHeadersConfig
- **vukem:** Add DefaultMiddlewareWith with CSP, stateless and bypass options
- **auth:** Add a configurable prefix to api tokens
- **webhook:** Add hmac, a middleware constructor that fails at boot
- **BREAKING:** **cli:** Translate install:auth messages with en and pt-BR catalogs
- **database:** Add the vukem_nosqlite build tag to leave SQLite out
- **cli:** Add vukem new --minimal and project build tags
- **session:** Store hashed ids and revoke a user's other sessions
- **auth:** Record the session user on login
- **cli:** End other sessions on password reset and 2fa enable
- **database:** Add Tx.AfterCommit hooks
- **app:** Add WithJobsFunc so job handlers reach the app
- **mail:** Add Mailer.SendLater
- **app:** Encrypt queued mail payloads and wire Mailer.SendLater
- **vukem:** Add ReadyHandler, CheckHealth and per-check health timeouts
- **vukem:** Expose Stdout, Stderr and Stdin to app commands
- **BREAKING:** **vukem:** Boot without APP_KEY outside production and fail closed on use
- **vukem:** Add lazy cache, session and queue store options
- **middleware:** Honour X-Forwarded-Proto only from trusted proxies
- **vukem:** Read TRUSTED_PROXIES and TRUST_FORWARDED_PROTO from the environment
- **cli:** Ship a production Dockerfile and .dockerignore in every starter
- **web:** Read the client IP from a named proxy header
- **cli:** Build the starter image with the tags in .buildtags
- **vukem:** Warn at boot when TRUSTED_PROXIES trusts every address

### Fixed

- **orm:** Make CreateMany fail closed on unordered ids and pk-only models
- **z:** Revalidate Preprocess output and reject a nil func
- **BREAKING:** **z:** Honor FieldError path in Custom parser errors
- **session:** Expire store sessions by the manager clock
- **session:** Make the store clock race-free and test Prune with it
- **schedule:** Bound the wait for running tasks on shutdown
- **admin:** Match % and _ literally in list search
- **middleware:** Refuse wildcard hosts and unsafe schemes in CSPSources
- **vukem:** Refuse SessionBypass with Stateless and document bypass matching
- **social:** Record used oauth states in the cache store
- **BREAKING:** **jwt:** Require audience and issuer with a remote key set
- **social:** Stop reflecting the provider error parameter
- **BREAKING:** **api:** Bound query page and filter value lists
- **BREAKING:** **api:** Scope idempotency keys by the authenticated principal
- **auth:** Keep the session when the user lookup fails
- **auth:** Treat wrapped no-rows errors from providers as not found
- **BREAKING:** **jwt:** Detect shared key sets by interface and fail guards at boot
- **api:** Make idempotency scopes unambiguous across guards and prefixes
- **api:** Keep default query limits for non-positive values
- **vite:** Serve public files from the app while the dev server runs
- **cli:** Load htmx before the vite entry in starter layouts
- **cli:** Create the social user before returning it
- **cli:** Let the app's language catalog and English subjects back auth messages
- **session:** Never resurrect revoked sessions and harden legacy re-key
- **auth:** Namespace the session owner by guard
- **cli:** Flash recovery codes before revoking sessions on 2fa enable
- **BREAKING:** **queue:** Refuse delays on sync and dispatch after commit without TxPusher
- **cli:** Queue the password reset mail in the auth scaffold
- **database:** Keep Tx comparable
- **mail:** Keep Mailer comparable
- **database:** Isolate AfterCommit hook panics and reject foreign Tx
- **app:** Turn a WithJobsFunc panic into the Queue error
- **BREAKING:** **app:** Refuse clear-text queued mail jobs by default
- **vukem:** Type the readiness body and log write errors
- **BREAKING:** **signed,social:** Refuse signing keys shorter than 32 bytes
- **vukem:** Tolerate a missing APP_KEY only with an explicit development or test APP_ENV
- **cli:** Derive scaffold signing keys with app.DeriveKey
- **vukem:** Run at most one goroutine per health check
- **vukem:** Reject nil lazy stores and detect the memory cache through any option
- **upload:** Log presign response write errors
- **vukem:** Bracket IPv6 hosts in the listen address
- **web:** Return client IPs from headers in canonical form
- **middleware:** Require one X-Forwarded-Proto line and trusted proxies

### Security

- **api:** State that op.security does not authenticate the route

## [0.23.0] - 2026-10-17

### Added

- **webhook:** Add signed outgoing webhooks with retries and delivery log
- **postgres:** Add pgtest helper for per-test databases
- **vukem:** Add key:generate app command
- **imaging:** Add contrib/imaging for safe image thumbnails
- **ratelimit:** Add a fixed-window limiter over a shared counter
- **app:** Choose the rate limit store with RATE_LIMIT_STORE
- **cli:** Build scaffold rate limiters from RATE_LIMIT_STORE
- **app:** Prune expired database cache entries hourly
- **app:** Find the cache pruner through wrapped stores
- **site:** Add last-updated dates and a pt-BR locale

### Changed

- **cache:** Increment database counters in one statement
- **site:** Remove the docs version switcher

### Fixed

- **webhook:** Harden outgoing deliveries after review
- **postgres:** Make pgtest cleanup robust to reconnecting pools
- **vukem:** Send key:generate diagnostics to stderr
- **imaging:** Apply review findings to thumbnails
- **cache:** Make database Increment atomic across connections
- **ratelimit:** Hash every window key and validate limiter names
- **app:** Refuse RATE_LIMIT_STORE=cache over the memory cache

### Security

- **webhook:** Document outgoing webhooks and their security guarantees
- **app:** Check unknown RATE_LIMIT_STORE values as a security default
- **ratelimit:** Document shared rate limits and RATE_LIMIT_STORE

## [0.22.0] - 2026-10-17

### Added

- **auth:** Add owner-scoped Tokens.RevokeFor and Tokens.List
- **webhook:** Add Standard Webhooks verifier and event dedupe
- **inertia:** Share locale and translations with I18n middleware
- **upload:** Cap image dimensions to stop decompression bombs
- **auth:** Redirect unverified browsers to the verification notice
- **cli:** Detect the JavaScript package manager from the lockfile
- **cli:** Tailwind-only Inertia starters with class-based dark mode

### Fixed

- **queue:** Settle database jobs only while holding the reservation
- **cache:** Store database cache values as BYTEA on Postgres
- **session:** Do not create a session for read-only visits
- **vukemtest:** Send the app's asset version from Inertia("")
- **vukemtest:** Keep GetJSON's Accept header to that request
- **cli:** Use a real PNG header in the starter upload test
- **cli:** Embed starter resources when APP_ENV=production
- **cli:** Render the 2FA QR code as an image, not raw HTML

### Security

- **api:** Refuse idempotency keys with an empty caller scope
- **session:** Prune expired database sessions hourly
- **vukemtest:** Add AssertHeaderContains and AssertHeaderMissing

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

[Unreleased]: https://github.com/AlexGalhardo/vukem/compare/v0.26.0...HEAD
[0.26.0]: https://github.com/AlexGalhardo/vukem/compare/v0.25.0...v0.26.0
[0.25.0]: https://github.com/AlexGalhardo/vukem/compare/v0.24.0...v0.25.0
[0.24.0]: https://github.com/AlexGalhardo/vukem/compare/v0.23.0...v0.24.0
[0.23.0]: https://github.com/AlexGalhardo/vukem/compare/v0.22.0...v0.23.0
[0.22.0]: https://github.com/AlexGalhardo/vukem/compare/v0.21.1...v0.22.0
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
