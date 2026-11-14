# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **ci:** Upload the Ubuntu coverage profile to [Codecov](https://app.codecov.io/gh/AlexGalhardo/vukem).
- Viewport landing page with a Three.js scene, the Vukem launch film and a Read Docs action.
- **cli:** Refactor the generated personal-finance SaaS: green viewport landing, shadcn/ui sidebar, transaction modals, light/dark theme, English/pt-BR/es, Google sign-in, idempotent local admin and demo seeds, `vukem new --example finance --seed`, and generated Docker setup.

### Changed

- **site:** Rebuild the landing: copyable install commands in the hero (with the Finance scaffold), feature tabs with real Go samples that switch in place through the View Transitions API, and a native dialog for the launch film. No vertical scroll from 768px wide; below that the tabs become a swipeable chip row.
- **site:** Use one animated Go Gopher SVG (`site/src/assets/gopher.svg`) for the logo and the landing, with credit to Renee French (CC BY 4.0). The animation respects `prefers-reduced-motion`.
- **site:** Move the landing background sphere into the open left side. The code panel on the right was covering it.
- **site:** Simplify the landing. Drop the eyebrow, the sample command, the edition line and the footer. The subtitle names web applications and APIs, and the feature panel shows larger examples from the docs.

### Removed

- **site:** Remove the Three.js scene and the `three` dependency.

### Fixed

- **ci:** Keep the Ubuntu test job green when Codecov has not registered the repository. The coverage gate still fails the job. The same OIDC upload succeeds once the Codecov GitHub App includes this repository.

- **site:** Let a landing viewport that is 360px tall or shorter scroll, so the headline and Read Docs link can both be brought fully into view. Taller screens stay one viewport.

- **ci:** Retry the OpenTelemetry module download when the Go proxy drops the HTTP/2 stream, and point the reviewed finance secret-scan fingerprints at the commit that landed on main.

- **cli:** Keep the finance scaffold clear of the secret scan. The admin display name is assigned apart from the password hash, the local sample passwords contain `example`, and password-field labels stay allowlisted.

- **cli:** Accept that Windows does not report the Unix 0600 mode of the generated local admin credential file. Unix still requires 0600.

- **cli:** Preserve every month's finance chart totals by allocating the complete history before indexing it (ST-3).

- **cli:** Keep workspace settings props separate from the shared workspace switcher (ST-2).

- **cli:** Keep empty finance-dashboard chart collections as JSON arrays so a new workspace renders before its first transaction (ST-1).

## [0.43.0] - 2026-11-12

### Added

- **BREAKING:** **cli:** Move the project upgrade to vukem update and update Go and JS dependencies ([8b4ac79](https://github.com/AlexGalhardo/vukem/commit/8b4ac793c77405f097e6e45c71c01e8740fea3ee))
- **cli:** Add vukem upgrade to update the CLI itself ([830fccc](https://github.com/AlexGalhardo/vukem/commit/830fccca76e44f467ffd4c2ebd26b7380db43bbf))
- **cli:** Register upgrade as the CLI self-update ([be34f8c](https://github.com/AlexGalhardo/vukem/commit/be34f8c27d21bffd3463cdeb30bc3fbecc952685))
- **tools:** Add starterdeps to keep scaffolded dependencies on the latest stable release ([d419e35](https://github.com/AlexGalhardo/vukem/commit/d419e35107a188e7673ab894b1a36bc2688a7395))
- **cli:** Scaffold the to-do example app in vukem new ([6439ee8](https://github.com/AlexGalhardo/vukem/commit/6439ee896c88f31a81bf4249401b9b8c09e2f3fd))
- **tools:** Cover images, actions, package managers and docs in starterdeps ([ff4774e](https://github.com/AlexGalhardo/vukem/commit/ff4774e02532152507c657510255f665ab848c24))
- **tools:** Pin the toolchain image base digest and Bun checksums in starterdeps ([032c2d8](https://github.com/AlexGalhardo/vukem/commit/032c2d83c4da88fa1826b6db26fd8fef18ba81f2))
- **cli:** Scaffold the personal finance SaaS example ([06d52d9](https://github.com/AlexGalhardo/vukem/commit/06d52d948e8e7826b413558dafec586363b21fc7))

### Fixed

- **cli:** Never downgrade the CLI to an older latest release ([65fdd8d](https://github.com/AlexGalhardo/vukem/commit/65fdd8d2fe95da03edefc19324a816beeb10eb71))
- **cli:** Resolve both paths when vukem upgrade checks for a go install binary ([1d5909f](https://github.com/AlexGalhardo/vukem/commit/1d5909feb3c3064cc3819828768b117e99f4b91b))

### Security

- **tenancy:** Add row-level multi-tenancy middleware, context and ORM scope ([d930d79](https://github.com/AlexGalhardo/vukem/commit/d930d79a09cf3faaa5bb84f6c159ae743a4fe752))

## [0.42.0] - 2026-11-12

### Added

- **redis:** Add a shared presence store and run the conformance suites ([5980f29](https://github.com/AlexGalhardo/vukem/commit/5980f2980a0d7aaf3ad33fc2bee4a122a019e30e))
- **turso:** Add the experimental Turso driver ([7d88df1](https://github.com/AlexGalhardo/vukem/commit/7d88df10b6a0a7b7a4994c75c65ce888d516034d))
- **studio:** Add the vukem-studio module with loopback guard, session and binary ([cdc94a3](https://github.com/AlexGalhardo/vukem/commit/cdc94a3df6e91fa56cdaabca895f20597eadd8da))
- **cli:** Add the studio command ([b8be220](https://github.com/AlexGalhardo/vukem/commit/b8be22023aeb2bd7834cddc3bd6eaa20cfe82e17))
- **studio:** Add the MongoDB and Redis explorers with fixed filters and command lists ([a17e0ab](https://github.com/AlexGalhardo/vukem/commit/a17e0abc2331e769a8ed0c3854e90378d6e7b2be))
- **studio:** Refresh pages live, gate editing behind --write and export ([9cf1d99](https://github.com/AlexGalhardo/vukem/commit/9cf1d99158b90cf3bef5c8fcf768c51703e04737))
- **mysql:** Map duplicate entries to a unique violation by error number ([4ce03f7](https://github.com/AlexGalhardo/vukem/commit/4ce03f707092d7c98a04df712c8a9e384cf86522))
- **studio:** Add the SQL table browser and the read-only console ([e079656](https://github.com/AlexGalhardo/vukem/commit/e0796563c7abbd04f035d15e55c1912a288a9751))
- **examples:** Scale the api example rate limits with RATE_LIMIT_SCALE ([fe4c528](https://github.com/AlexGalhardo/vukem/commit/fe4c528975518a3352c0280e1a570323f3379000))
- **examples:** Add a task_flow bench scenario to the api example ([15f241e](https://github.com/AlexGalhardo/vukem/commit/15f241eb50c615a90171bcabcd74070a90d5591f))
- **studio:** Edit, insert and delete SQL rows with --write, plus export, row counts and incoming references ([9a0694b](https://github.com/AlexGalhardo/vukem/commit/9a0694b6dd35218add138f64ca9cd740876e805c))
- **studio:** Read the SQL catalog through schema.Inspect ([bdbbc62](https://github.com/AlexGalhardo/vukem/commit/bdbbc62d510c8f178b96d2a23164a76e0c7fffdd))

### Changed

- **studio:** Isolate the schema catalog behind an Introspector interface ([1a86ceb](https://github.com/AlexGalhardo/vukem/commit/1a86ceb8b0a4c2e8a6514c0e1bed1b918d4289b3))
- **websocket:** List presence members with Here and refuse on store errors ([9a9fcf1](https://github.com/AlexGalhardo/vukem/commit/9a9fcf124be866a9e3d7927faf9c4d574a9f82e2))

### Fixed

- **apicheck:** Bound the apidiff comparison so a module that loops never hangs CI ([6b5a857](https://github.com/AlexGalhardo/vukem/commit/6b5a857f64d70ca9a8e1ff95ca391b89bf7d2166))
- **apicheck:** Stop a looping comparison before the runner kills the step ([257d218](https://github.com/AlexGalhardo/vukem/commit/257d2181e51acbf44c769853e92443df42331e1b))
- **cli:** Pin contrib/stripe v0.5.3 in install:billing ([997bd2b](https://github.com/AlexGalhardo/vukem/commit/997bd2bff47542ca48a6f51d173c1f9c728b7df0))
- **engine:** Treat a process that is already exiting as killed on Windows ([d081c5e](https://github.com/AlexGalhardo/vukem/commit/d081c5e44db21a31c7aa8ccf03389cec147083b4))
- **studio:** Refuse anything but a read in the read-only SQL console ([907186c](https://github.com/AlexGalhardo/vukem/commit/907186cd824108e3df01dcc229c90b3f79405ea4))
- **studio:** Refuse functions with side effects in the read-only console ([9eaf995](https://github.com/AlexGalhardo/vukem/commit/9eaf9959076bb3390453a5762e562abe3f2dc8ea))
- **studio:** Treat a table name schema cannot inspect as not found ([1b90b8f](https://github.com/AlexGalhardo/vukem/commit/1b90b8f9601f7cf1cf58c3de94fc4e44c4ccfd3e))
- **studio:** Refuse console statements from a non-loopback peer ([819a2cd](https://github.com/AlexGalhardo/vukem/commit/819a2cd8e904a64b620c7ecf4c5e6c988916ce12))
- **studio:** Mark the session cookie Secure when it comes over TLS ([2da6546](https://github.com/AlexGalhardo/vukem/commit/2da654616e176cff73cba0a99afabf0075961dd2))
- **studio:** Match Redis command names in ASCII only ([6efdb26](https://github.com/AlexGalhardo/vukem/commit/6efdb266c6195d1428071a311615edca646c570f))
- **schema:** Read a SQLite column's declared size from its CREATE TABLE when the pragma drops it ([91d3ab0](https://github.com/AlexGalhardo/vukem/commit/91d3ab021da642e1e623e1548c8246d1cf3e1713))

## [0.41.0] - 2026-11-11

### Added

- **BREAKING:** **ssg:** Replace the output directory atomically ([c81751d](https://github.com/AlexGalhardo/vukem/commit/c81751dd07e263ec4bc2c8c783677cf286a0d83a))
- **release:** Tag changed drivers and contrib modules ([bdba789](https://github.com/AlexGalhardo/vukem/commit/bdba78966b2204ea25c47925ccdd07ac5d67f1c6))
- **cli:** Add stub:publish ([14dfc63](https://github.com/AlexGalhardo/vukem/commit/14dfc63ba69a0420d2cee5e2f0b7dd01e805a343))
- **cli:** Add upgrade and upgrade --check ([ce8eae9](https://github.com/AlexGalhardo/vukem/commit/ce8eae9544d0f7ee994c54754fa9605e28946b05))
- **database:** Add the db console command ([11ba57e](https://github.com/AlexGalhardo/vukem/commit/11ba57ec2111dfb8b6b0bc42ff0c39198d5b6a22))
- **BREAKING:** **config:** Treat a missing APP_ENV as production ([27d2b94](https://github.com/AlexGalhardo/vukem/commit/27d2b942a91683cb44dd9b367bb009ef474e1773))
- **router:** Pin the middleware order, refuse duplicate providers, enrich route:list ([ee74bdf](https://github.com/AlexGalhardo/vukem/commit/ee74bdf1c3119dba2b08d1a8798981f4c44ffd16))
- **cli:** Generate the config reference, suggest app commands, extend doctor ([c97efbc](https://github.com/AlexGalhardo/vukem/commit/c97efbca0d37c2b42a5bf7800ac5dda8a2e22e7e))
- **router:** Say how to fix route and boot configuration errors ([8c83c00](https://github.com/AlexGalhardo/vukem/commit/8c83c00ba3892acc208bd54ad89407b9f55545bb))
- **view:** Add typed views and the view:check command ([015b247](https://github.com/AlexGalhardo/vukem/commit/015b2477fcf0a73c7a78521dc37c667bce1e1086))
- **web:** Show the production problem response on the debug page ([110ad35](https://github.com/AlexGalhardo/vukem/commit/110ad3562944959bd4b29387df137bb35fae0284))
- **z:** Add Literal, Union, DiscriminatedUnion, Tuple, Lazy and struct combinators ([b42aca5](https://github.com/AlexGalhardo/vukem/commit/b42aca59ed10e88a6d3aa009a38de50a55704600))
- **broadcast:** Add PresenceStore and the sse and presence conformance suites ([1bb5551](https://github.com/AlexGalhardo/vukem/commit/1bb555168d5705ae632fd3d7a296dae176465232))
- **loadtest:** Add the load generator engine and the bench command ([158262c](https://github.com/AlexGalhardo/vukem/commit/158262c2af4ae5823a4afd5407e7040f08a23e8f))
- **z:** Add cross-field rules, string formats, codecs and brand ([7ea256e](https://github.com/AlexGalhardo/vukem/commit/7ea256e57d890be6ec9d896a0026c602c36b98bf))
- **orm:** Add joins, aggregates, subqueries, relation filters, nested loading, Upsert and Raw ([5def372](https://github.com/AlexGalhardo/vukem/commit/5def3728cdae0be3535499c6eced73c0686e56de))
- **web:** Add structured issues to validation problems and WithMessage to z rules ([d5dd544](https://github.com/AlexGalhardo/vukem/commit/d5dd5449f190ba87766079c7abaa36ff8910429b))
- **loadtest:** Add scenarios, stages, thresholds, baselines and bench:run ([95a92af](https://github.com/AlexGalhardo/vukem/commit/95a92afbe532357ac6369717cc7938c7ad25e550))
- **loadtest:** Add the read-only live dashboard and the HTML report ([58bfd82](https://github.com/AlexGalhardo/vukem/commit/58bfd82b7082bfbfb033fe69759e93d63a066b87))
- **orm:** Add savepoints, tx options, optimistic locking, typed columns and codegen:models ([c10771b](https://github.com/AlexGalhardo/vukem/commit/c10771bd5a3b8fee7b6bbfd3dbd42e859365ebb7))
- **BREAKING:** **openapi:** Read validation constraints from z ([952ef5f](https://github.com/AlexGalhardo/vukem/commit/952ef5fa57fb3986950f48c777e0504d294397d3))
- **schema:** Add Inspect, Rebuild and Column.Change, plus migrate:check, migrate:diff, db:pull ([8e3ff9a](https://github.com/AlexGalhardo/vukem/commit/8e3ff9a1827dec12b5ecdff1930cf7e0c42d5113))
- **database:** Let a driver open and close its own connection ([02882a2](https://github.com/AlexGalhardo/vukem/commit/02882a2b5881dd02dbee1f8eda535fd0b0288742))
- **site:** Serve a markdown twin of every docs page ([acecee2](https://github.com/AlexGalhardo/vukem/commit/acecee20247223bcbd747e3183b3c200cc26459c))
- **cli:** Ship typed views in the html/template starters ([d540d3d](https://github.com/AlexGalhardo/vukem/commit/d540d3df5fe81513ef352ccc035a0a5614b7ed36))
- **BREAKING:** **openapi:** Return { data, error, status } from the TypeScript client ([65bf3ce](https://github.com/AlexGalhardo/vukem/commit/65bf3cef68cebd644a54b1a313f5670ea265077a))
- **cli:** Add codegen command, -style, -tanstack and -check, and regenerate on serve ([77d7e72](https://github.com/AlexGalhardo/vukem/commit/77d7e7273e60f9adafb8230e4448cb4861c028c3))
- **inertia:** Add typed page components and openapi.TypeScriptTypes ([4ef4c39](https://github.com/AlexGalhardo/vukem/commit/4ef4c39714c2de2c6dabedb6d3cce62380e6a433))
- **codegen:** Add codegen:pages and type the saas example's Inertia props ([79e6121](https://github.com/AlexGalhardo/vukem/commit/79e61212cf7e90b1112eab664dba974886a89325))
- **web:** Map errors to responses by type and give every problem a default type ([ce12002](https://github.com/AlexGalhardo/vukem/commit/ce120022853d05dc6e9107df00e30c9fba5f2b18))
- **cli:** Ship typed Inertia pages and page-props.ts in the starters ([1c497a2](https://github.com/AlexGalhardo/vukem/commit/1c497a2dafdd0c7158e84b97331e07e0bb997199))
- **cli:** Regenerate the typed column file with the codegen command ([0add8bf](https://github.com/AlexGalhardo/vukem/commit/0add8bff12bcdb6836bccb619e645bf1970d8479))
- **cli:** Type the Inertia pages that the installers add ([5b11739](https://github.com/AlexGalhardo/vukem/commit/5b117391d5790cfa4dd92463775daec40055283c))

### Changed

- **inertia:** Build the i18n catalog lazily and keep idle SSR connections ([0bac1ec](https://github.com/AlexGalhardo/vukem/commit/0bac1ec4e4bc95deeeeb73824bd02765c48d2802))
- **broadcast:** Decode the published envelope once for all subscriptions ([42f2107](https://github.com/AlexGalhardo/vukem/commit/42f21071c543ae9c1fb4887bfbf210b09589b2f3))
- **queue:** Bound the queue:failed listing with a LIMIT (default 100) ([c098e3f](https://github.com/AlexGalhardo/vukem/commit/c098e3f698b3d90a8d2dc4214c2025893a52a2c5))
- **ratelimit:** Sweep a bounded batch of keys instead of the whole map ([632d7e0](https://github.com/AlexGalhardo/vukem/commit/632d7e04c04abed5ba982faa87688f38e25fdfa2))
- **site:** Load the search dialog on first open ([3c6bea5](https://github.com/AlexGalhardo/vukem/commit/3c6bea5e57a4518e1c7be8b9274b6c631b347a4c))
- **upload:** Share the file name sanitizer with disposition ([c920858](https://github.com/AlexGalhardo/vukem/commit/c92085821b7a811a45dc4a8a788b0efada52651a))
- Move the SSR commands and Main to their theme files, split graphql coercion ([1087387](https://github.com/AlexGalhardo/vukem/commit/1087387be7f70434f912c03b7c74f9264311341f))
- **BREAKING:** **z:** Make Slice and Map generic schemas and share the modifiers ([f639020](https://github.com/AlexGalhardo/vukem/commit/f63902061bd9da6757273b0d7d166f984eb5383c))
- **graphql:** Bound the page size multiplier with an explicit check ([b38ea5f](https://github.com/AlexGalhardo/vukem/commit/b38ea5f3ddb20f918a93fbf9c3951bc83b4bdb6b))
- **z:** Keep the parse context and Passthrough off the hot path ([45f9e4e](https://github.com/AlexGalhardo/vukem/commit/45f9e4e67bf7f44bb82a39afc4561fe1e3092d3d))

### Fixed

- **cli:** Pin contrib/stripe v0.5.2 in install:billing ([6b502d8](https://github.com/AlexGalhardo/vukem/commit/6b502d821f703dc0f24344c8134ce27aedd0640e))
- **drivers:** Keep redis ttl, shut down otel on failed boot, clean s3 url keys ([8761f0d](https://github.com/AlexGalhardo/vukem/commit/8761f0dcab1a7062a7ce9156486d7faa2637dfa4))
- **mongo:** Map duplicate key errors to ErrDuplicate with status 409 ([df6f770](https://github.com/AlexGalhardo/vukem/commit/df6f77072695d8f3ddcc9b8b8441a16ec434e761))
- **contrib:** Fix stripe event error, kill engine process tree on windows, align x/net ([b9ec27d](https://github.com/AlexGalhardo/vukem/commit/b9ec27d50b4ff7b84c12287901a7d14af74b419c))
- **devtools:** Bound SQL, bind arguments and mail subject per record ([47e53f5](https://github.com/AlexGalhardo/vukem/commit/47e53f5178bd68da4551c82bf0c093e0c5d83792))
- **config:** Expand chained .env references and fix mail, tus and make:mail edge cases ([c172173](https://github.com/AlexGalhardo/vukem/commit/c172173be4beeb30568d4b6c8caed4796500a653))
- **orm:** Bound pagination, session update, cache prefix and migration lock ([8a80470](https://github.com/AlexGalhardo/vukem/commit/8a80470a392641af490f3af29e8a1958aad47fd6))
- **web:** Keep read errors, order form keys and tighten cross-origin and webhook handling ([15e357b](https://github.com/AlexGalhardo/vukem/commit/15e357b3bd7d0bcce807e0e0282dcf717f4c687e))
- **boot:** Shut down started providers on failure and report hub subscribe errors ([2e76f9e](https://github.com/AlexGalhardo/vukem/commit/2e76f9e13f5ddaf957dab7f1e32a8ddb25628e08))
- **graphql:** Bound query complexity for negative costs and null page sizes ([d20332a](https://github.com/AlexGalhardo/vukem/commit/d20332a1b23295251166aa565217434e11bbc709))
- **graphql:** Mask scalar Parse errors, validate directive args, print string defaults ([ec94f2d](https://github.com/AlexGalhardo/vukem/commit/ec94f2d701b96b1a7dbc365957ec5a5e096fa06b))
- **graphql:** Run loader batches with the operation context ([f650711](https://github.com/AlexGalhardo/vukem/commit/f650711a5e596b8c9324049127418b306ba1af1d))
- **graphql:** Compare every pair of distinct fields when merging selections ([b7e90a7](https://github.com/AlexGalhardo/vukem/commit/b7e90a72d5852d9cf11a40abe6d89e051f80327e))
- **social:** Require https and a matching issuer in OIDC discovery ([ff91584](https://github.com/AlexGalhardo/vukem/commit/ff9158413f7209ae3ee6fdd1c9fa89837536f098))
- **auth:** Bound argon2id concurrency and tighten JWKS, typ and recovery codes ([c29b009](https://github.com/AlexGalhardo/vukem/commit/c29b009ed2bcb278c6991080935c985e96b45c41))
- **site:** Canonical for old versions, scroll padding, reduced motion ([9428726](https://github.com/AlexGalhardo/vukem/commit/9428726dd23b6693c3d11a03bb2311c3d46ca8e9))
- **httpclient:** Evict idle hosts, and share the GCRA math with ratelimit ([e5ee331](https://github.com/AlexGalhardo/vukem/commit/e5ee3319860e6e2e5e0f8dcfbaf354ab1d98b6ef))
- **middleware:** Trust no peer when TrustedProxies is empty ([d81beba](https://github.com/AlexGalhardo/vukem/commit/d81beba4deb8a2392919c45ccd378973e3714f38))
- **cli:** Keep the doctor APP_ENV check case-sensitive like the boot ([1565240](https://github.com/AlexGalhardo/vukem/commit/156524038c13be2c0ab728bb801a4371d1b63935))
- **BREAKING:** **orm:** Skip trashed rows in Update, add UpdateOnly, branch on dialect capabilities ([f1fabaf](https://github.com/AlexGalhardo/vukem/commit/f1fabafae9066944c2832abc93535843faf29047))
- **loadtest:** Mark the dashboard cookie Secure when it comes over TLS ([3b74189](https://github.com/AlexGalhardo/vukem/commit/3b741891eba33aeed4fd56606ff8b963779739fd))
- **mysql:** Refuse clientFoundRows=false in the DSN ([5f22c57](https://github.com/AlexGalhardo/vukem/commit/5f22c5710812dfcd7858d4a7facfe531368cd303))
- **openapi:** Keep route patterns and summaries inside the doc comment of a generated client ([6ae73fa](https://github.com/AlexGalhardo/vukem/commit/6ae73fae2be43b6b3b68b26482dff0fccd2739ac))
- **codegen:** Refuse a models package name that is not a Go identifier ([9a6f128](https://github.com/AlexGalhardo/vukem/commit/9a6f1285f7e7983bd890bf1605ad6d519a164479))
- **site:** Leave MDX comments out of the Markdown twin ([36914de](https://github.com/AlexGalhardo/vukem/commit/36914de54f096dc0bf1eb645cb2402d464c48309))
- **web:** Validate APP_PROBLEM_BASE_URL at boot ([e8985f9](https://github.com/AlexGalhardo/vukem/commit/e8985f90d6be68b164e04a13c6bc0560ca070297))
- **cli:** Run the serve codegen beside the app, with a timeout ([a66dc85](https://github.com/AlexGalhardo/vukem/commit/a66dc85a79c8e6591991c70887a98a61b0a1ad35))
- **codegen:** Keep the G703 nolint valid on golangci-lint 2.14 and 2.15 ([e15c3fe](https://github.com/AlexGalhardo/vukem/commit/e15c3fe989c0440ea0dcedbd88b229d5e7c54bcd))

## [0.40.0] - 2026-11-10

### Added

- **social:** Add `Social.Allow` to refuse a provider user before the account lookup, returning `social.ErrNotAllowed`, and `auth.Allow` to end the sessions of users it refuses ([e0204c2](https://github.com/AlexGalhardo/vukem/commit/e0204c2e5c488f2437197cd14fc6a477b36de9bf))
- **site:** Add a sticky release index to the changelog ([cbf8c5c](https://github.com/AlexGalhardo/vukem/commit/cbf8c5c81a2f0a1c201a3500cbcce3c181991243))
- **site:** Move the changelog release index to the left ([59fe881](https://github.com/AlexGalhardo/vukem/commit/59fe881d3f6f72875739065ae629a1cbae6a09af))
- **site:** Show a reading progress bar on docs pages ([dd36106](https://github.com/AlexGalhardo/vukem/commit/dd3610611f55e76eccb478dd4413fe4ef0b8b612))
- **site:** Replace the native scrollbars of the side columns with a slim one ([fe4dea9](https://github.com/AlexGalhardo/vukem/commit/fe4dea9edbc66f402d24604882629c7ac8a47ee8))
- **site:** Lay the changelog out like the docs, with the progress bar ([46a0a65](https://github.com/AlexGalhardo/vukem/commit/46a0a65eccfd8086222212b8f3ac6fbb50fbe19d))
- **site:** Link llms.txt as Agents in the docs header ([7aa0fa8](https://github.com/AlexGalhardo/vukem/commit/7aa0fa8a69b95b2128ab09b37ff21bee45db0af8))
- **site:** Drop the tagline from llms.txt and the default description ([16a2982](https://github.com/AlexGalhardo/vukem/commit/16a29821d9fef4aeac999429d34f3d08b11d6da8))
- **graphql:** Cap the size of stored persisted queries ([bd152d9](https://github.com/AlexGalhardo/vukem/commit/bd152d958a69319efbf32201e55b0e09a94a4fd4))
- **cli:** Limit login attempts per IP in the auth scaffolds ([917aa47](https://github.com/AlexGalhardo/vukem/commit/917aa47c717bb6e11e8de04914523c7c2dd444ed))
- **examples:** Limit login attempts per IP ([fd9f551](https://github.com/AlexGalhardo/vukem/commit/fd9f551d1866be98cf1b68ef435f455020c44517))
- **BREAKING:** **vukem:** Refuse SMTP_INSECURE in production ([aea851c](https://github.com/AlexGalhardo/vukem/commit/aea851c656c83687b7194d2567993dccf9b15723))
- **BREAKING:** **postgres:** Default remote connections to sslmode=verify-full ([b976972](https://github.com/AlexGalhardo/vukem/commit/b976972e9133ced9a192c4373ee7712285f86b88))
- **BREAKING:** **redis:** Refuse plaintext connections to remote hosts ([ec2dcd6](https://github.com/AlexGalhardo/vukem/commit/ec2dcd6051acd5559e0c378238434c793ba02df7))

### Changed

- **graphql:** Track token columns incrementally in the lexer ([fa5d148](https://github.com/AlexGalhardo/vukem/commit/fa5d148d45801b387b76c483e60b55b66a0f88f3))
- **orm:** Stop Exists at the first matching row ([1979541](https://github.com/AlexGalhardo/vukem/commit/1979541943db8401f04b079274aad234d4ac0f4a))
- **stripe:** Split flatten to drop its gocognit suppression ([ce2d21d](https://github.com/AlexGalhardo/vukem/commit/ce2d21d5347d77c02187a11718d9d0bb86f80f22))
- **api:** Split Query to drop its gocognit suppression ([4b02198](https://github.com/AlexGalhardo/vukem/commit/4b02198331068bf02ee0fba9357d970c8e486129))
- **api:** Split register to drop its gocognit suppression ([7daffb3](https://github.com/AlexGalhardo/vukem/commit/7daffb32238ead3318221aff900e3ad8b78cea13))
- **vukem:** Build the served OpenAPI document once ([d561d82](https://github.com/AlexGalhardo/vukem/commit/d561d82f8980f8f2d21a64ffe95bfed4ace67a2a))
- **router:** Probe allowed methods with one shallow request copy ([834cc54](https://github.com/AlexGalhardo/vukem/commit/834cc5429a9181499a18290cfba57c59f98b5015))
- **otel:** Split Middleware to drop its gocognit suppression ([aaf7b10](https://github.com/AlexGalhardo/vukem/commit/aaf7b10c5cdcae8173096c5f9625826d5a141d79))
- **api:** Split Idempotency to drop its gocognit suppression ([6a3b094](https://github.com/AlexGalhardo/vukem/commit/6a3b09468241fb4a2a05cdc81337b172f3874f80))
- **inertia:** Split resolveMap to drop its gocognit suppression ([1b7d769](https://github.com/AlexGalhardo/vukem/commit/1b7d76936552c1e7909885c60f157d76be2091a0))
- **auth:** Split Required to drop its gocognit suppression ([ba5d534](https://github.com/AlexGalhardo/vukem/commit/ba5d534b34eee2727e72634caee2e8ab91dfe0dc))
- **middleware:** Split Maintenance to drop its gocognit suppression ([24feee6](https://github.com/AlexGalhardo/vukem/commit/24feee64f4702e31f8c910046f62edda20f50df7))
- **openapi:** Split zodBase to drop its gocognit suppression ([56d13ea](https://github.com/AlexGalhardo/vukem/commit/56d13ea5571dda906f201b4ced1b7247e0a2fa00))
- **queue:** Split Work to drop its gocognit suppression ([5529435](https://github.com/AlexGalhardo/vukem/commit/552943583a2fb36096f2334b585c3282a892e8b1))
- **openapi:** Split Build to drop its gocognit suppression ([3c2c1fa](https://github.com/AlexGalhardo/vukem/commit/3c2c1fade3d1e8d0e8e1647eb0469bd1e4e6ac9d))
- **orm:** Split createMany to drop its gocognit suppression ([b76e44f](https://github.com/AlexGalhardo/vukem/commit/b76e44f915a4b92b1ba1afc4bfdfa76147227c37))
- **vite:** Split Tags to drop its gocognit suppression ([d00071f](https://github.com/AlexGalhardo/vukem/commit/d00071f2f9dfa737f1bbc74a88f833987c7e808b))
- **z:** Split SliceSchema.parseAny to drop its gocognit suppression ([b199906](https://github.com/AlexGalhardo/vukem/commit/b1999063dbef6abfdd8151e769f28335577e9e24))
- **cli:** Split installScaffold to drop its gocognit suppression ([b9c7075](https://github.com/AlexGalhardo/vukem/commit/b9c70750eae53a45d05b314b147b6c8e08bb7739))
- **totp:** Split qr.penalty to drop its gocognit suppression ([4793ea1](https://github.com/AlexGalhardo/vukem/commit/4793ea1f297c9dba67254dbc343cea19aacaf951))
- **app:** Split New to drop its gocognit suppression ([3175ef8](https://github.com/AlexGalhardo/vukem/commit/3175ef817a08f6e13e5fe3831dc0b9a6f1e4bf64))
- **cli:** Split installAuth to drop its gocognit suppression ([d0899cc](https://github.com/AlexGalhardo/vukem/commit/d0899ccfdb4ffe4a62d8d6a2610e5d661ec71d84))
- **cli:** Split newProject to drop its gocognit suppression ([5945b7a](https://github.com/AlexGalhardo/vukem/commit/5945b7a272d6cd77bdaa31cc874b50ef6b548f56))
- **apicheck:** Split renderMarkdown to drop its gocognit suppression ([7efac49](https://github.com/AlexGalhardo/vukem/commit/7efac4926e3432967bbad591a8829e9b3d2df891))
- **repo:** Ignore the local `vukem-feedback.md` queue ([735632f](https://github.com/AlexGalhardo/vukem/commit/735632f015b334110a489e617bb0d488b4a8d7c4))

### Fixed

- **api:** Store the idempotent response after the client disconnects ([be3c251](https://github.com/AlexGalhardo/vukem/commit/be3c251e730cac4148189d6e9787ac7c1f94920c))
- **webhook:** Remember a delivered event after the sender disconnects ([16d3e1d](https://github.com/AlexGalhardo/vukem/commit/16d3e1d01d853ac036605385802342cf5e1ee046))
- **api:** Release the idempotency key when the handler panics ([0f792da](https://github.com/AlexGalhardo/vukem/commit/0f792daf0ed3ad76d7aefcda8ee4de4eea454fdf))
- **webhook:** Release the dedupe key when the handler panics ([421123b](https://github.com/AlexGalhardo/vukem/commit/421123b98c3fee731feca01938796d8e09d3508d))
- **cli:** Bind every published port of the `vukem new` compose template and of `.config/compose.yaml` to `127.0.0.1`, so the dev Postgres and Mailpit are not reachable from the network ([37b9730](https://github.com/AlexGalhardo/vukem/commit/37b9730afaf86425e19b4c6276bde5aa1eecb94b))
- **auth:** Answer 401 only for guests in `auth.Allow`; a failing user lookup now returns its own error instead of signing the user out ([e5cd34b](https://github.com/AlexGalhardo/vukem/commit/e5cd34bb6db0ebe94ceddf95d7683843a8a17c60))
- **mail:** Fail a queued mail at once when the provider rejects it ([8783a55](https://github.com/AlexGalhardo/vukem/commit/8783a5526a4383fb4c6731f589142e779a70265f))
- **site:** Let the docs article use the width the TOC leaves free ([9baa93c](https://github.com/AlexGalhardo/vukem/commit/9baa93c076141305bd2a7ab9424afdeaed60b4ae))
- **site:** Highlight the active release in the changelog index ([3b0fd58](https://github.com/AlexGalhardo/vukem/commit/3b0fd58f6df3a10862b7a6e67b5a2c5809e47497))
- **site:** Link and canonicalize pages with the trailing slash they are served at ([cd92c80](https://github.com/AlexGalhardo/vukem/commit/cd92c806410aa9184efdd7129580dc6da551f93a))
- **site:** Keep the legacy /pt-br redirect on this origin ([8ba0d9d](https://github.com/AlexGalhardo/vukem/commit/8ba0d9d1f7fb4d2da324209c43ab84bb12c76110))
- **web:** Remove multipart temp files when the handler returns ([e8fe057](https://github.com/AlexGalhardo/vukem/commit/e8fe057228364159ec150ddec60c1d8c962a6590))
- **cli:** Keep the password reset token out of the logs in auth scaffolds ([5ae9045](https://github.com/AlexGalhardo/vukem/commit/5ae90457b739a5b811c0e7bb9506c197900897e1))
- **examples:** Mark the reset token as a secret route parameter ([8e99824](https://github.com/AlexGalhardo/vukem/commit/8e99824ef1f4cec6952c58fdac03fead2cf37fb6))
- **BREAKING:** **orm:** Refuse Limit, Offset and OrderBy on bulk Update and Delete ([1190aad](https://github.com/AlexGalhardo/vukem/commit/1190aadadd43a584fa137f42181da2c1a64b2c63))
- **cache:** Let Remember waiters recompute when the first caller is cancelled ([e60f4e5](https://github.com/AlexGalhardo/vukem/commit/e60f4e58ea0dcee8c7e7419c035c91aff25df7c1))
- **jwt:** Fetch the JWKS outside the lock and rate limit failed refreshes ([02f1dd7](https://github.com/AlexGalhardo/vukem/commit/02f1dd72b44d38ae0e5fba885810dda434fd400b))
- **cli:** Detect an exported or spaced APP_KEY in key:generate ([093efba](https://github.com/AlexGalhardo/vukem/commit/093efba905a8e26840fc21ec56834bb1a5fd863e))
- **cli:** Detect an exported or spaced APP_KEY in the app key:generate ([4288655](https://github.com/AlexGalhardo/vukem/commit/4288655584317c48a830f05740a4555b4ebe7e42))
- **web:** Hide wrapped validation fields on server errors ([87f18fc](https://github.com/AlexGalhardo/vukem/commit/87f18fcd43b8f6f0333d0ee93315409bac2b49cc))
- **jsonrpc:** Answer a wrapped validation error as internal when it is a 5xx ([3f3ae2d](https://github.com/AlexGalhardo/vukem/commit/3f3ae2d4ee196ede824d4d4100c304c5338947da))
- **auth:** End an expired impersonation without the remember-me fallback ([a917218](https://github.com/AlexGalhardo/vukem/commit/a9172182f63cf214b5e5e8ae86aac53743b6dabd))
- **middleware:** Let Gzip flush before the first write ([c8beb32](https://github.com/AlexGalhardo/vukem/commit/c8beb32cbe46eef7e2790550f6d0183873f4936d))
- **jwt:** Release JWKS waiters when a fetch panics or the caller leaves ([5588fa5](https://github.com/AlexGalhardo/vukem/commit/5588fa57872894df1574f9ed0a60262befd5f9af))
- **auth:** Keep the remember-me token when the user store is failing ([229b255](https://github.com/AlexGalhardo/vukem/commit/229b255536336c49af7dfc06591e6bb1b3d51c01))
- **csrf:** Remove multipart temp files when a middleware answers early ([3efda88](https://github.com/AlexGalhardo/vukem/commit/3efda88cbcd07f36890c3b5d53cfa15ef9d4cf18))
- **auth:** Keep the remember-me cookie when taking the token fails ([b9a28de](https://github.com/AlexGalhardo/vukem/commit/b9a28de8d3e00731038492434f8b9c3dfc0def3b))
- **auth:** Drop the actor remember-me token after impersonation starts ([8a5b230](https://github.com/AlexGalhardo/vukem/commit/8a5b23072ef983dee4088447a69d551e4971c500))
- **schedule:** Accept Every(24h) ([00fe074](https://github.com/AlexGalhardo/vukem/commit/00fe0744dc9f8ecaf4e9fd07921f327f0243caed))
- **BREAKING:** **schedule:** Combine stepped wildcard day fields with AND, as Vixie cron ([7189843](https://github.com/AlexGalhardo/vukem/commit/7189843131d0dc03e8bd579d3908ac6c73bb6472))
- **vukem:** Close open connections when the HTTP drain times out ([35f0aab](https://github.com/AlexGalhardo/vukem/commit/35f0aab4cb2372f6a06b900b18363942aac66fa5))
- **schedule:** Run fixed-time tasks exactly once across DST changes ([f85f81d](https://github.com/AlexGalhardo/vukem/commit/f85f81d81408fe65641f98433851af93b7775c6c))
- **graphql:** Recover panics from scalar and type callbacks per field ([7ab4cea](https://github.com/AlexGalhardo/vukem/commit/7ab4cea8d8471122bf4b4652401373d1d91fdb27))
- **graphql:** Recover panics in subscription operations ([b5f185f](https://github.com/AlexGalhardo/vukem/commit/b5f185fc13ec844a4368672585a53a13edb21f28))
- **sse:** End a topic's streams when its broker subscription drops ([8bd6c43](https://github.com/AlexGalhardo/vukem/commit/8bd6c438f6edf8a6c48f4be5db1ebdde181ab231))
- **vukem:** Share an in-flight health check between concurrent probes ([7d1dfa0](https://github.com/AlexGalhardo/vukem/commit/7d1dfa0500c75437b0c2f68b625c27edbbf0129d))
- **events:** Register async listeners under the lock Shutdown closes with ([3bb45b7](https://github.com/AlexGalhardo/vukem/commit/3bb45b7dcc37e218c535b37896956e6a25641a39))
- **ratelimit:** Default a zero window to one second instead of dividing by zero ([546dd9f](https://github.com/AlexGalhardo/vukem/commit/546dd9f41872d374355662111944e5144819e4dd))
- **redis:** Report the rate limit the script enforces and default a zero window ([7c59e95](https://github.com/AlexGalhardo/vukem/commit/7c59e95cdae83d09c0483f8ffb8c77a457eb9295))
- **graphql:** Store a persisted query only after it validates, and only on POST ([9793aea](https://github.com/AlexGalhardo/vukem/commit/9793aea53aab2f3d8fcca1fd6ce291496c1ce857))
- **middleware:** Serve index.html files through Static ([0588569](https://github.com/AlexGalhardo/vukem/commit/0588569bfe976e901d759adf3c2305f754fc4c6e))
- **graphql:** Skip a persisted document larger than the memory store budget ([5d7f5d0](https://github.com/AlexGalhardo/vukem/commit/5d7f5d0e5afdfbb53ed2a8f94b41439f8c5bee70))
- **web:** Keep a peeked JSON body when LimitBody runs again ([aea63ad](https://github.com/AlexGalhardo/vukem/commit/aea63ad625e7dc2a9b12a59faed5204a5bfbf81e))
- **orm:** Report ErrNotFound when Restore matches no trashed row ([31fc347](https://github.com/AlexGalhardo/vukem/commit/31fc347b89581d02324e300dff73e48f20c5f558))
- **i18n:** Pick the plural form with the rules of the message locale ([c4e12f5](https://github.com/AlexGalhardo/vukem/commit/c4e12f54f857f3769b03c83da83e466262dfce59))
- **z:** Check MultipleOf exactly for integers and with a tolerance for floats ([82e7eb2](https://github.com/AlexGalhardo/vukem/commit/82e7eb2776943d46e713f2b1e1a85b0dc2bba122))
- **orm:** Match many-to-many keys of types with their own column form ([9593206](https://github.com/AlexGalhardo/vukem/commit/95932067d33cc97167e9d487192c89ab70aa5c59))
- **graphql:** Decode WebSocket variables with exact numbers and refuse 2^63 ([946ebf7](https://github.com/AlexGalhardo/vukem/commit/946ebf7e31fc1519f910b64e70f57493310e18dc))
- **jwt:** Refuse tokens that carry a crit header ([64f966d](https://github.com/AlexGalhardo/vukem/commit/64f966d9fdbd18de86e22c111ed9f2d460d3ffbe))
- **examples:** Ask for the password before regenerating 2FA recovery codes in saas ([7e5a27d](https://github.com/AlexGalhardo/vukem/commit/7e5a27d7cca1c68ab543113060c508c0e2d0c1f0))
- **totp:** Refuse a period that is not whole seconds ([5ce733a](https://github.com/AlexGalhardo/vukem/commit/5ce733a8e95a2bdb98b46a29266f3000442e752c))
- **devtools:** Record request paths with secret parameters hidden ([2d9ea27](https://github.com/AlexGalhardo/vukem/commit/2d9ea27c1947a268f92b3443551c2d91f35343ae))
- **admin:** Redact hidden columns in the audit log ([4b865cd](https://github.com/AlexGalhardo/vukem/commit/4b865cda1774abfa3a271ca2c70569898c6f54dc))
- **BREAKING:** **social:** Answer 503 when the Allow hook fails instead of refusing ([e8d8466](https://github.com/AlexGalhardo/vukem/commit/e8d846691ac9c66802ae4cb29fa3b7930bf43eeb))
- **ssg:** Write the output through os.Root ([655f1c2](https://github.com/AlexGalhardo/vukem/commit/655f1c2f6759f17cbd8502c6e8c993f39a5225f2))
- **examples:** Keep author emails out of the blog's JSON responses ([6acb267](https://github.com/AlexGalhardo/vukem/commit/6acb26707b048e516208210c78e05ecf28bf7dc1))
- **admin:** List audit entries only for resources the reader may view ([362960d](https://github.com/AlexGalhardo/vukem/commit/362960ddb54b2453909467043b936f874c7ca841))
- **graphql:** Keep the introspection depth bound when the other limits are off ([8c4e68f](https://github.com/AlexGalhardo/vukem/commit/8c4e68f5cbb77c321c85014b05eb7cafd8f13b4a))
- **examples:** Require the write ability on the API example's write routes ([7d934dd](https://github.com/AlexGalhardo/vukem/commit/7d934ddd991533495271c4faec5776b876c382bf))
- **ssg:** Refuse output paths that collide on case-insensitive file systems ([d07849d](https://github.com/AlexGalhardo/vukem/commit/d07849dcacc0dcd11cc0513b5bb1a63df0911226))
- **examples:** Use the app's rate limiters in the API example ([f4f4298](https://github.com/AlexGalhardo/vukem/commit/f4f429877acbb5795052c4b4e772088176da77a4))
- **devtools:** Strip every string literal form from recorded SQL ([4daa265](https://github.com/AlexGalhardo/vukem/commit/4daa265a2103e0868227140b935eea792e63e745))
- **graphql:** Answer a subscription over plain HTTP with the request error status ([6a094b3](https://github.com/AlexGalhardo/vukem/commit/6a094b386669f4ce8bc55a1d4cf5adce58d02748))
- **admin:** Treat more column names as sensitive ([a941227](https://github.com/AlexGalhardo/vukem/commit/a941227edff3b76467cf067450e236b761d3d12a))
- **ssg:** Decode page paths once ([eed420e](https://github.com/AlexGalhardo/vukem/commit/eed420ed56516ee7ebaba46c85353860f9dba545))
- **examples:** Throttle and unify sign-up in the blog example ([ada6bb4](https://github.com/AlexGalhardo/vukem/commit/ada6bb4893262039fb4049079510d6084580b31b))
- **site:** Follow the system theme only while System is selected ([24c2d01](https://github.com/AlexGalhardo/vukem/commit/24c2d01ab9432ba203179e83d9b0ff9b9f281f4c))
- **imaging:** Apply the EXIF orientation of JPEGs before scaling ([f145824](https://github.com/AlexGalhardo/vukem/commit/f145824ca39481593e6ca8b9ac73bd5c3ac13e01))
- **site:** Check heading anchors in docs links ([2f60b36](https://github.com/AlexGalhardo/vukem/commit/2f60b36dee641b35ea98204692b91c2643ee4ae7))
- **site:** Keep the 404 page out of search indexes ([6f04f23](https://github.com/AlexGalhardo/vukem/commit/6f04f238469da94bd961a377cf794ea27df3154f))
- **admin:** Authorize and run actions on the record locked inside the transaction ([26a4d6b](https://github.com/AlexGalhardo/vukem/commit/26a4d6ba44bfb6238d275d2c0ef601db2efd1954))
- **site:** Retry loading the search index when the dialog reopens ([01dd3b3](https://github.com/AlexGalhardo/vukem/commit/01dd3b3200e3dbb140f22f54f0a5551c75683647))
- **admin:** Redact hidden and sensitive columns when reading the audit log ([59603dc](https://github.com/AlexGalhardo/vukem/commit/59603dc547641d392249a30fa07d642f083d611a))
- **ssg:** Refuse the temporary file name and the remaining Windows device names ([e91a433](https://github.com/AlexGalhardo/vukem/commit/e91a433b0193401964e7f5ac76abecbc84ba3881))
- **social:** Refuse OIDC ID tokens that carry a crit header ([f0d13fc](https://github.com/AlexGalhardo/vukem/commit/f0d13fcec1020ce0480cf2bf51c95f16170f223a))
- **totp:** Refuse empty text in QR and QRMatrix ([3df1693](https://github.com/AlexGalhardo/vukem/commit/3df1693fb3ab65d5a4dcb1279fea6e8761537eaf))
- **web:** Reuse the log attribute holder already in the context ([241331c](https://github.com/AlexGalhardo/vukem/commit/241331c337f984f7257de2b596e254572427d76e))
- **devtools:** Skip SQL comments and limit backslash escapes to E strings ([8703e59](https://github.com/AlexGalhardo/vukem/commit/8703e59cf0544a9a4e164400c371a5a2c2a3e858))
- **redis:** Settle only the reservation the worker still holds ([340cf6d](https://github.com/AlexGalhardo/vukem/commit/340cf6d909889bafb23d92c84f9cf0b00ddde16f))
- **cli:** Explain the Railway Postgres sslmode in the generated IaC file ([81fe29e](https://github.com/AlexGalhardo/vukem/commit/81fe29e18843ec9bdeeb61b0b6202dffdcb5e743))
- **site:** Read the locale switcher of the Inertia and localization docs with `c.Input()`, so JSON posts from React, Vue or Svelte pages no longer 404 ([9da0cc2](https://github.com/AlexGalhardo/vukem/commit/9da0cc2108cb2d9dc965e53d6005b7fc127fe305))

## [0.39.1] - 2026-11-07

### Fixed

- **site:** Date doc pages by author date so rebase merges keep them valid ([25891fd](https://github.com/AlexGalhardo/vukem/commit/25891fd5b90a8940143267216424a24f5a6a0b55))
- **mail:** Send Resend addresses as email or Name \<email>, never \<email> ([8281961](https://github.com/AlexGalhardo/vukem/commit/828196190edebde682870dc1fbbe603a6d6823fe))
- **repo:** Escape \< in changelog entries so commit subjects never render as inline HTML ([cb0165f](https://github.com/AlexGalhardo/vukem/commit/cb0165fbbc17fb9be8b51d1dff47dc7d4bcc5cd8))

## [0.39.0] - 2026-11-07

### Added

- **web:** Decode and encode HTTP JSON with encoding/json/v2 ([6739710](https://github.com/AlexGalhardo/vukem/commit/6739710618d4a3055b16eac3bd266d3e329f98d6))
- **mail:** Add custom headers and a List-Unsubscribe helper ([184f083](https://github.com/AlexGalhardo/vukem/commit/184f083e529fe5079e119cee73f2d93191a22535))
- **vukem:** Add per-route cross-origin support to DefaultMiddlewareWith ([5f54723](https://github.com/AlexGalhardo/vukem/commit/5f547235deee3ef407b6b05be9afe47773890015))
- **webhook:** Accept svix-* headers in Standard and StandardID ([5be98eb](https://github.com/AlexGalhardo/vukem/commit/5be98eb7e156ef1e00384653d6bcaacf427f9a7c))
- **schema:** Drop an index by its columns with DropIndexOn ([587b2a0](https://github.com/AlexGalhardo/vukem/commit/587b2a07d89e10ab22abde0d3dedef8c076c8fc8))
- **orm:** Add GroupCount and a WhereIn hint for the IN operator ([369a679](https://github.com/AlexGalhardo/vukem/commit/369a67946abb43dd77360dc18fe9289399d2d45b))
- **cache:** Add ForgetPrefix to forget a family of keys ([e62712c](https://github.com/AlexGalhardo/vukem/commit/e62712c073e7c4123582387b6434c78829b1fae4))
- **mail:** Add typed Resend events and a recipient suppression hook ([58ebe35](https://github.com/AlexGalhardo/vukem/commit/58ebe350f3ebd0278fde76d40add352cfe81dc19))
- **orm:** Add Incr for atomic column arithmetic in Query.Update ([960be62](https://github.com/AlexGalhardo/vukem/commit/960be624db93031c4d2b42f611e41e9e381b6f7a))
- **middleware:** Add a default Cache-Control for dynamic responses ([6120179](https://github.com/AlexGalhardo/vukem/commit/6120179e65f1332eec2d80e01d28b883828b0c5b))
- **social:** Validate the production callback URL at boot ([fae0435](https://github.com/AlexGalhardo/vukem/commit/fae043542c839a9c11f4404ed4779ed35f8fee5c))
- **database:** Add Tx.LockKey for lock-first per-user limits ([62d78f0](https://github.com/AlexGalhardo/vukem/commit/62d78f0da78cd40592f1806c65d421aa7fbca364))
- **jwt:** Add opt-in nonce check to VerifyOptions ([57a7d39](https://github.com/AlexGalhardo/vukem/commit/57a7d39e5a857819088ecb9514840bf87b912291))

### Fixed

- **site:** Stop the legacy path redirect from leaving the origin ([0d33342](https://github.com/AlexGalhardo/vukem/commit/0d3334299e18ba332831a55905713efa2985298e))
- **orm:** Bind every time.Time in UTC so SQLite window queries agree ([db9fff0](https://github.com/AlexGalhardo/vukem/commit/db9fff0c20f1cdd5374fd13e6e9d7cd97f47407c))
- **queue:** Name QUEUE_TABLE when a migration clashes with a queue table ([cf692d6](https://github.com/AlexGalhardo/vukem/commit/cf692d6fed4561e10436e8cbbc6b9e48a4868a8c))
- **boost:** Refuse side-effecting selects in database_query ([6dc55e8](https://github.com/AlexGalhardo/vukem/commit/6dc55e800f790a166ecc75cb1280d572e5bea0d2))
- **boost:** Match side-effect function names despite comments ([4c222a8](https://github.com/AlexGalhardo/vukem/commit/4c222a88132b4560fb18513987d7783cd52ab466))
- **vukem:** Ignore stale vite hot file in tests and warn when used ([a671a74](https://github.com/AlexGalhardo/vukem/commit/a671a74dd47d8cbac46151401c87ee50d6883631))
- **vukem:** Keep vite dev mode in tests while hiding the hot file ([03fbb91](https://github.com/AlexGalhardo/vukem/commit/03fbb91bf66f9fbcc91f267c138d795d59852129))

### Security

- **skills:** Vendor cloudflare security-audit skill ([232a811](https://github.com/AlexGalhardo/vukem/commit/232a8119f5e3572512c937664ffc4612c2ea1232))

## [0.38.0] - 2026-11-07

### Added

- **queue:** Add Unique dispatch and Jitter backoff ([00a28ae](https://github.com/AlexGalhardo/vukem/commit/00a28ae03383194d521212ee3877aeef8e036285))
- **notify:** Add a Telegram channel and document StripPathPrefix order ([90ec3d4](https://github.com/AlexGalhardo/vukem/commit/90ec3d4780ea2a04ecad6d677558324b677eb603))
- **admin:** Export Audit and ChangeFlag for apps that bring their own screens ([627b7ad](https://github.com/AlexGalhardo/vukem/commit/627b7ade90227752dcc47b62aed755e85cff78c5))
- **vukemtest:** Show the logged errors when a 5xx assertion fails ([c63ad41](https://github.com/AlexGalhardo/vukem/commit/c63ad415cac4891f35ecb1bc0f4488553136c944))
- **cli:** Add hash:make to print a password hash from env or stdin ([64e1c5e](https://github.com/AlexGalhardo/vukem/commit/64e1c5ed44f12df06a012ac8d25abdd4fbb100ba))
- **cli:** Add db:run, a transactional SQL script runner with a dry run ([d4c6094](https://github.com/AlexGalhardo/vukem/commit/d4c60940e2e2d6a7b043a2ac516f0fe37a84c373))
- **admin:** Answer JSON on resource list and detail routes ([b183f12](https://github.com/AlexGalhardo/vukem/commit/b183f12b985bf91dc5e974f2224c59aba7398146))
- **admin:** Answer JSON on the dashboard route ([9983cf3](https://github.com/AlexGalhardo/vukem/commit/9983cf34a9b012c8ac611cdcfc52cb901308f386))
- **cli:** Add install:admin -stack inertia with React admin pages ([f4dbf83](https://github.com/AlexGalhardo/vukem/commit/f4dbf83bf6d44d021449243158aaf5e2e80f277e))
- **queue:** Refuse Unique on drivers that cannot honour it ([fe996c2](https://github.com/AlexGalhardo/vukem/commit/fe996c24227f94ca803052427ee3215a233b516d))
- **admin:** Answer JSON on the audit log and flags pages ([6ceaf81](https://github.com/AlexGalhardo/vukem/commit/6ceaf810d30328501a16479effdb462719aafc55))
- **site:** Replace the Next.js docs site with Astro 7.4 ([638c635](https://github.com/AlexGalhardo/vukem/commit/638c635ce55db400b3e93ff89c9ff4bdcbba9d64))
- **cli:** Add key:generate --if-missing ([eefd086](https://github.com/AlexGalhardo/vukem/commit/eefd08672b14c29451c3e04d3b332821cf20a654))
- **cli:** Report project checks from vukem doctor ([939da18](https://github.com/AlexGalhardo/vukem/commit/939da185d7d0ecf4df5e65cef08ba2b28be254ff))
- **cli:** Let new replace only the assets image ([7f9be45](https://github.com/AlexGalhardo/vukem/commit/7f9be454dff3c4693035ad48f17a17395e44be1c))
- **database:** Add db:backup for a SQLite snapshot ([fe19ed9](https://github.com/AlexGalhardo/vukem/commit/fe19ed960412c01dc24d17bc074f159446a89896))
- **cli:** Audit the starter lockfile in CI ([c47d231](https://github.com/AlexGalhardo/vukem/commit/c47d2311ac5f7d3d12201d6035e1df9c76cb176f))
- **cli:** Point doctor at the starter upgrade page ([30cfa78](https://github.com/AlexGalhardo/vukem/commit/30cfa78c00be401c2aba426f274817add5b650b6))
- **cli:** Pin the bun starter to 1.4.4 ([5a156b3](https://github.com/AlexGalhardo/vukem/commit/5a156b3626328c3588d7049fff983045711f398f))
- **cli:** Add install:auth --social-only ([80906de](https://github.com/AlexGalhardo/vukem/commit/80906de4f1898ebf66aed7a1dfcafad565cba25d))
- **cli:** Publish starter release notes from the changelog ([c5b8c5a](https://github.com/AlexGalhardo/vukem/commit/c5b8c5a9a78987e68df3566bfa5f75d9e0122c89))
- **cli:** Publish the starter image to GHCR ([27dea70](https://github.com/AlexGalhardo/vukem/commit/27dea70e92a35d99e51947e53001b8f695c4f34e))

### Changed

- **vukem:** Merge root option, command and provider sources ([9b0b2b5](https://github.com/AlexGalhardo/vukem/commit/9b0b2b5ed89237a585c5dab8aea252760df3ae99))

### Fixed

- **queue:** Unique jobs no longer clash with failed rows or Retry ([44acf4e](https://github.com/AlexGalhardo/vukem/commit/44acf4e1e527c2fd39d775d934c016d1b22629b0))
- **queue:** Keep the Retry id-clash rollback for non-unique ids ([36fd37a](https://github.com/AlexGalhardo/vukem/commit/36fd37a57f723aba7ba9048b84ebea029bfbee8c))
- **cli:** Validate and encode the slug in the React admin scaffold ([c80bf2f](https://github.com/AlexGalhardo/vukem/commit/c80bf2fc51a0adbff102cb84b830391f9638bc7d))
- **cli:** Db:run refuses transaction control and MySQL DDL, docs state the limits ([3455adf](https://github.com/AlexGalhardo/vukem/commit/3455adf88c67d6436e1b75dcf7e8fdbe6ef6b547))
- **queue:** MySQL unique insert keeps real errors, Fake records a unique job once ([baf58eb](https://github.com/AlexGalhardo/vukem/commit/baf58ebac2ad6191f43d814b96efec2045fc46a0))
- **cli:** React admin scaffold links, generated test per stack, lighter nav ([fce8461](https://github.com/AlexGalhardo/vukem/commit/fce8461100287cfdf721f66aa1d769404d9b9912))
- **cli:** Write APP_KEY files atomically with mode 0600 ([a9aaa4d](https://github.com/AlexGalhardo/vukem/commit/a9aaa4d5ffadea442384bdbfd9d6d8240eb8b89c))
- **cli:** Keep markdownlint off local artifacts ([df7953a](https://github.com/AlexGalhardo/vukem/commit/df7953a19f5362be093a07a834d5a4ca18e6f64c))
- **cli:** Print secret findings without the value ([8eb7468](https://github.com/AlexGalhardo/vukem/commit/8eb7468345a2c58fb14dbe42f2f5b602c37e9e6e))
- **site:** Read last-updated.json from disk ([046729c](https://github.com/AlexGalhardo/vukem/commit/046729c4fcaa9acd5b365f88225fd5cf82648187))
- **cli:** Fail social boot when a provider has no secret ([019a3fb](https://github.com/AlexGalhardo/vukem/commit/019a3fbbbbff6621183a5a92b164a8461d1b3f8a))
- **cli:** Satisfy golangci-lint on the new commands ([aa2764c](https://github.com/AlexGalhardo/vukem/commit/aa2764c2a10ccbbb03457e5795f4485e60e834ca))
- **cli:** Boot route:list after social credentials exist ([1606aa3](https://github.com/AlexGalhardo/vukem/commit/1606aa35baa3702528b6a46656f7f5ae7791baf6))

### Security

- **cli:** Ask for best-practice configs and open source files in vukem new ([b3c21c2](https://github.com/AlexGalhardo/vukem/commit/b3c21c2c6b34290ebe7ec07974430ca7af5ec16d))

## [0.37.0] - 2026-11-02

### Added

- **admin:** `Panel.Clock()` applies the idle timeout and the session age cap to every admin request (VF-063)
- **cache:** `cache.Buffer` takes an optional `MaxKeys` cap (VF-064)
- **web:** `web.ClientIP` for plain middleware and the `security/crawlers` verified-crawler check (VF-065)
- **vukem:** `App.HealthHandler` keeps `/up` on a `Router.Host(...).Only()` group (VF-068)
- **router:** `Route.SecretParams` keeps secret path parameters out of the logs (VF-071)
- **inertia:** Document that a same-origin redirect with a `#fragment` stays an Inertia visit (VF-072)
- **middleware:** `SecureHeadersConfig.AddCSPSources` adds CSP sources without dropping the integrations' ones (VF-073)
- **config:** `vukem.WithEnvDefaults` sets defaults below the `.env` files (VF-074)
- **robots:** New `robots` package and the `NOINDEX` switch (VF-075)
- **apidocs:** Add `Config.Options` for extra Scalar options (VF-078) ([b8dc3ac](https://github.com/AlexGalhardo/vukem/commit/b8dc3ac0043a3e70d082c9ad126980b552f7d1d3))

### Fixed

- **s3:** Keep up to 100 idle connections per host (VF-077) ([b8dc3ac](https://github.com/AlexGalhardo/vukem/commit/b8dc3ac0043a3e70d082c9ad126980b552f7d1d3))
- **cli:** `TestStripeVersionIsLatestTag` fails instead of skipping without tags (VF-076)
- **cli:** Pin contrib/stripe v0.5.1 in install:billing (#136) ([0d1d3a1](https://github.com/AlexGalhardo/vukem/commit/0d1d3a1a5f168a676076038a9f87622747216a81))

### Security

- Close the second 2026-11-01 HexaScore feedback batch (#138) ([45c803e](https://github.com/AlexGalhardo/vukem/commit/45c803ead14e36e1f2f1185da76e816f2a373f88))

## [0.36.0] - 2026-11-01

### Added

- Fix the 2026-11-01 HexaScore feedback batch and add llms.txt support (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **llmstxt:** Serve `/llms.txt` and a size-capped `/llms-full.txt` from a `llmstxt.Doc` (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **router:** `Group.Only()` makes a host group the only thing its host serves (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **admin:** `Config.DenyStatus`, `Config.TwoFactorRedirect` and `Panel.TrackIdle()` (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **cache:** `cache.Buffer` batches hot-path counts into one periodic write (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **web:** `web.AddLogAttrs` adds attributes and the matched route to the request line (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **errs:** `HTTPError.WithType` and `WithTitle` set a 4xx problem's type and title (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **web:** `Services.ProblemDecorator` edits every problem+json response (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **export:** `export.EncodeNDJSON` writes NDJSON to any writer (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **mail:** `MAIL_REPLY_TO` sets a default Reply-To (#128) ([9cafa13](https://github.com/AlexGalhardo/vukem/commit/9cafa13387b75915995ac4e67b61f2db03a48c97))
- **analytics:** Add umami, ga4, search console, bing webmaster and indexnow (#130) ([343667b](https://github.com/AlexGalhardo/vukem/commit/343667b196243b1e590c32ad1fddf22d0109b324))
- **analytics:** Umami, GA4 with Consent Mode v2, and Search Console and Bing verification from env, off when unset (#130) ([343667b](https://github.com/AlexGalhardo/vukem/commit/343667b196243b1e590c32ad1fddf22d0109b324))
- **bingwmt:** Bing Webmaster Tools API client keyed by `BING_WEBMASTER_API_KEY` (#130) ([343667b](https://github.com/AlexGalhardo/vukem/commit/343667b196243b1e590c32ad1fddf22d0109b324))
- **indexnow:** Serve the `INDEXNOW_KEY` file and push changed URLs with `Client.Notify` and `indexnow.Job` (#130) ([343667b](https://github.com/AlexGalhardo/vukem/commit/343667b196243b1e590c32ad1fddf22d0109b324))
- **challenge:** Add native Cloudflare Turnstile support and step-up hook (#131) ([330866d](https://github.com/AlexGalhardo/vukem/commit/330866d306cd54047c6c944a27a6e8b2e36992ac))
- **challenge:** `TurnstileConfig` protects form routes and `StepUp` challenges suspicious clients with a signed clearance cookie (#131) ([330866d](https://github.com/AlexGalhardo/vukem/commit/330866d306cd54047c6c944a27a6e8b2e36992ac))
- **challenge:** `SiteVerify.InsecureFailOpen` opt-in, idempotent siteverify calls with one retry (#131) ([330866d](https://github.com/AlexGalhardo/vukem/commit/330866d306cd54047c6c944a27a6e8b2e36992ac))

### Fixed

- **web:** Reject SafeRedirect targets that http.Redirect cleans off-origin (#132) ([ea01c38](https://github.com/AlexGalhardo/vukem/commit/ea01c38114f55c262c209408d1cb1a4577330b04))
- **cli:** Pin contrib/stripe v0.5.0 in install:billing (#133) ([ef1f768](https://github.com/AlexGalhardo/vukem/commit/ef1f7683a23b87bad8a557366622e86485a6129f))

## [0.35.0] - 2026-11-01

### Added

- Add QR codes and static site generation (#102) ([6bc5576](https://github.com/AlexGalhardo/vukem/commit/6bc5576d6ebc0cf24408e41546e02e7bc5b6a967))
- **ratelimit:** Add NewMemoryBurst with a burst apart from the rate (#105) ([6b69a32](https://github.com/AlexGalhardo/vukem/commit/6b69a32346b6486580c060b3863a8e1ae050a27b))
- **pgtest:** Make the drop timeout configurable with PGTEST_DROP_TIMEOUT (#106) ([cb6a6f4](https://github.com/AlexGalhardo/vukem/commit/cb6a6f4aa97759df8790661a0626bae843158100))
- **inertia:** Render the error page for full page loads with ErrorFullPages (#107) ([8fbe0f2](https://github.com/AlexGalhardo/vukem/commit/8fbe0f2ac8cd1868a032bdef5c955cbdd2df76f1))
- **inertia:** Add ClientShell head and placeholder for client-rendered pages (#109) ([11761d2](https://github.com/AlexGalhardo/vukem/commit/11761d20fd8128d38450df178ffe76e6c27d8c40))
- **admin:** Add Panel.AddPage for custom pages inside the admin layout (#110) ([7fb2cec](https://github.com/AlexGalhardo/vukem/commit/7fb2cec48f1dcb1e8550910e3d858ee59d6b7803))
- **cli:** Add api:fuzz to run Schemathesis against the OpenAPI document (#113) ([7ddace8](https://github.com/AlexGalhardo/vukem/commit/7ddace87daae20c13f4008cbb82418149a6ae1b4))
- **cli:** Default APP_NAME to the project name with WithName (#115) ([01ca804](https://github.com/AlexGalhardo/vukem/commit/01ca804ae9785c2ce5575329e5f94989c09052a1))
- **inertia:** Add a data-hydrating hook to the scaffold root view (#116) ([c7b5efe](https://github.com/AlexGalhardo/vukem/commit/c7b5efe4668f4d1a70723f1cb5531559eb8f28ca))
- **openapi:** Add an enum tag that documents values without validating (#117) ([7f9b959](https://github.com/AlexGalhardo/vukem/commit/7f9b9593611d9956122bd477f1e3880d34d46212))
- **openapi:** Type code and extension members on the generated ApiProblem (#118) ([01023ab](https://github.com/AlexGalhardo/vukem/commit/01023abd190337202e2140526cb35a855df7ad72))
- **pgtest:** Sweep orphan databases left by killed runs (#122) ([e697a8a](https://github.com/AlexGalhardo/vukem/commit/e697a8a8a4a3e9a0304274e6b69903113637fbdb))
- **openapi:** Per-document Servers and StripPrefix via Info.Docs (#124) ([9ee09c0](https://github.com/AlexGalhardo/vukem/commit/9ee09c07aee9a0494999330c03dcdb3536066270))

### Fixed

- **otel:** Keep Railway OTLP export to traces only (#104) ([daf6d30](https://github.com/AlexGalhardo/vukem/commit/daf6d3028ef217daac721034e135d8a4bcd946a1))
- **site:** Tolerate squash merges in the last-updated check (#114) ([66234fb](https://github.com/AlexGalhardo/vukem/commit/66234fb4b862df88710be843da8994a65ba70324))
- **admin:** Lock the row and write only changed columns on update (#121) ([10c1c2a](https://github.com/AlexGalhardo/vukem/commit/10c1c2ad76754e6ea77fbb3d8ce9aa453849c26c))
- **schema:** Poll pg_try_advisory_lock instead of blocking on the migration lock (#123) ([53e76c1](https://github.com/AlexGalhardo/vukem/commit/53e76c1fac71c4e98964c89046129c32d19d28f0))

## [0.34.0] - 2026-10-30

### Added

- **stripe:** Add subscription and checkout session list calls (#36) ([be4299a](https://github.com/AlexGalhardo/vukem/commit/be4299a26090782e825820bbe3824ba493d05e0d))
- **orm:** Add orm:"readonly" for columns the database computes (#37) ([7840707](https://github.com/AlexGalhardo/vukem/commit/7840707ff824ab8fa2f74e937b4c55b503002dce))
- **schema:** Add Index.Using for PostgreSQL index methods (#38) ([8d16456](https://github.com/AlexGalhardo/vukem/commit/8d1645651a7d9cb93a8f9e225f8f1ab446388ec1))
- **admin:** Filter and search nullable sql.Null columns (#40) ([e428559](https://github.com/AlexGalhardo/vukem/commit/e4285591d9449c796fc7b85a607b5115254ec92b))
- **webauthn:** Verify passkey registration and authentication ceremonies (#43) ([4f09030](https://github.com/AlexGalhardo/vukem/commit/4f0903065f17b6c1c83d82666b84ead54886190b))
- **challenge:** Check the Turnstile action in SiteVerify (#47) ([1d889df](https://github.com/AlexGalhardo/vukem/commit/1d889df0be5003e25f1b0f0ced82dc1504a9c67a))
- **signed:** Sign and verify opaque tokens such as pagination cursors (#54) ([db1533c](https://github.com/AlexGalhardo/vukem/commit/db1533cd12954e0ee034f75efa1400638bb04d6b))
- **cloudflare:** Trust CF-Connecting-IP from Cloudflare ranges and lock the origin (#46) ([aba6483](https://github.com/AlexGalhardo/vukem/commit/aba6483d3d124804ef8e1a1284946dc88f0dbf11))
- **sitemap:** Stream XML sitemaps with hreflang alternates and an index (#56) ([9128044](https://github.com/AlexGalhardo/vukem/commit/91280441a0567a94bc89127b56aa131b43dc4cf2))
- **cli:** Add install:auth --passkeys passkey sign-in scaffold (#57) ([89505dc](https://github.com/AlexGalhardo/vukem/commit/89505dcc58b9d4a36088244e07c8eb9e918adbaa))
- **jsonrpc:** Serve JSON-RPC 2.0 methods as an http.Handler (#59) ([47f3e75](https://github.com/AlexGalhardo/vukem/commit/47f3e755a0d97d16e739174324f905d21915c2cd))
- **middleware:** Truncate client IPs in the request log on request (#53) ([f7a5d47](https://github.com/AlexGalhardo/vukem/commit/f7a5d479cb5f1ee2f9e0bb90eab13373606fd859))
- **mail:** Send React Email templates exported at build time (#58) ([b59cc09](https://github.com/AlexGalhardo/vukem/commit/b59cc09492e598f9b1c03ff4ca18d44003b0b7cf))
- **api:** Build list envelopes with the app's own type (#69) ([3724c6c](https://github.com/AlexGalhardo/vukem/commit/3724c6c97e34f0710782786a14e8a2b11825c38a))
- **totp:** Export HashRecoveryCode to import recovery codes (#70) ([4b0b67c](https://github.com/AlexGalhardo/vukem/commit/4b0b67c55e384319a07dd937e0b34ef45c8deddc))
- **vukem:** Serve HTTPS with HTTP/2 on the main server (#61) ([181915c](https://github.com/AlexGalhardo/vukem/commit/181915c908582f4e903927bb7baed66d3839a1d6))
- **jsonrpc:** Generate OpenRPC documents and add a typed Go client (#74) ([74a408d](https://github.com/AlexGalhardo/vukem/commit/74a408de6c150f7e862d18b78a45f8e6bd1ceb11))
- **openapi:** Rebase documented paths under a server prefix (#72) ([8e245a7](https://github.com/AlexGalhardo/vukem/commit/8e245a771ab753bd901aee9bbcb762488d0fcdd3))
- **httpclient:** Add per-host throttle, retry and circuit breaker transports (#76) ([6643afa](https://github.com/AlexGalhardo/vukem/commit/6643afa786697818bbd8719a5fd822307cd5bda4))
- **router:** Set an error handler per route group (#78) ([f43c5bf](https://github.com/AlexGalhardo/vukem/commit/f43c5bf083350c7e7144bcc18b001351258a14da))
- **cloudflare:** Expose visitor country, ray and bot headers from Cloudflare peers (#65) ([918c47c](https://github.com/AlexGalhardo/vukem/commit/918c47c8b1d38426da0a7b5afdf731286b383a3c))
- **http3:** Serve the app over HTTP/3 in an opt-in module (#87) ([d8b02c8](https://github.com/AlexGalhardo/vukem/commit/d8b02c8db2534e6c3ad71e862f678d6e049afb24))
- **openapi:** Opt-in required response fields and a typed-only document check (#82) ([71eead2](https://github.com/AlexGalhardo/vukem/commit/71eead28bb263bd8527a1fdfa34bc5b229c7cc26))
- **admin:** Address composite-key rows as a-b ids (#94) ([5fa5d1a](https://github.com/AlexGalhardo/vukem/commit/5fa5d1a50fd0e97739023669f0fa5e2c897e5ab0))
- **cloudflare:** Add cache helpers, a purge client and Access JWT checks (#97) ([cdff113](https://github.com/AlexGalhardo/vukem/commit/cdff11331aaea851eea131d5814519a05707c1a0))
- **admin:** Add idle timeout and a reusable panel guard (#95) ([2399deb](https://github.com/AlexGalhardo/vukem/commit/2399debb61f419fc44e1221a3aa41d92292a635f))

### Fixed

- **vukem:** Stop reporting a finished health check as busy (#42) ([5b7d969](https://github.com/AlexGalhardo/vukem/commit/5b7d969fb6d58ed297eb2abeb0ebbc292d8e8074))
- **schema:** Refuse to record a NoTransaction migration that left an INVALID index (#51) ([3912065](https://github.com/AlexGalhardo/vukem/commit/3912065acab95cc465c37ecc71b0853f1542e557))
- **api:** Send no body for a struct{} output with an explicit status (#55) ([60521d8](https://github.com/AlexGalhardo/vukem/commit/60521d8588a7287f106bed3eb9961880d5107f76))
- **cli:** Stop waiting for output pipes held by a tool's grandchildren (#79) ([68046fb](https://github.com/AlexGalhardo/vukem/commit/68046fbb2ca9e77ee5d805c2eec1e7e696ec5fa6))
- **api:** Let typed handlers finish the response themselves (#71) ([6ff017d](https://github.com/AlexGalhardo/vukem/commit/6ff017d73522557e8e76a9e6a39911667d97e4c8))
- **middleware:** Remember a body-limit error from MethodOverride (#93) ([5940cb1](https://github.com/AlexGalhardo/vukem/commit/5940cb1477522019ca6791e7dd05459992f2b6a2))
- **site:** Show Last updated on every docs page (#98) ([4d5e61f](https://github.com/AlexGalhardo/vukem/commit/4d5e61fb9cd0c3d906c8007ba793c9f0cb514ff5))

### Security

- **openapi:** Add Info.License, plain-route security and 4XX responses (#52) ([518f09b](https://github.com/AlexGalhardo/vukem/commit/518f09bf42ac773cd09f7760f20205ee3471d60d))
- **cli:** Scan generated projects for secrets with betterleaks (#64) ([ab4244f](https://github.com/AlexGalhardo/vukem/commit/ab4244f98a6f333e830f1b5dfa31b092322129f4))
- **notify:** Store in-app notifications and fan them out to mail and broadcast (#68) ([fd8a2d5](https://github.com/AlexGalhardo/vukem/commit/fd8a2d5923d149ebe3cd6874288071d2cb48f691))
- **auth:** Let an authorized user impersonate another for a bounded time (#75) ([36a7948](https://github.com/AlexGalhardo/vukem/commit/36a7948b689d37cbe8bd832a140364d854643184))

## [0.33.0] - 2026-10-29

### Added

- **inertia:** Add strict SSR that answers 503 instead of falling back (#7) ([674d6a7](https://github.com/AlexGalhardo/vukem/commit/674d6a722134f301b8f57b82fbfcf9799ec0c2de))
- **vukem:** Add serve -with-ssr, build -ssr and -ldflags, and WithVersion (#9) ([7608240](https://github.com/AlexGalhardo/vukem/commit/7608240e4ee0e79acbfe7f8a675edac8a04b5352))
- **i18n:** Add locale URL path prefixes with StripPathPrefix and LocalizePath (#10) ([620b51b](https://github.com/AlexGalhardo/vukem/commit/620b51b3964094714c928c7a9c40008e2961504e))
- **orm:** Page CursorPaginate by the query OrderBy with a composite keyset (#11) ([afbce68](https://github.com/AlexGalhardo/vukem/commit/afbce683d4ffa716c49fec78a258b99caa3dd4ee))
- **router:** Add Host groups for host-restricted routes (#12) ([71e9be2](https://github.com/AlexGalhardo/vukem/commit/71e9be20d52f0702d05460164eb375c9a3fbe8c1))
- **schema:** Add NoTransaction migrations and a cross-process migration lock (#13) ([b5a2253](https://github.com/AlexGalhardo/vukem/commit/b5a22532656df216bfe325110e5a580b4634d685))
- **queue:** Add per-queue running-job limits (WorkOptions.Limits, QUEUE_LIMITS) (#14) ([2b5ed18](https://github.com/AlexGalhardo/vukem/commit/2b5ed1880638163395903ae15ae7037ec9527750))
- **inertia:** Cache successful SSR renders (SSRConfig.Cache, CacheTTL, CacheWhen) (#16) ([d9aec2d](https://github.com/AlexGalhardo/vukem/commit/d9aec2d40c3f5b01e0298a13599d18e3625366f5))
- **cli:** Ship an Inertia SSR entry and keep vite build --ssr out of the public dir (#17) ([0a03f26](https://github.com/AlexGalhardo/vukem/commit/0a03f26227719ea5f78aff4eee370951f8d20334))
- **admin:** Add custom row and bulk actions with reasons and a JSON diff view (#22) ([5893fd2](https://github.com/AlexGalhardo/vukem/commit/5893fd26c1b78ddc7ad2a2afe9a413d8b81dc03f))
- **orm:** Re-seal encrypted columns after a key rotation (orm.Reseal, crypt:reseal) (#24) ([394d293](https://github.com/AlexGalhardo/vukem/commit/394d2934840bd1e9aae61f71068c0900c5671d7f))
- **challenge:** Add a bot challenge middleware with Turnstile and hCaptcha (#21) ([74bf415](https://github.com/AlexGalhardo/vukem/commit/74bf41511d8a7708e3fe3f0a8b81cee3397c20e7))
- **orm:** Add composite primary keys and schema Primary, SmallInteger and StoredAs (#25) ([fcb7631](https://github.com/AlexGalhardo/vukem/commit/fcb7631e55a4708cc7f4795c78dad6a603e0ee46))
- **cli:** Let install:billing name the plans and accept checkout currencies (#26) ([c12321b](https://github.com/AlexGalhardo/vukem/commit/c12321b61d41504eb5bb6b983190c86e5870f933))
- **admin:** Add AfterSave hooks with the transaction and virtual form fields (#27) ([3a174b7](https://github.com/AlexGalhardo/vukem/commit/3a174b7d4dbfeeec303d950934c1c9ac57e46737))
- **inertia:** Add request-bound root view data and keep the locale prefix in page URLs (#28) ([425a277](https://github.com/AlexGalhardo/vukem/commit/425a2776529e200ff27d3be74afae3fcdcfbd1c0))
- **storage:** Add HandlerWith cache-control and conditional requests for non-seekable files (#29) ([62f4c06](https://github.com/AlexGalhardo/vukem/commit/62f4c0662dfd6cfebe5bd1f9893b6bc1fdd1d51f))
- **cli:** Compress static files and pages in the browser starters (#30) ([dc660dd](https://github.com/AlexGalhardo/vukem/commit/dc660ddfd373861e40983267991f018379523bbe))
- **i18n:** Ship Spanish auth and framework message catalogs (#31) ([569ae7d](https://github.com/AlexGalhardo/vukem/commit/569ae7d772bb1a2242cf09260dbf2630ac4c145e))
- **export:** Add EncodeCSV for CSV files built outside a request (#32) ([a70802c](https://github.com/AlexGalhardo/vukem/commit/a70802c24b02b74227d238fd821c2a790156f911))

### Security

- **admin:** Add opt-in hardening (TwoFactor, MaxSessionAge, AllowCIDRs) (#20) ([7dbd6f9](https://github.com/AlexGalhardo/vukem/commit/7dbd6f99041ad8991991517b511d8ca00af08965))
- **errs:** Add problem+json extension members to HTTPError (#33) ([5def7be](https://github.com/AlexGalhardo/vukem/commit/5def7be0da93a5113aa3e8359051d62bd86b131c))

## [0.32.0] - 2026-10-25

### Added

- **cli:** Suggest the closest command for an unknown one ([28bcc36](https://github.com/AlexGalhardo/vukem/commit/28bcc36e846691ce460f364904f47f7638d4bd5a))

### Fixed

- **upload:** Close the two fuzz failures and save fuzz crashers ([67321b7](https://github.com/AlexGalhardo/vukem/commit/67321b7f07e63a1c6e9535c5c195aacfb818f120))
- **release:** Derive the PLAN.md next task when releasing ([70d3b6b](https://github.com/AlexGalhardo/vukem/commit/70d3b6b8e5707449c51c9acd67347271f8058f2d))
- **auth:** Bound login throttle keys and hash long memory limiter keys ([ae7063a](https://github.com/AlexGalhardo/vukem/commit/ae7063a90f420f01d9e7bf3e7056c3c8f515fe1e))
- **cache:** Keep held locks out of memory store LRU eviction ([5e44876](https://github.com/AlexGalhardo/vukem/commit/5e4487618ef506fa4373c0eafd8b53529eb4018f))
- **auth:** Keep the account dimension for oversized login emails ([301e0a5](https://github.com/AlexGalhardo/vukem/commit/301e0a5cd481774022ef41662f3c18591ea73b62))
- **session:** Keep more secret fields out of flashed old input ([b73964d](https://github.com/AlexGalhardo/vukem/commit/b73964d762c715b7948f7022e1ae9a61d8c9f462))
- **cache:** Cap held locks skipped by memory eviction ([052ba23](https://github.com/AlexGalhardo/vukem/commit/052ba233f995fdc9f67b8133658434c71a925d38))
- **session:** Match secret field names at camelCase boundaries ([338fd68](https://github.com/AlexGalhardo/vukem/commit/338fd68a39aae279226172fe2d9c26d1643ad83c))
- **ratelimit:** Keep hashed memory keys apart from raw ones ([b6bc8bf](https://github.com/AlexGalhardo/vukem/commit/b6bc8bfc4b8494fac5d02b1b9c24bf64a39dc13d))
- **cache:** Refuse new locks instead of evicting held ones ([9cb02b1](https://github.com/AlexGalhardo/vukem/commit/9cb02b1bdf503a9814037dff962c0cbc5b29949f))
- **examples:** Align the api example with the token scaffold ([0a80924](https://github.com/AlexGalhardo/vukem/commit/0a80924e07f941dd5abdb33d41e2563095662e67))
- **ratelimit:** Accept the bearer scheme in any case in ByToken ([2ca2b83](https://github.com/AlexGalhardo/vukem/commit/2ca2b831918e91e7624e8ee2ec40e5513301cb65))

### Security

- **security:** Support the latest v0 minor during the BETA ([75d7fac](https://github.com/AlexGalhardo/vukem/commit/75d7fac035a189e9be83d5383a633894f511fd65))
- **repo:** Keep site and repo docs out of the changelog security group ([da44c68](https://github.com/AlexGalhardo/vukem/commit/da44c68e8cceee7723c6cee9b261f4cb88c612b8))

## [0.31.0] - 2026-10-25

### Added

- **engine:** Add contrib/engine, a seam for running an external binary ([76ae3c8](https://github.com/AlexGalhardo/vukem/commit/76ae3c86cc4d6e45ded42c5e0e2718d22dd18db1))
- **cache:** Add Add and Pull generic helpers ([29e79e1](https://github.com/AlexGalhardo/vukem/commit/29e79e1464b9bedf99fbb73035b991fd43666c1a))
- **factory:** Add PickWeighted ([ea4da92](https://github.com/AlexGalhardo/vukem/commit/ea4da92b1fd2d555844c74818d07ee57511e83ce))
- **z:** Support map[string]V fields in FromTags ([efd1335](https://github.com/AlexGalhardo/vukem/commit/efd13350688d426b7866b2917aa7fc73ff762065))
- **z:** Add StructSchema.Normalize ([e6053e8](https://github.com/AlexGalhardo/vukem/commit/e6053e81b488a7a9119f10fac876d09bce5f1033))
- **z:** Add HasLower, HasUpper, HasDigit and HasSymbol string rules ([0e9791d](https://github.com/AlexGalhardo/vukem/commit/0e9791d438d5fe85b06e540f1fffdccdf47990f3))
- **auth:** Add ThrottleKeys for the login throttle ([8e6fe4d](https://github.com/AlexGalhardo/vukem/commit/8e6fe4dae2fea1f888ba14bbe3ca9cb8fb3527bd))
- **middleware:** Mark recovered panic records with panic=true ([f87d004](https://github.com/AlexGalhardo/vukem/commit/f87d00435c2c63ec5498d29eacb69737c299fa3a))
- **cli:** Add make:seeder --app for AppSeeders ([e8eee49](https://github.com/AlexGalhardo/vukem/commit/e8eee4920f3b2ec2ad5bde2f3b75e529b78b7fa2))
- **release:** Update PLAN status and bump nested module requires ([b698cc8](https://github.com/AlexGalhardo/vukem/commit/b698cc8a5a13f544c7c3495ecd7b039fb4e22238))
- **tools:** Add snippetcheck for the site docs Go blocks ([8b8a074](https://github.com/AlexGalhardo/vukem/commit/8b8a07456987feb1dd23ee567252f8b6248bf1c9))

### Changed

- **z:** Split tags.go by field kind ([fb9e023](https://github.com/AlexGalhardo/vukem/commit/fb9e02391bf2734c2ab00bab0e1f13f3e3f4153c))
- **auth:** Explain why redirect write errors are ignored ([9fa5205](https://github.com/AlexGalhardo/vukem/commit/9fa520588c989a0567f1403f4e323f24569f1a9f))
- **middleware:** Replace orDefault with cmp.Or ([4a24a2e](https://github.com/AlexGalhardo/vukem/commit/4a24a2ea25c6604a90a99bce0181ae4a9dd218e5))
- **repo:** Split root app.go by lifecycle and subsystem ([b5939e0](https://github.com/AlexGalhardo/vukem/commit/b5939e0ccb76ab66dc1c4fd54a20d0e5801fd70c))

### Fixed

- **z:** Reject omitempty on any, map[string]any and time tag fields ([08428a4](https://github.com/AlexGalhardo/vukem/commit/08428a4bc3e436f69788721fe257090f8d61f87e))
- **auth:** Read the throttle email like Ctx.Input and fail closed ([cd89ae6](https://github.com/AlexGalhardo/vukem/commit/cd89ae6ab3f9f105d9edd1866d89cbf27512c73b))
- **auth:** Key the login throttle on web.PeekJSON, like Ctx.Input ([fe69a79](https://github.com/AlexGalhardo/vukem/commit/fe69a7902135772d9ff3be4020ca684e66e5c769))
- **cli:** Align help text with behavior ([41a136d](https://github.com/AlexGalhardo/vukem/commit/41a136db3e413149bfae77bef855fa9ba911430b))

## [0.30.0] - 2026-10-19

### Added

- **cli:** Record the project's package manager in package.json ([c5b0b92](https://github.com/AlexGalhardo/vukem/commit/c5b0b92870abdaf7d39d89036efa8dcdecdc3bea))
- **z:** Describe a JSON payload whose shape is not fixed ([999d0e8](https://github.com/AlexGalhardo/vukem/commit/999d0e8af244f9648cf404f0ec7214ce1ce815d1))
- **vukem:** Give the operational seams their own names and checks ([131e904](https://github.com/AlexGalhardo/vukem/commit/131e904967a689a378c90ba46c272351aef23caa))
- **z:** Let the validate tag express an optional value and a default ([51fbbfc](https://github.com/AlexGalhardo/vukem/commit/51fbbfc38bb0c646ad7d4a5376d582033d1ea42c))
- **cpu:** Report the container's CPU quota for pools the runtime does not size ([073560f](https://github.com/AlexGalhardo/vukem/commit/073560fd3a04066ce2a22786449830df185cd5bd))
- **vukemtest:** Decode a response body into a struct ([9f218a9](https://github.com/AlexGalhardo/vukem/commit/9f218a9e14201603450643178f7c0cfccae058bd))

### Fixed

- **apidocs:** Run the accessibility pass in a microtask, not a frame ([6664021](https://github.com/AlexGalhardo/vukem/commit/66640213bb21397e4cb64e8e022f941c978d7796))
- **cli:** Make the make:* generators produce lint-clean code ([0977763](https://github.com/AlexGalhardo/vukem/commit/0977763d42dcccb4b525764c93b8df4479f94c52))
- **docker:** Make the lockfile an explicit build input of the assets stage ([f29c478](https://github.com/AlexGalhardo/vukem/commit/f29c4788cd0bfc4d58b9b5d4d652494f83dc72a9))
- **cli:** Gate install:auth --social and give its packages a doc.go ([856512c](https://github.com/AlexGalhardo/vukem/commit/856512c600f65048676c7024fd0bd45e31f2a18c))
- **upload:** Refuse a type the content detector cannot produce ([7912ed6](https://github.com/AlexGalhardo/vukem/commit/7912ed67529f88095cc8a5c6b1d13d3d57d4c68f))

### Security

- **cli:** Teach the API ability model the scaffold already enforces ([b8087dc](https://github.com/AlexGalhardo/vukem/commit/b8087dc5dfc6845722ccbf65f0fcbeac7da89922))
- **archive:** Read a user archive under a policy and return safe keys ([0d1e0c8](https://github.com/AlexGalhardo/vukem/commit/0d1e0c87c8ed836f6d1cf999ba6cd05df7fd50d3))
- **mediatype:** Name the types an app produces, not the ones it accepts ([933de25](https://github.com/AlexGalhardo/vukem/commit/933de25e3cfea4b235e1b2ff65cca608cba66970))

## [0.29.0] - 2026-10-18

### Added

- **site:** Serve sitemap.xml and robots.txt for search engines ([0b09352](https://github.com/AlexGalhardo/vukem/commit/0b093525435b23bc01ae1710f5c366705aa94a83))
- **cli:** Add -prefix to openapi:export and the codegen generators ([9062d20](https://github.com/AlexGalhardo/vukem/commit/9062d20f2fe76801c6f5a71cb69aace07b6d2156))
- **api:** Add RegisterWith for a caller-supplied input schema ([bbc07c4](https://github.com/AlexGalhardo/vukem/commit/bbc07c492c99d4e458b5d759f73448704738b2e2))
- **ulid:** Add a monotonic ULID generator with Valid and TimeOf ([f71f287](https://github.com/AlexGalhardo/vukem/commit/f71f2871131a03f8ca283d3c5c7bdf387a451b58))
- **auth:** Accept the token from extra headers, with Vary ([3604e4c](https://github.com/AlexGalhardo/vukem/commit/3604e4cadbd5a20c5123dfb35182f35ed13e194f))
- **queue:** Let a handler report a permanent failure and see its attempt ([6d90e21](https://github.com/AlexGalhardo/vukem/commit/6d90e2108d75da85d2683da9d08ac5c20fd87b00))
- **postgres:** Add an sse.Broker on LISTEN/NOTIFY ([f6ce304](https://github.com/AlexGalhardo/vukem/commit/f6ce30445d5f540ad94c5799c3fd82e9fb525745))
- **openapi:** Carry examples in schemas, parameters and media types ([cb9fff1](https://github.com/AlexGalhardo/vukem/commit/cb9fff130d082149c2039903ba8ae3261b15ae6e))
- **openapi:** Reject a path prefix that matches no route ([0ab4aaf](https://github.com/AlexGalhardo/vukem/commit/0ab4aafca6945bdd9451b6a2577e8610d4387054))
- **schema:** Expose a Schema's dialect and record a migration's SQL ([ad353df](https://github.com/AlexGalhardo/vukem/commit/ad353df62b29369ddf0a2872c403239f2e06058c))
- **queue:** Ship the queue tables as a registrable migration ([ddfe2fd](https://github.com/AlexGalhardo/vukem/commit/ddfe2fd82e31c5cf191b9857f3971b342fdd1906))
- **session:** Ship the sessions table as a registrable migration ([f5ae66b](https://github.com/AlexGalhardo/vukem/commit/f5ae66bffaba13ba173aa3a6346483df348c2ba3))
- **cache:** Ship the cache table as a registrable migration ([4430efb](https://github.com/AlexGalhardo/vukem/commit/4430efbab4a037972e0bb4a5010fa0e3454a9b5a))
- **webhook:** Ship the delivery log table as a registrable migration ([38fdf2c](https://github.com/AlexGalhardo/vukem/commit/38fdf2c9d407cca140bcae0ffe76136584f72c24))
- **schema:** Add stores.Migrations for the tables vukem owns ([0408641](https://github.com/AlexGalhardo/vukem/commit/0408641fdb3e59acb5f0b5e7463dcf12aedb6477))
- **ui:** Give the generated password fields a toggle and live rule checks ([e5b876c](https://github.com/AlexGalhardo/vukem/commit/e5b876c71c279da6e78e899d562daf528db5ad69))
- **cli:** Choose the auth message catalogs with install:auth --locales ([6ca182d](https://github.com/AlexGalhardo/vukem/commit/6ca182d73058d35064dad0592879f7d74b7221be))

### Changed

- **auth:** Read the listed token owner from its row ([da63a05](https://github.com/AlexGalhardo/vukem/commit/da63a052d666caf5391ccda5fe94d85a1765fd7c))
- **cli:** Keep starter package comments in a doc.go ([264fb46](https://github.com/AlexGalhardo/vukem/commit/264fb4632487316fc75463466b270a7c694a765c))

### Fixed

- **apidocs:** Give Scalar's caption labels and combobox field accessible markup ([082ab73](https://github.com/AlexGalhardo/vukem/commit/082ab7382fd2e5f266455aaa7ee651090376f6f5))
- **release:** Match changelog headings literally and refuse an empty body ([89a19bb](https://github.com/AlexGalhardo/vukem/commit/89a19bbfe76b5c92de6e3e335e37f205e244e821))
- **openapi:** Describe path parameters from their doc tag ([174cdb3](https://github.com/AlexGalhardo/vukem/commit/174cdb3b8d79eb822bfe34eb3fbd99af95125e45))
- **cli:** Pin install:billing to contrib/stripe v0.4.0 ([9b51f70](https://github.com/AlexGalhardo/vukem/commit/9b51f704e920778ac263d66dd729b52a9904ef7d))
- **cli:** Keep the demo resource's links inside the demo ([c3eb298](https://github.com/AlexGalhardo/vukem/commit/c3eb29863ee7f261fe569f45b607cf4606046e7c))
- **cli:** Generate the Dockerfile for the package manager the project uses ([3e3c92c](https://github.com/AlexGalhardo/vukem/commit/3e3c92cff2681ae4284bab05cb7a3ee11b180528))
- **docker:** Give the starter image a mailer so its commands can run ([04b9c2b](https://github.com/AlexGalhardo/vukem/commit/04b9c2b60a9a52d13145e8c34b51bd6ae6809d37))
- **cli:** Make a fresh scaffold pass the repository's golangci-lint ([d76e7b7](https://github.com/AlexGalhardo/vukem/commit/d76e7b7be278ff36eb5df8dffdf68c107ec40cf0))
- **cli:** Scope the API keys install:api mints instead of granting "*" ([7791277](https://github.com/AlexGalhardo/vukem/commit/77912775a7fff83c92e96d010279067d5e855f56))
- **cli:** Commit the generated git hooks with the executable bit ([be9ceea](https://github.com/AlexGalhardo/vukem/commit/be9ceea5bbf7f7298a7dbb0d8915d2822ca54756))

### Security

- **vukem:** Add WithOpenAPI so an app owns its document info ([697c8a0](https://github.com/AlexGalhardo/vukem/commit/697c8a0f24e6c9de1430d4186be96377d67af6d8))
- **cli:** Scaffold the project standards behind flags on vukem new ([68e6763](https://github.com/AlexGalhardo/vukem/commit/68e6763e72d36dc433a1d6608f39e460f756d82a))
- **cli:** Point a new project at the documentation it needs next ([bc09220](https://github.com/AlexGalhardo/vukem/commit/bc092204b4cf314400c11a8a449cd442893b6564))

## [0.28.0] - 2026-10-18

### Added

- **site:** Wrap code blocks and add Term and Quote components ([7328830](https://github.com/AlexGalhardo/vukem/commit/732883055ac6886d29ee8402dc99ee4bc2d1b0e7))
- **signed:** Add Signer.WithClock for the app clock ([b1fc8b0](https://github.com/AlexGalhardo/vukem/commit/b1fc8b05e87efe7f98c3680e706a354429703489))
- **api:** Eager-load includes and add a cursor-aware Request.CursorPaginate ([ab96f97](https://github.com/AlexGalhardo/vukem/commit/ab96f97a3383d5eaaade9692eade4d91d382da6d))
- **apidocs:** Configurable route names for several references ([751431c](https://github.com/AlexGalhardo/vukem/commit/751431c48669737249c59057f5b8bf0abdf38176))
- **vukem:** Add MiddlewareOptions.CSRFTokenFallback ([656bc11](https://github.com/AlexGalhardo/vukem/commit/656bc116e2e0e29f5d0e39d6ab6f4e59906016a6))
- **vukem:** Add WithViewFuncs for custom template functions ([f5d6d4f](https://github.com/AlexGalhardo/vukem/commit/f5d6d4f264d446b34cbf2b250921b1a48a16ad6e))
- **vukem:** Add WithEventsFake and WithRateLimiters options ([f3f7efc](https://github.com/AlexGalhardo/vukem/commit/f3f7efc13d29d9bd0eb5a791c76d89fbb830da2e))
- **BREAKING:** **config:** Resolve @file: secrets in the process environment ([ea204d2](https://github.com/AlexGalhardo/vukem/commit/ea204d2720f2a5d801a2e3f9427aacfb1158a155))
- **otel:** Read OTEL_SERVICE_VERSION into service.version ([5c32abe](https://github.com/AlexGalhardo/vukem/commit/5c32abe4a330033e2a56cb157dc871c854917a47))
- **config:** Escape a literal @file: value as @@file: ([46a7c04](https://github.com/AlexGalhardo/vukem/commit/46a7c042bdf1b28ccdfeeea50c36437f59a9d05f))

### Fixed

- **site:** Fit the header on 390px phones ([cbc14fe](https://github.com/AlexGalhardo/vukem/commit/cbc14fe6370334da7ec33006552f0411bab80356))
- **cli:** Ignore .env.local in new projects ([4a47421](https://github.com/AlexGalhardo/vukem/commit/4a4742175bb709fae738faf5b69472fa3d2d54ca))
- **site:** Fit code blocks and steps on phones ([d077dac](https://github.com/AlexGalhardo/vukem/commit/d077daced6c57a440141988f08f9926ceed31fd8))
- **authz:** Let T values use the policy registered for *T ([242e495](https://github.com/AlexGalhardo/vukem/commit/242e495ea2eba45cb7f48c4b35fdd89bc7322b38))
- **inertia:** Name the page script after Config.RootID ([6c57955](https://github.com/AlexGalhardo/vukem/commit/6c579559183b711a941bda8a535455b63c8ba64c))
- **inertia:** Keep X-Inertia-Location on https behind a trusted proxy ([a43acd6](https://github.com/AlexGalhardo/vukem/commit/a43acd620ec1b02ce715ce2068a0cb42166d8e30))
- **app:** Warn about a missing Inertia error page without WithEnv ([e8b6990](https://github.com/AlexGalhardo/vukem/commit/e8b6990cb701ab7736be04f1ff14315c6bfe9de6))
- **authz:** Document the shallow copy for value subjects and test fallback edges ([a0ab556](https://github.com/AlexGalhardo/vukem/commit/a0ab556cd822daf0c9d50f1c63484330093d552f))
- **schema:** Pluralise Constrained() targets like the orm ([0605a43](https://github.com/AlexGalhardo/vukem/commit/0605a43d2d4aec4f16f88856a24a043b3d7f849d))
- **schema:** Rebind ? placeholders in Raw for the dialect ([43c7aa0](https://github.com/AlexGalhardo/vukem/commit/43c7aa0321d8ab05115ee93fb1dfb19dd0f71090))
- **orm:** Name the Field() name in built-in rule messages ([46745cd](https://github.com/AlexGalhardo/vukem/commit/46745cdf576eb8b446b50eaf36f2207c0e220c38))
- **BREAKING:** **orm:** Read Chunk batches by keyset instead of OFFSET ([7e8b6ce](https://github.com/AlexGalhardo/vukem/commit/7e8b6ceb4d1988b96305df012684c9e4371264b6))
- **BREAKING:** **orm:** Cap Paginate and CursorPaginate per page at 100 ([9f0bedf](https://github.com/AlexGalhardo/vukem/commit/9f0bedf0b8428b3713d24906a0a69b18061cdaf6))
- **mongo:** Answer 404 for ErrNotFound and return [] from All ([2f02c82](https://github.com/AlexGalhardo/vukem/commit/2f02c82f24b5c6b9a4704fedb39e830f68fbd67b))
- **orm:** Group OR filters in keyset Chunk and check the selected key up front ([56659b5](https://github.com/AlexGalhardo/vukem/commit/56659b5289ebef64ea9ebb365197eb83dc542117))
- **auth:** Match the Bearer scheme case-insensitively ([a7f1ed1](https://github.com/AlexGalhardo/vukem/commit/a7f1ed10647765d8176ea3629aad94871624f0eb))
- **jwt:** Answer key set and user store outages with 503 ([2d5e1b4](https://github.com/AlexGalhardo/vukem/commit/2d5e1b459796087e7561f788c5811c05985985f0))
- **cli:** Revoke API tokens instead of deleting them on email proof ([e75382d](https://github.com/AlexGalhardo/vukem/commit/e75382de917a75b3f3c08adcd2cae92e0997ca5b))
- **cli:** Require the password and a rate limit to regenerate 2fa recovery codes ([4d78c0a](https://github.com/AlexGalhardo/vukem/commit/4d78c0a79ba7a3ea259287eeeec23e386b7a5687))
- **BREAKING:** **cli:** Refuse install:auth --social x ([694b6ae](https://github.com/AlexGalhardo/vukem/commit/694b6ae730db4104a1e2a989c5a3c49a3c143b38))
- **upload:** Accept Verify keys for types outside the extension table ([7658d96](https://github.com/AlexGalhardo/vukem/commit/7658d96b0b38dccdc4f649cdb1c7ddedb3892231))
- **storage:** Send the real Last-Modified and hide local temporary files ([d031b68](https://github.com/AlexGalhardo/vukem/commit/d031b68854a69b6ca81324af832af102f93afdf0))
- **BREAKING:** **storage:** Require UploadRules.Types in PutUploaded ([d30d7aa](https://github.com/AlexGalhardo/vukem/commit/d30d7aa7aa5be28743392949b6cf46e8482976e0))
- **schedule:** Log lock errors and measure took from the task start ([fb039f7](https://github.com/AlexGalhardo/vukem/commit/fb039f78651a215ef7ef880803764a8e0731b11b))
- **upload:** Keep scanner causes and log tus completion failures ([709123d](https://github.com/AlexGalhardo/vukem/commit/709123db21ee8c983b082bbf4462e3fa29e2ec81))
- **events:** Name listeners by function and recover sync panics ([eeb50d5](https://github.com/AlexGalhardo/vukem/commit/eeb50d5ad9ec60625384b6a1beef0e9579622808))
- **vukem:** Explain queue commands on in-process queue drivers ([ff78253](https://github.com/AlexGalhardo/vukem/commit/ff782534c257e6e8741eddff820a44c828103f26))
- **BREAKING:** **mail:** Validate tags for every transport ([938c891](https://github.com/AlexGalhardo/vukem/commit/938c891e82d8d5ff44eb4123a68965d6ae579634))
- **mail:** Retry network errors in the Resend and Mailtrap transports ([78da6ed](https://github.com/AlexGalhardo/vukem/commit/78da6ede6ceb2ce0bea1bfdfad91e069d082a2a8))
- **BREAKING:** **vukem:** Fail boot on a mail transport that cannot send ([a74ee8d](https://github.com/AlexGalhardo/vukem/commit/a74ee8df9283ede7df01f9e59a7093796521019e))
- **events:** Re-panic http.ErrAbortHandler and wrap error panics from sync listeners ([01ee89d](https://github.com/AlexGalhardo/vukem/commit/01ee89de3f457d6ddc79f0cc24adc322b780137c))
- **api:** Parse typed handler input with the request context and logger ([0958e16](https://github.com/AlexGalhardo/vukem/commit/0958e16a119deba1b2bbb8459d234f50591f915f))
- **BREAKING:** **api:** Replay only representation headers on idempotent repeats ([1925d67](https://github.com/AlexGalhardo/vukem/commit/1925d67ce25830ac5d09b52f066e38422953746b))
- **cli:** Create the output directory of codegen:types -o ([eee86eb](https://github.com/AlexGalhardo/vukem/commit/eee86ebab6aeb904caa018956ab1130ec5955873))
- **graphql:** Cost page-size arguments by their SDL default when omitted ([903c041](https://github.com/AlexGalhardo/vukem/commit/903c041c172a9db7d7a7544c3716689b39a72bc1))
- **BREAKING:** **graphql:** Reject introspection in Handler unless opted in ([03d0326](https://github.com/AlexGalhardo/vukem/commit/03d032646f9dc8da289baaa573a7a8681de6c068))
- **BREAKING:** **graphql:** Validate nested inputs, mask Validate errors, check fields at build ([3625e30](https://github.com/AlexGalhardo/vukem/commit/3625e302f695ea6fc14d662d91b0261de86a70c1))
- **openapi:** Reject duplicate operationIds ([beb0ac6](https://github.com/AlexGalhardo/vukem/commit/beb0ac63ebc33545e6d21347d02ac1839ca3dc53))
- **api:** Never replay credential headers on idempotent repeats ([4282e95](https://github.com/AlexGalhardo/vukem/commit/4282e9541393e5e2795e26e7867a090b78cf0371))
- **BREAKING:** **middleware:** Let a route raise the router-wide body limit ([bd64d69](https://github.com/AlexGalhardo/vukem/commit/bd64d69fceacbd04e3c98a738ab5132c33aa25f0))
- **web:** Send the c.Status code with the next writer's headers ([daefe5c](https://github.com/AlexGalhardo/vukem/commit/daefe5c5867cb83204af5cf47fcb6000ac4b5c5b))
- **BREAKING:** **web:** Reject unknown JSON fields in BindJSON as documented ([1387d1b](https://github.com/AlexGalhardo/vukem/commit/1387d1baf6a82d7c89cf10ef2b8fc9a590feb603))
- **web:** Answer 422 when FormFile reads a non-multipart request ([c6243d2](https://github.com/AlexGalhardo/vukem/commit/c6243d2995e2254846e599e2d8611350bafbb582))
- **BREAKING:** **router:** Make a group's "/" route exact like the root route ([e6f8e5c](https://github.com/AlexGalhardo/vukem/commit/e6f8e5cc2e8258c1cb5f122144c8ece6a9342d67))
- **router:** Strip inlined-closure suffixes from middleware names ([46edb46](https://github.com/AlexGalhardo/vukem/commit/46edb46e2d25b25d0ea4a624b86d9385e619d2be))
- **middleware:** Detect fingerprints strictly and add ETags to embedded files ([d11e4dd](https://github.com/AlexGalhardo/vukem/commit/d11e4ddf93a76b58605b7a38ade494b0eb3f6463))
- **csrf:** Trigger the token fallback on the session cookie only ([b24352c](https://github.com/AlexGalhardo/vukem/commit/b24352c638fae03a63d787fb9ba9437afd549231))
- **web:** Pass the error to error views in debug mode ([067a370](https://github.com/AlexGalhardo/vukem/commit/067a3705c039275b6d91013c1a096328e1c7e626))
- **session:** Report no id for cookie sessions, even after Regenerate ([4bbb51e](https://github.com/AlexGalhardo/vukem/commit/4bbb51e3432909a92d50cd11bcfea5bca58c6e7f))
- **z:** Use the singular in default messages for a count of 1 ([a1b5717](https://github.com/AlexGalhardo/vukem/commit/a1b5717cf65cadca04ec42374a61a937486d187c))
- **z:** Leave Nullable-only keys out of the JSON Schema required list ([004e63f](https://github.com/AlexGalhardo/vukem/commit/004e63f5e33f5223b2fe31b379f543599a197012))
- **middleware:** Keep an earlier body limit in MethodOverride ([ab1b31b](https://github.com/AlexGalhardo/vukem/commit/ab1b31b38b118bf36ca5f928e98e858d84cd9cdf))
- **web:** Apply a pending status only to 200 and commit it on Flush ([ff0e6fb](https://github.com/AlexGalhardo/vukem/commit/ff0e6fb33168165a90b1d078d3acccc8e8004463))
- **BREAKING:** **view:** Refuse custom template functions that replace built-ins ([ed9a6df](https://github.com/AlexGalhardo/vukem/commit/ed9a6df23c8872e1866df82fa0c428a2ed51c3d3))
- **BREAKING:** **telegram:** Honour info min level, never nil handler, html-safe send truncation ([9f8715c](https://github.com/AlexGalhardo/vukem/commit/9f8715cad95cad44f3f6cff5927b1cfecde1b8b2))
- **broadcast:** Reject channels with an empty dot segment ([0c30e19](https://github.com/AlexGalhardo/vukem/commit/0c30e19eb2b2f85f35432c3cb3c174a2fc5a9bac))
- **BREAKING:** **webhook:** Namespace dedupe keys per provider or route ([e722635](https://github.com/AlexGalhardo/vukem/commit/e72263546273db15c36418265c5aee0dc21ca500))
- **webhook:** Record deliveries dropped for a disabled endpoint ([a177d48](https://github.com/AlexGalhardo/vukem/commit/a177d48bd128a0a15378e252beb03ff2f3b968ac))
- **BREAKING:** **stripe:** Form-encode every map and slice type, refuse the rest ([9958167](https://github.com/AlexGalhardo/vukem/commit/995816748428ea7c18c33d5d9baab89ccc6350f9))
- **cli:** Pin install:billing to the latest contrib/stripe tag ([79ef0c4](https://github.com/AlexGalhardo/vukem/commit/79ef0c40bc641d5b17058e07569311665659800d))
- **BREAKING:** **i18n:** Refuse plural objects without other, explain bad catalog names ([be0512e](https://github.com/AlexGalhardo/vukem/commit/be0512e02ec90b95aa310ce48861a672631ee16d))
- **devtools:** Treat X-Forwarded-Proto, X-Forwarded-Port and Via as proxied ([b10eb56](https://github.com/AlexGalhardo/vukem/commit/b10eb568ed2bbf656fa70d3e337594d8983ed68a))
- **flags:** Stop repeating the flags prefix on invalid values ([a15d214](https://github.com/AlexGalhardo/vukem/commit/a15d214ae74f6f4977764c98f7c2372038622135))
- **admin:** Mount flag routes only for registered flags, skip no-op audits ([da22e46](https://github.com/AlexGalhardo/vukem/commit/da22e4683c013d59091aa1fdf97fde0b8765cde1))
- **admin:** Match short secret words in column names as whole segments ([f0e20df](https://github.com/AlexGalhardo/vukem/commit/f0e20df8f3b8ecf0d45b1c8e0586e22e7ef52ef7))
- **admin:** Match secret words anywhere, exempt only known counters ([2d8d4c2](https://github.com/AlexGalhardo/vukem/commit/2d8d4c2368b8530da550df2df9c40b140b47420e))
- **webhook:** Never namespace dedupe keys by the raw request path ([d477a32](https://github.com/AlexGalhardo/vukem/commit/d477a3237ca940cad6165843bff946cdb68af125))
- **telegram:** Count UTF-16 units and skip self-closing tags when truncating ([05ee8b9](https://github.com/AlexGalhardo/vukem/commit/05ee8b9a4e232634fdb72b7ca4945dd654766bb2))
- **stripe:** Bound param nesting, refuse NaN/Inf and send []byte as a string ([91d625a](https://github.com/AlexGalhardo/vukem/commit/91d625abc3780df129015f74b4bce4f5a4446bba))
- **vukemtest:** Restore Accept after JSON sends, match HX-Trigger lists and stored nils ([d916cd5](https://github.com/AlexGalhardo/vukem/commit/d916cd5fcbc8ae398b366e4c411f106fd35aa62a))
- **cli:** Validate generator input before writing and create the policies registry ([0a17532](https://github.com/AlexGalhardo/vukem/commit/0a175326a5238c7bf1624313f87037f677c82621))
- **cli:** Run templ generate before app-command rebuilds and watch .buildtags ([727d365](https://github.com/AlexGalhardo/vukem/commit/727d365c503c198135dd40dfd2ea7563a436729e))
- **cli:** Check bootstrap/app.go can be patched before install:inertia deletes files ([0580ff6](https://github.com/AlexGalhardo/vukem/commit/0580ff6f2b2cdd0c0ba0f9480a4b472b049a08af))
- **cli:** Read APP_ENV from .env too when choosing embedded resources ([e8166d5](https://github.com/AlexGalhardo/vukem/commit/e8166d5a962c416f3bad2f196ea17993d108fa2a))
- **cli:** Build the release binary with CGO_ENABLED=0 like the Dockerfile ([0f5d2e1](https://github.com/AlexGalhardo/vukem/commit/0f5d2e144f080068c558c5e4092cd35fbcc9c1ca))
- **vukem:** Report every boot configuration error at once ([d31d8dc](https://github.com/AlexGalhardo/vukem/commit/d31d8dc5fa0ec79702691666c125dd5852e31419))
- **flags:** Fail closed when a FLAG_ @file: secret cannot be read ([430b7db](https://github.com/AlexGalhardo/vukem/commit/430b7db869fdaab985b730b20a4d9192f1314e14))
- **vukem:** Check @file: secrets are regular files before opening them ([6d98bce](https://github.com/AlexGalhardo/vukem/commit/6d98bce629c681667f1e3b575756119af55deb00))
- **schema:** Defer closing the table listing rows on every return ([320f9a1](https://github.com/AlexGalhardo/vukem/commit/320f9a1f0a02bedf2d45909b9320c2f4bf63add8))

### Security

- **vukem:** Send security headers on the maintenance 503 ([8eff044](https://github.com/AlexGalhardo/vukem/commit/8eff0444c0957d6a3093e1820c69b1167a4ecdde))
- **BREAKING:** **openapi:** Consistent operationIds, plain route bodies, unknown schemes fail ([22a766c](https://github.com/AlexGalhardo/vukem/commit/22a766cddecb7886b85de19352fe6213ffc6350f))
- **api:** Document v0.28 API, OpenAPI and GraphQL fixes and refresh stale names ([88691b5](https://github.com/AlexGalhardo/vukem/commit/88691b5a6d2bf0caa4c80a0cd00d0080efc5fabc))

## [0.27.0] - 2026-10-18

### Added

- **site:** Add a changelog page linked from the header ([6381fa5](https://github.com/AlexGalhardo/vukem/commit/6381fa54f1e32d24214cb9771f99f061833bec1d))
- **auth:** Add ProblemJSON so every API auth failure is problem+json ([0c183cf](https://github.com/AlexGalhardo/vukem/commit/0c183cffb6bb517ed042e39f92182fd281af9a87))
- **vukem:** Warn once when proxy headers arrive without TRUSTED_PROXIES ([06ee06a](https://github.com/AlexGalhardo/vukem/commit/06ee06af672db9fbd8be209e67346634a8c3c1e1))
- **ratelimit:** Export IPKey and group throttle keys by it ([b0e40f5](https://github.com/AlexGalhardo/vukem/commit/b0e40f5fda2e0737f1bd92ce0ec4f395cb63af99))
- **vukem:** Let lazy store funcs fall back with ErrUseDefault ([7c2b800](https://github.com/AlexGalhardo/vukem/commit/7c2b800ab912d9fdcd487559bd0e9fabab4f1d6f))
- **telegram:** Read the Bot API base URL from TELEGRAM_API_URL ([eaab1b3](https://github.com/AlexGalhardo/vukem/commit/eaab1b31f25a3595eade4aa3542be295cba01aba))
- **fixinline:** Check every go:fix inline wrapper against go fix ([8655ddb](https://github.com/AlexGalhardo/vukem/commit/8655ddb02d03de9f3ad7d5e28e47058e0f772269))
- **database:** Add ScanTime for timestamps SQLite returns as text ([7198679](https://github.com/AlexGalhardo/vukem/commit/71986795b4354c23b7db8e276bf9a6b8faa10c54))
- **factory:** Add en and pt-BR locales with commerce and address data ([9c5bf18](https://github.com/AlexGalhardo/vukem/commit/9c5bf1846f977a1bbfac5f420183b344de016d37))
- **factory:** Add CreateMany to insert factory models in batches ([7dc730b](https://github.com/AlexGalhardo/vukem/commit/7dc730b096d0e0bd2ddec38037780dac80133d9e))
- **vukem:** Select seeders and pass seeder flags in db:seed ([4312444](https://github.com/AlexGalhardo/vukem/commit/4312444177bb3306a65b034e0450b2c772d0301d))
- **flags:** Add typed feature flags with a cached database store ([30031c3](https://github.com/AlexGalhardo/vukem/commit/30031c30e1e7e99e38caface134669155f21dbf9))
- **vukem:** Register feature flags with WithFlags and build them with app.Flags ([ffed272](https://github.com/AlexGalhardo/vukem/commit/ffed272372bd74904f01f8d0e8f7d9c8e233f544))
- **admin:** Add a feature flags page with audit events ([041c0f5](https://github.com/AlexGalhardo/vukem/commit/041c0f55291c0dffabae0dede7f7df16d0bcf2f0))
- **cli:** Add install:flags and wire feature flags into the admin scaffold ([f70d6b0](https://github.com/AlexGalhardo/vukem/commit/f70d6b01f2c95dfa10c45a0d0b6b93f952d07bdd))
- **web:** Let a frontend adapter render the error page ([358087f](https://github.com/AlexGalhardo/vukem/commit/358087f1fe9fa73cc9edb4f2261600be83647795))
- **cli:** Ship an Error page in the Inertia starters ([f9c00a7](https://github.com/AlexGalhardo/vukem/commit/f9c00a72b6f05e24a37cd433de5a51a243cfc4e5))
- **BREAKING:** **vukem:** Cap request headers at 64 KiB with HTTP_MAX_HEADER_BYTES ([320b89b](https://github.com/AlexGalhardo/vukem/commit/320b89be73e9cd1ad313e73cc1c7b0766ac44c52))
- **grpc:** Let operators raise the gRPC port's header cap ([7db5602](https://github.com/AlexGalhardo/vukem/commit/7db560293d1f3e88f4f9a381d094972247d9348d))
- **vukem:** Keep an existing ErrorPage hook and warn without an Error page ([0ab65c5](https://github.com/AlexGalhardo/vukem/commit/0ab65c57551b735bc8d72401eb20379362d61530))

### Changed

- **vukem:** Group the root package files by theme ([a90f9c8](https://github.com/AlexGalhardo/vukem/commit/a90f9c8ec7a8c808461b93fadaf2c5442336925f))
- **factory:** Replace NewFakerWithLocale with Faker.WithLocale ([fe297b4](https://github.com/AlexGalhardo/vukem/commit/fe297b4299ec6e54cc8140349f39c61112f62913))
- **flags:** Type scopes, version cached values and describe flags with Info ([5593d07](https://github.com/AlexGalhardo/vukem/commit/5593d07e5986c9fcbd238014c641e7325dc88083))
- **web:** Pass error pages to the hook as a web.ErrorPage struct ([b34be2b](https://github.com/AlexGalhardo/vukem/commit/b34be2ba350482fdaabf81542669680a021b95b4))

### Fixed

- **BREAKING:** **cli:** Require the account's login to verify an email address ([37da4e1](https://github.com/AlexGalhardo/vukem/commit/37da4e1d2e6303532e51cfa15b615d8d34c5ed1f))
- **cli:** Send expired verification links to the resend page ([379620d](https://github.com/AlexGalhardo/vukem/commit/379620d73fd2946e615531915f91ab475ae6e1fe))
- **BREAKING:** **ratelimit:** Key IPv6 clients by their /64 in ByIP ([6f66901](https://github.com/AlexGalhardo/vukem/commit/6f669013544e68563821c75d18e4894219204755))
- **vukem:** Harden the ErrUseDefault fallback of lazy store funcs ([9ac99f1](https://github.com/AlexGalhardo/vukem/commit/9ac99f1e715645f4f116ec51b3f235d28824170d))
- **BREAKING:** **grpc:** Give the gRPC port its own cleartext opt-in ([91cf1f5](https://github.com/AlexGalhardo/vukem/commit/91cf1f51e1023db020a26a64ee3f5b4758c01bba))
- **BREAKING:** **otel:** Export metrics to the generic OTLP endpoint by default ([4cb0b8e](https://github.com/AlexGalhardo/vukem/commit/4cb0b8ee7d784700f01817bf5be32f1970761ffc))
- **apicheck:** Reject trailing braces and foreign-major exception versions ([4156585](https://github.com/AlexGalhardo/vukem/commit/4156585c74fffc3fc9e1c55cee2f3ad1a95ad99a))
- **orm:** Scan text timestamps into time fields on SQLite ([fc0eac7](https://github.com/AlexGalhardo/vukem/commit/fc0eac7566a8cef023edae3bbe270b78885c8c6d))
- **vukem:** Fail closed on db:seed --class without a name ([4de9aa6](https://github.com/AlexGalhardo/vukem/commit/4de9aa6fbdc0240e96f0dd24a663f7441e50eeec))
- **database:** Accept space-separated RFC 3339 text in ScanTime ([ea2ef1c](https://github.com/AlexGalhardo/vukem/commit/ea2ef1ce909a6c44a45940ad8b33e420d5977c62))
- **flags:** Match FLAG_ keys exactly when checking for unknown flags ([7895fad](https://github.com/AlexGalhardo/vukem/commit/7895fad477c248c3e908ae65768a4037f85032a6))
- **vukem:** Refuse FLAG_ keys in .env when no flag is registered ([c5c2697](https://github.com/AlexGalhardo/vukem/commit/c5c2697c301d870e968b1fc812c3ab621e5a278c))
- **admin:** Redact string flag values and survive a failed audit row ([52881da](https://github.com/AlexGalhardo/vukem/commit/52881da8b956fa78c94f411e357035008e9eb5d6))
- **cli:** Make install:flags safe to re-run ([7bbcbf6](https://github.com/AlexGalhardo/vukem/commit/7bbcbf699e2ede3d8ec245f97c6e922d64484370))
- **apidocs:** Load the API reference with zero CSP violations ([770263f](https://github.com/AlexGalhardo/vukem/commit/770263f159dd3581d2a816ce2fa46d79bfc5143f))

### Security

- **repo:** Move the community health files into .github ([49c22b0](https://github.com/AlexGalhardo/vukem/commit/49c22b0a9651ccfe96b8d70af3a97d27be5e143a))
- **apicheck:** Add a versioned exception list for security-driven breaks ([e4542e7](https://github.com/AlexGalhardo/vukem/commit/e4542e77a98365f88b2b64cab05d797417d3664f))
- **BREAKING:** **inertia:** Render an Error page for errors on Inertia requests ([d9d3876](https://github.com/AlexGalhardo/vukem/commit/d9d3876dfaa2a448aa5c86a922edd61de86f20dc))
- **web:** Render the built-in status pages under the default CSP ([83fcc01](https://github.com/AlexGalhardo/vukem/commit/83fcc0158e8f445fc285fe482a63fe652a17dbbe))

## [0.26.0] - 2026-10-17

### Added

- **stripe:** Add typed Accounts v2, refunds and transfer reversals ([3ae351a](https://github.com/AlexGalhardo/vukem/commit/3ae351a78aaed03e311de6b877e9d85e16583077))
- **BREAKING:** **cache:** Add Sharer contract and fail closed on undeclared stores ([7b86743](https://github.com/AlexGalhardo/vukem/commit/7b86743ab437fdf6d377ee4eceb075ccf7913537))
- **redis:** Declare the cache store shared ([bcd81e0](https://github.com/AlexGalhardo/vukem/commit/bcd81e00ace0eacfee56bb9896578e534edd257c))
- **cache:** Bound Unwrap chains with cache.Layers ([25e49bb](https://github.com/AlexGalhardo/vukem/commit/25e49bb3f2639fbdd447afcbedd03d3cda4b7017))
- **database:** Add RebindChecked that rejects unterminated SQL and mixed placeholders ([a5864e0](https://github.com/AlexGalhardo/vukem/commit/a5864e0bc48097d8db7a1f0a5c3730ea57091ad8))
- **BREAKING:** **queue:** Pop same-millisecond database jobs in push order ([b644749](https://github.com/AlexGalhardo/vukem/commit/b64474936266433f45627d3773cfca13ec8de025))
- **signed:** Verify links signed with previous keys ([acad399](https://github.com/AlexGalhardo/vukem/commit/acad39972e77d46365be94140e8ef80b003c790b))
- **social:** Accept state cookies signed with previous keys ([febfa33](https://github.com/AlexGalhardo/vukem/commit/febfa33e9d682537e8d799a97cb00ab080a11487))
- **session:** Open cookie sessions sealed with previous keys ([5658c74](https://github.com/AlexGalhardo/vukem/commit/5658c74f556f1befc7c241a92cad70a72d983e41))
- **middleware:** Accept maintenance bypass cookies from previous keys ([efccd58](https://github.com/AlexGalhardo/vukem/commit/efccd58c795f38851e8f44f6b772866c88bd6c31))
- **cli:** Add key:rotate and pass previous keys in auth scaffolds ([8cdca11](https://github.com/AlexGalhardo/vukem/commit/8cdca1191371b9170433a2f127bd354aeeaee4c4))
- **grpc:** Add ErrorContext to translate keyed errors into the request locale ([ae3a3ca](https://github.com/AlexGalhardo/vukem/commit/ae3a3ca9a948825d7138f54a7ddaa9855c0e4000))
- **schema:** Add Schema.HasColumn for upgrade migrations ([99e2b8a](https://github.com/AlexGalhardo/vukem/commit/99e2b8a429ed06bd8cbdc01523b92553c8507699))
- **BREAKING:** **auth:** Soft revoke API tokens with revoked_at ([6cf8d9c](https://github.com/AlexGalhardo/vukem/commit/6cf8d9c8cabc1cb276c88e4593738a704d2b73e6))
- **cli:** Add revoked_at to the install:auth access_tokens table ([534c3d8](https://github.com/AlexGalhardo/vukem/commit/534c3d8c24d55344e0ff44eca2e5d4afcb4d9cd5))
- **cli:** Revoke API tokens with Tokens.RevokeAll on password reset ([d66f523](https://github.com/AlexGalhardo/vukem/commit/d66f5236611df43c7b86ff994582b3ecbb1b97c3))
- **BREAKING:** **i18n:** Return the config error from Middleware instead of panicking ([43df6fd](https://github.com/AlexGalhardo/vukem/commit/43df6fdb16822ec29b763d2cff6fa04ded5d3201))
- **mail:** Add the Mailtrap Email API transport ([1c70018](https://github.com/AlexGalhardo/vukem/commit/1c70018d6241eb8d523a590e059afe4db4a1dd20))
- **vukem:** Select the Mailtrap transport with MAIL_MAILER=mailtrap ([0689a31](https://github.com/AlexGalhardo/vukem/commit/0689a31745d22d197badffee0f035b97c7d6efd0))

### Changed

- **graphql:** Derive the 5xx check from errs.Status ([6cd0ccb](https://github.com/AlexGalhardo/vukem/commit/6cd0ccb2ec420d1efea771b323d6f562be3d6dcc))
- **BREAKING:** **orm:** Remove the deprecated BeforeCreater and AfterCreater aliases ([ad02707](https://github.com/AlexGalhardo/vukem/commit/ad027077a14da124c2bad69926bfd6923dd77302))
- **BREAKING:** **mail:** Remove the deprecated WithAttachments alias ([7c57159](https://github.com/AlexGalhardo/vukem/commit/7c57159ccf919f4d607db168730d2ef2f81482e7))
- **BREAKING:** **stripe:** Remove the deprecated Client.Do ([a5affc3](https://github.com/AlexGalhardo/vukem/commit/a5affc3f86b27c4370ad69aa1ab39227dbcdfd61))

### Fixed

- **BREAKING:** **stripe:** Map v2 errors and validate Call paths and object ids ([4550bf6](https://github.com/AlexGalhardo/vukem/commit/4550bf63886fd7261e3df586975fffc5daa334ce))
- **BREAKING:** **schedule:** Check OnOneServer locks with cache.Shared ([3c6f831](https://github.com/AlexGalhardo/vukem/commit/3c6f831fc4695ebd62e83fe6ecb5888d69a5e6a6))
- **BREAKING:** **vukem:** Require a declared shared cache for RATE_LIMIT_STORE=cache ([5ba0290](https://github.com/AlexGalhardo/vukem/commit/5ba0290b5aac939115b7ec8310a6d217d4f4bc55))
- **schedule:** Name cache.Sharer in ErrLocksNotShared ([42fe1c1](https://github.com/AlexGalhardo/vukem/commit/42fe1c1263871252616f16a200ffdddacbcc84ac))
- **database:** Quote only an 80-byte prefix of the query in RebindChecked errors ([e7aea62](https://github.com/AlexGalhardo/vukem/commit/e7aea62675357bf66967c16950acf381cb457a38))
- **queue:** Add the seq column only on an unknown-column probe error ([cd7434a](https://github.com/AlexGalhardo/vukem/commit/cd7434afd9d220c5609f3ec754d05d6da65f4c6d))
- **cli:** Keep the starter upload test off the filesystem ([f4a0be4](https://github.com/AlexGalhardo/vukem/commit/f4a0be40e28c55a6ead6c9b977ea7398711242d1))
- **cli:** Make key:rotate atomic and keep .env line syntax ([c063b95](https://github.com/AlexGalhardo/vukem/commit/c063b953fb3ac02faae176d36648b147c1adb0c9))
- **graphql:** Keep loader batches whole while siblings make progress ([dc529e4](https://github.com/AlexGalhardo/vukem/commit/dc529e459c0af985aa3d2ad1c7a856624e5eb2b0))
- **graphql:** Translate keyed resolver errors into the request locale ([427aab1](https://github.com/AlexGalhardo/vukem/commit/427aab1116a9a249047a4012a5227897aed0076b))
- **graphql:** Hide field errors wrapped in a 5xx HTTPError ([6e05325](https://github.com/AlexGalhardo/vukem/commit/6e05325b21ed67b206c56f509243ae116f7623a2))
- **BREAKING:** **grpc:** Send 5xx HTTPErrors as "internal error" and log their cause ([35832ec](https://github.com/AlexGalhardo/vukem/commit/35832ecdbe291228df318c773039a9afc5d2b260))
- **graphql:** Cap how long a pending loader batch waits for progress ([2a6c078](https://github.com/AlexGalhardo/vukem/commit/2a6c0784396a328b58570b9c10d9284760d23d74))
- **BREAKING:** **grpc:** Mask unknown Validate errors and 5xx headers ([aa000e2](https://github.com/AlexGalhardo/vukem/commit/aa000e2946a2acd18f8e3655b9449ef5ad5be005))
- **auth:** Answer 503 when the token store fails ([58a101b](https://github.com/AlexGalhardo/vukem/commit/58a101b458ac9c8d4498e10b2dc29434df5e7681))
- **auth:** Keep revoking possible on access_tokens without revoked_at ([30404bd](https://github.com/AlexGalhardo/vukem/commit/30404bdacf98bc4cd12166df2afea1118de485ca))
- **mail:** Harden Mailtrap error decoding and redirects ([a9f7b84](https://github.com/AlexGalhardo/vukem/commit/a9f7b84fc45dda99edd3533284d51fe99c90ad85))

### Security

- **cache:** Document the cache.Sharer contract ([73c41d0](https://github.com/AlexGalhardo/vukem/commit/73c41d091dc2338cafabce5a50915e2aa73f150a))
- **vukem:** Honour APP_PREVIOUS_KEYS in every APP_KEY-derived feature ([6c5084c](https://github.com/AlexGalhardo/vukem/commit/6c5084c00b331d6c65869ab0692cf827450530ba))
- **web:** Add AuditEvent for routine audit records at info ([35995da](https://github.com/AlexGalhardo/vukem/commit/35995da233e9ab34652e4378a02344429e2a1be7))
- **BREAKING:** **cli:** Make the install:auth sign-up enumeration-safe ([53788f2](https://github.com/AlexGalhardo/vukem/commit/53788f257ecf3b16a4f42e97720afcf530b37a93))
- **BREAKING:** **cli:** Require verified email and rate limit sign-up in install:auth ([0eb6fa2](https://github.com/AlexGalhardo/vukem/commit/0eb6fa2296d6ee510163c36b52d16f653dced296))
- **BREAKING:** **cli:** End squatter access when the email is proven ([99edfe5](https://github.com/AlexGalhardo/vukem/commit/99edfe5253b383b9816b3bd1a034f34cabbe5915))

## [0.25.0] - 2026-10-17

### Added

- **middleware:** Log peer address and resolved client IP per request ([c6da5ad](https://github.com/AlexGalhardo/vukem/commit/c6da5ad6082dac61fa811e7e30a950d6855ffb56))
- **middleware:** Add HSTSForce and HSTS_FORCE for untrusted TLS proxies ([98135e2](https://github.com/AlexGalhardo/vukem/commit/98135e2347d568dec5ed56fc2826d5be40f994fd))
- **stripe:** Add connect resources and per-request idempotency and account options ([7970378](https://github.com/AlexGalhardo/vukem/commit/7970378c81c62cc0911dba045ca2c069737ba0bb))
- **stripe:** Accept connect webhook secrets and expose event account ([e253c0f](https://github.com/AlexGalhardo/vukem/commit/e253c0fda05990c1d6ab662a0bbd3fec231753b8))
- **router:** Report whether routes were compiled ([26f12d5](https://github.com/AlexGalhardo/vukem/commit/26f12d57881f89353ca8846f4a224ce47d6c4ded))
- **vukem:** Add WithRoutesAfterBoot for routes that need booted services ([8596784](https://github.com/AlexGalhardo/vukem/commit/8596784ba70858e4ec75998d4c3d404646202890))
- **vukem:** Let seeders reach the app with WithAppSeeders ([d2c820a](https://github.com/AlexGalhardo/vukem/commit/d2c820a9b5875593ab5bdc79d1a16c58428f671c))
- **vukem:** Name seeders through an optional Name method ([da31331](https://github.com/AlexGalhardo/vukem/commit/da31331e336af96606c71eda6ab6c0ebc8a00fac))
- **export:** Stream CSV, JSON and NDJSON downloads ([50f9f6b](https://github.com/AlexGalhardo/vukem/commit/50f9f6b5e03f00cc5df6974ddeb693d8bfc5470a))
- **errs:** Add message keys and a request translator to errors ([3200fb9](https://github.com/AlexGalhardo/vukem/commit/3200fb9836afa2744fe66229077b70857c50ea4b))
- **web:** Show keyed http errors in the request locale ([67dd98b](https://github.com/AlexGalhardo/vukem/commit/67dd98b6f9d9f8760e5082a7795c1479b11cde9e))
- **i18n:** Ship built-in en and pt-BR framework messages ([5a54ca2](https://github.com/AlexGalhardo/vukem/commit/5a54ca22ee3ac4a740e4ea52e5a77fa29fceda21))
- **auth:** Translate guard errors through message keys ([6b52fdd](https://github.com/AlexGalhardo/vukem/commit/6b52fdd7b70ba8d60bb490178317ffaf272ba836))
- **social:** Add message keys to callback errors ([9eed6e8](https://github.com/AlexGalhardo/vukem/commit/9eed6e865b869f06103e12f17bd75712410ee3ce))
- **orm:** Translate unique and exists rule messages ([7c20d74](https://github.com/AlexGalhardo/vukem/commit/7c20d74cc3bcdaf747729a53bdcc3dce5c1ae6d0))
- **mail:** Build envelopes from the send context with ContextEnveloper ([f977288](https://github.com/AlexGalhardo/vukem/commit/f9772885a7cfe974df7e819cc2abdf8c9824ed50))
- **telegram:** Add SendSync/ContactSync, Filter and RateLimit ([ed796d4](https://github.com/AlexGalhardo/vukem/commit/ed796d4253fba8221be008c98a3c70f35e4b440a))
- **vukem:** Add TELEGRAM_LOG, TELEGRAM_RATE_LIMIT and WithTelegram ([a0e4030](https://github.com/AlexGalhardo/vukem/commit/a0e40308be4c824d6f1259dbc3c4e4fa43d005c4))
- **webhook:** Add DeliveryLogFunc and document DeliveryLog as the extension point ([9fa44c8](https://github.com/AlexGalhardo/vukem/commit/9fa44c8c03db4b46ce12ae6615e745434c2384e4))
- **grpc:** Serve rpc services on their own port as an app runner ([54bec2a](https://github.com/AlexGalhardo/vukem/commit/54bec2a7e019ec4846223da9bd9dd1909e2a12f6))
- **s3:** Add idempotent region-aware CreateBucket ([1586a42](https://github.com/AlexGalhardo/vukem/commit/1586a42c3c0a4d101c8159e9f0b065b491cb916a))
- **totp:** Encode qr codes up to version 40 ([b078d4f](https://github.com/AlexGalhardo/vukem/commit/b078d4f74fce23ca7b26212cbaed48db2706fd97))
- **api:** Add Op.ValidateResponse to check typed handler output ([5b1736e](https://github.com/AlexGalhardo/vukem/commit/5b1736e6e94cc448482269d5f83ca17c867a1270))
- **BREAKING:** **openapi:** Emit zod 4 schemas and inline request body schemas ([6204877](https://github.com/AlexGalhardo/vukem/commit/62048773bf8f88d97663d294a17df930a25acdc3))
- **vukem:** Run codegen:types without a database or APP_KEY ([1f1077f](https://github.com/AlexGalhardo/vukem/commit/1f1077fe85dda3d794516c08a3121587d3e21bfe))
- **BREAKING:** **i18n:** Add locale source order and allowed locales to MiddlewareConfig ([3d1680d](https://github.com/AlexGalhardo/vukem/commit/3d1680d1da6abc3841789020e5f8e5887960898f))
- **vukem:** Add MiddlewareOptions.I18n and APIMiddlewareWith ([64f3d12](https://github.com/AlexGalhardo/vukem/commit/64f3d12a59ca9d51e2f29c180535e5655bb2c4f9))
- **inertia:** Add RenderStatus for pages with a non-200 status ([3ffb56f](https://github.com/AlexGalhardo/vukem/commit/3ffb56f9c68fa38b93cca83cea5019fe42f1206a))
- **i18n:** Fail closed on invalid middleware config and vary on cookie ([d80a1ec](https://github.com/AlexGalhardo/vukem/commit/d80a1ece17e73a54250f8f402accceedbc8ccbfa))
- **vukem:** Install the locale resolver in APIMiddleware ([9a35a8e](https://github.com/AlexGalhardo/vukem/commit/9a35a8ea977204c50db143764bf688f2d983f4ab))
- **vukem:** Add ServeOpenAPIWith for prefixed, production-opted documents ([0d9b5b5](https://github.com/AlexGalhardo/vukem/commit/0d9b5b573a1bc608d7deaf583a309ed5d1fabb4d))
- **BREAKING:** **schema:** Add partial indexes with Index/UniqueIndex(...).Where(expr) ([52f1906](https://github.com/AlexGalhardo/vukem/commit/52f19060543b374b1c0ae70c82e1fbce3e9896c2))
- **database:** Add Rebind for portable raw SQL placeholders ([1012e20](https://github.com/AlexGalhardo/vukem/commit/1012e20e7342150d9e9d4c092199e96b705c9375))
- **database:** Fan out query observers and add vukem.WithQueryObservers ([f983e9d](https://github.com/AlexGalhardo/vukem/commit/f983e9dca03443daa1c191e022613baec70e8d77))
- **uuid:** Add stdlib UUID v4/v7 package ([6faedf8](https://github.com/AlexGalhardo/vukem/commit/6faedf8d0423dc7f46dcdc6aa475966dfd58ada7))
- **session:** List a user's sessions and revoke one by handle ([5c70a2a](https://github.com/AlexGalhardo/vukem/commit/5c70a2a1d3502f907f82e1154a6e30170de269b9))
- **vukem:** Read SESSION_RECORD_CLIENT into the session config ([98d49bc](https://github.com/AlexGalhardo/vukem/commit/98d49bc8f4fcf2e379ac515e3b12f3a376b1ac8f))
- **auth:** End sessions on every driver with a per-user session version ([81efca9](https://github.com/AlexGalhardo/vukem/commit/81efca985b8e165a9423c971bc9bb3e9190e7345))
- **vukemtest:** Add WithSession and stamp the session version in ActingAs ([62a0836](https://github.com/AlexGalhardo/vukem/commit/62a08360f409494b07f02670109de9af797640e7))
- **redis:** Implement session.ListStore on the session store ([80c6b94](https://github.com/AlexGalhardo/vukem/commit/80c6b9437ea0cac0acb82f4b2ee0628f3f21ec21))
- **cli:** End every session on password reset with a session version ([bd4e878](https://github.com/AlexGalhardo/vukem/commit/bd4e8785027a0f2f4722375dfdf8e48dc61f84ab))
- **queue:** Add Queue.SetClock and the queue.Clock driver interface ([5d334ee](https://github.com/AlexGalhardo/vukem/commit/5d334ee397815d3f5164cd1ddfc63deb1524c5d4))
- **queue:** Bound the worker drain with WorkOptions.StopTimeout ([486ff01](https://github.com/AlexGalhardo/vukem/commit/486ff01719e00ebea2015a2e70b6f36106853539))
- **queue:** Add DispatchKindTx ([cafe81e](https://github.com/AlexGalhardo/vukem/commit/cafe81ed5b8d488ed419afc4e3a407fa4d7705a8))
- **redis:** Implement queue.Clock on the queue driver ([84aceaf](https://github.com/AlexGalhardo/vukem/commit/84aceafdf98b56fbdbdda6fd1dfdc167e38dc844))
- **mail:** Add Mailer.SendLaterTx ([add0822](https://github.com/AlexGalhardo/vukem/commit/add08225c5058951f2a80fb014cd192e6f2962f8))
- **schedule:** Add Task.OnOneServer ([3d4ac45](https://github.com/AlexGalhardo/vukem/commit/3d4ac451545eb2aac5e364d3679b4352b3537b54))
- **BREAKING:** **app:** Run a queue worker in serve for persistent queues ([8324377](https://github.com/AlexGalhardo/vukem/commit/8324377154b4116c07a5046d86ade02fb5e924c1))
- **app:** Add MailLaterTx ([a1a3f17](https://github.com/AlexGalhardo/vukem/commit/a1a3f17f79ebd22878ecc277720281ffb706ccb2))
- **app:** Refuse OnOneServer schedules on the in-process cache ([08d912d](https://github.com/AlexGalhardo/vukem/commit/08d912d219a41f0e9ab51d98fd1ecc323f44795a))
- **schedule:** Namespace task locks with Options.Namespace ([3c536b3](https://github.com/AlexGalhardo/vukem/commit/3c536b35b3db7332e2287c246d098274daf75b14))
- **httpclient:** Add Options.WrapTransport for tracing and metrics ([27e4822](https://github.com/AlexGalhardo/vukem/commit/27e48225d4be178c5f515bcb3a56deafc06f3cbe))
- **vukem:** Add App.Instrument hooks for database, queue and mail ([2ac2c2f](https://github.com/AlexGalhardo/vukem/commit/2ac2c2f47047741983e2df00aa6ded06ed5523b3))
- **otel:** Add metrics, data-layer spans and OTEL_* settings from app.Source ([a76a55d](https://github.com/AlexGalhardo/vukem/commit/a76a55da63e122ee49bc88ac64e5bb63966c9c9e))
- **otel:** Export metrics over OTLP/HTTP from OTEL_* settings ([4ee0cb1](https://github.com/AlexGalhardo/vukem/commit/4ee0cb176860612b06c3482b5dc3c8c9690c9448))

### Changed

- **stripe:** Deprecate Do in favour of Call ([d944d94](https://github.com/AlexGalhardo/vukem/commit/d944d940552e38c04d9891eb323508a6edf0bc2b))

### Fixed

- **middleware:** Warn when HSTS_FORCE is ignored and document its scope and log privacy ([3674147](https://github.com/AlexGalhardo/vukem/commit/3674147e586bdc7309f59fce5f26ba0c410351bc))
- **tools:** Report compatible apidiff changes as unchecked in gate mode ([77a74ed](https://github.com/AlexGalhardo/vukem/commit/77a74ed7d6cce3b0bff78a775f09828d52ac1bec))
- **stripe:** Refuse account scope on platform calls and decode expanded ids ([61133d0](https://github.com/AlexGalhardo/vukem/commit/61133d0221fac4bb3ade744888156c2ce0b395ae))
- **router:** Mark the router compiled before building it ([eb7bff3](https://github.com/AlexGalhardo/vukem/commit/eb7bff3a30b1ba747aed1c06aaf172b79dec300c))
- **vukem:** Fail closed around routes after boot and keep the boot error ([35172ca](https://github.com/AlexGalhardo/vukem/commit/35172cafed44625801bec811e2e18c01474c4669))
- **middleware:** Hijack through wrapped writers in gzip ([5bde2ca](https://github.com/AlexGalhardo/vukem/commit/5bde2caf8a8f1e030110482bc1bb65d4277860ad))
- **web:** Sanitize Download file names ([9eb6303](https://github.com/AlexGalhardo/vukem/commit/9eb63035885582d5508b02d8431062a30cb2402e))
- **export:** Renew write deadline per write and always abort mid-stream ([806c5b2](https://github.com/AlexGalhardo/vukem/commit/806c5b200ab4facdf1832dc42e6c0c8d14c3b795))
- **i18n:** Fill only existing catalogs of the same locale with built-in messages ([99b6c23](https://github.com/AlexGalhardo/vukem/commit/99b6c23b6a30605713809f15e119585408513716))
- **errs:** Copy the params given to NewKey ([ec9f1be](https://github.com/AlexGalhardo/vukem/commit/ec9f1bef7f287ae7fe1603d2361d2d086a4e97fa))
- **broadcast:** Show keyed sse refusals in the request locale ([6b4bc1d](https://github.com/AlexGalhardo/vukem/commit/6b4bc1dbb678ad49ac57cfeed480e55739c47ad5))
- **sse:** Release a topic's broker subscription when its last subscriber leaves ([23abd7b](https://github.com/AlexGalhardo/vukem/commit/23abd7b504bfa1c41f2c496e2d71d3fc38bc804f))
- **telegram:** Report errors outside the queue lock and never forward OnError records ([6e02e03](https://github.com/AlexGalhardo/vukem/commit/6e02e037fdd85dcbcf214b0ae04ca6f053dd8ae7))
- **sse:** Make every subscriber wait for its topic relay and fail closed ([9eb5277](https://github.com/AlexGalhardo/vukem/commit/9eb527758ae89feeecc1a16b0f67dcea55f8616c))
- **vukem:** Show telegram log forwarding off in about ([5cee6b7](https://github.com/AlexGalhardo/vukem/commit/5cee6b7f251509582dbf416202b1846b943019f3))
- **graphql:** Fold go names of json-tagged fields in the default resolver ([27ac7f1](https://github.com/AlexGalhardo/vukem/commit/27ac7f1c85632c85a0bca7c9a36c9ca8412e74fb))
- **graphql:** Free a websocket subscription id before its terminal message ([b917414](https://github.com/AlexGalhardo/vukem/commit/b91741413028612676cd1a7de7a594b8b2746315))
- **grpc:** Refuse a cleartext production port in Server too ([1e8b11b](https://github.com/AlexGalhardo/vukem/commit/1e8b11b1d76d050d1e43cb486902909e45325ddc))
- **BREAKING:** **z:** Treat required on a list as present, not non-empty ([5cd14ca](https://github.com/AlexGalhardo/vukem/commit/5cd14cacce60f4504ff0460923340aff3d8994c7))
- **openapi:** Emit minLength 1 for required strings ([abafd04](https://github.com/AlexGalhardo/vukem/commit/abafd043b4d5c73d3c564d8464b1e0be65830466))
- **BREAKING:** **openapi:** Suffix component names that shadow typescript globals ([514d1c8](https://github.com/AlexGalhardo/vukem/commit/514d1c84566a1520c262d5c491c76eee4acc3af7))
- **vukem:** Never boot a warmup app and export openapi in warmup ([5c34f59](https://github.com/AlexGalhardo/vukem/commit/5c34f59b7727048df0241bb92b48105750647b46))
- **openapi:** Reserve the fetch types the generated client uses ([d817eae](https://github.com/AlexGalhardo/vukem/commit/d817eae27c3bf9432d08cc55341eb54d85c21e40))
- **openapi:** Fail typescript codegen on colliding generated names ([2435e2b](https://github.com/AlexGalhardo/vukem/commit/2435e2b76b95038c406c56ce95b42bdeb7bc20aa))
- **web:** Answer 422 for z.FieldError and z.Issues returned by handlers ([bea0e86](https://github.com/AlexGalhardo/vukem/commit/bea0e8634f221b7db0c3153287a782029b8f9479))
- **BREAKING:** **z:** Hide the text of plain Refine and Custom errors ([6bdc413](https://github.com/AlexGalhardo/vukem/commit/6bdc4133f6300a7c71b87dbe62d0809802cf172d))
- **web:** Fail closed on z issues and skip redirect-back for GET ([36110d0](https://github.com/AlexGalhardo/vukem/commit/36110d031afd8b8e3adc45b469eebbfe779522b9))
- **upload:** Send z validation messages from tus completion ([e80f03b](https://github.com/AlexGalhardo/vukem/commit/e80f03bd00992ee34b9f63b1481e3e7aba77bd39))
- **apidocs:** Keep the nonce-only CSP and pass axe serious checks ([8ce814f](https://github.com/AlexGalhardo/vukem/commit/8ce814f00a52b200d987439259ae13c4d3fc2103))
- **auth:** Answer problem+json on token routes and use the app clock ([890d919](https://github.com/AlexGalhardo/vukem/commit/890d919a263b3941ff223810521571e6a3fc2c56))
- **auth:** Force JSON only on the error path of API guards ([cdae9a9](https://github.com/AlexGalhardo/vukem/commit/cdae9a9ca978d96dfbbbe3f0c35e74a2758effc6))
- **openapi:** Match PathPrefix on whole path segments ([6d9196b](https://github.com/AlexGalhardo/vukem/commit/6d9196b5ab789f873dcd1f7d3970f5c06a870294))
- **apidocs:** Limit the CSP shim to plain style values and hide scalar.com links ([95da209](https://github.com/AlexGalhardo/vukem/commit/95da20963c9678ed022f4c9f5c586c316c84e6f3))
- **orm:** Share the Rebind lexer for WhereRaw placeholder counting ([82972f7](https://github.com/AlexGalhardo/vukem/commit/82972f79804d82130675d2e05d228ffc37c00ec2))
- **dbtest:** Bound each conformance subtest and shrink the MySQL bulk insert ([db74695](https://github.com/AlexGalhardo/vukem/commit/db7469585eade44dbd8f3f4139a0658586535b65))
- **orm:** Lex MySQL -- comments, backticks and SQLite brackets per dialect ([d33721a](https://github.com/AlexGalhardo/vukem/commit/d33721af28b896b01dd3a761359003b5418f48bc))
- **orm:** Reject $n in WhereRaw and fail closed on unterminated WHERE SQL ([d21a30b](https://github.com/AlexGalhardo/vukem/commit/d21a30b4e63bcab38abc4af1573391593ae92326))
- **database:** Contain panicking query observers and ignore typed nils ([e5432a9](https://github.com/AlexGalhardo/vukem/commit/e5432a9508e7e6ac9adcbb1361ded7f0e567ec19))
- **auth:** Bind remember-me tokens to the user's session version ([e26bdc0](https://github.com/AlexGalhardo/vukem/commit/e26bdc0faa39aae445c6cd5da57b390629cae9a0))
- **cli:** Delete remember-me tokens when ending a user's other sessions ([493583f](https://github.com/AlexGalhardo/vukem/commit/493583fec5395e878fee491048d9bd8684f27106))
- **redis:** Key sessions by SHA-256 of the id and re-key legacy keys ([cb3f49b](https://github.com/AlexGalhardo/vukem/commit/cb3f49b121bd1f7f99dfb4c30691ae07655e2606))
- **auth:** Harden session version checks and remember-me deletion ([9ac9923](https://github.com/AlexGalhardo/vukem/commit/9ac992369bd5111c48df712aef4582976b0dc450))
- **session:** Strip control and format runes from the recorded user agent ([0854dad](https://github.com/AlexGalhardo/vukem/commit/0854dadd338da54d3e0b8173ffbcb1aa9dd378d8))
- **app:** Follow the app clock in the queue and its drivers ([e6dfa9c](https://github.com/AlexGalhardo/vukem/commit/e6dfa9c4f0063ea9bcf86637f53e5a6356cb472b))
- **app:** Stop waiting for runners that ignore ctx after SHUTDOWN_TIMEOUT ([8ad882a](https://github.com/AlexGalhardo/vukem/commit/8ad882a9daad99e97a824914477e4fc7f7c66737))
- **cli:** Queue the verification mail in the auth scaffold ([290116c](https://github.com/AlexGalhardo/vukem/commit/290116ca8ae03956d148646d080165793d59d5b8))
- **queue:** Return reserve errors from Work with StopWhenEmpty ([d358031](https://github.com/AlexGalhardo/vukem/commit/d358031e5f8bda86ad408448de75b8b9191c627e))
- **app:** Create the database queue tables at boot ([c8f5c5e](https://github.com/AlexGalhardo/vukem/commit/c8f5c5e7dd5d35440c454b562b88f186051c59b3))
- **app:** Namespace schedule locks by APP_NAME ([77667c7](https://github.com/AlexGalhardo/vukem/commit/77667c76bac9d8930207d49f6511fb9ef65bfd81))
- **queue:** Free only shutdown-caused interruptions, at most three in a row ([a8b4a0b](https://github.com/AlexGalhardo/vukem/commit/a8b4a0b97608f4e6a651b8311aafdde1d323ef88))
- **app:** Give runners three seconds of grace after SHUTDOWN_TIMEOUT ([fad85c2](https://github.com/AlexGalhardo/vukem/commit/fad85c2e75df35f96a1d2fa7a4001af24386b28f))
- **app:** Name the database queue in its boot error ([ec100e3](https://github.com/AlexGalhardo/vukem/commit/ec100e307333f371053e558f7c52ef11bfd67cde))
- **httpclient:** Fail closed when WrapTransport returns nil ([3b3c6f8](https://github.com/AlexGalhardo/vukem/commit/3b3c6f8eb86530854d30dad5c15a1c489fbb3da3))
- **otel:** Harden provider boot, shutdown and globals; add db.system.name ([d745922](https://github.com/AlexGalhardo/vukem/commit/d7459229435369c86eeaa13fa9dd455879350624))
- **vukem:** Chain Instrument query hooks through database observers ([2df56d3](https://github.com/AlexGalhardo/vukem/commit/2df56d308216ff990db784fa0c416a8c334b15e5))
- **queue:** Contain panicking job observers and add AddObserver ([d1f047b](https://github.com/AlexGalhardo/vukem/commit/d1f047b89d2d379818427f6ae0303c99b2f63bb5))
- **vukem:** Add Instrument job hooks through queue.AddObserver ([88b4aee](https://github.com/AlexGalhardo/vukem/commit/88b4aee710bbeaa19c33788603fef0a3dc44ef9f))
- **tools:** Exempt the sessiontest conformance suite from the coverage gate ([b223724](https://github.com/AlexGalhardo/vukem/commit/b223724623d094ba921a77e3ada8908266fc003f))

### Security

- **site:** Expand cache, scheduler, idempotency, two-factor, redis and mongodb pages ([03a9a83](https://github.com/AlexGalhardo/vukem/commit/03a9a834d3abf735596e9c3d1b832b59d0c476a4))

## [0.24.0] - 2026-10-17

### Added

- **schema:** Add named CHECK constraints with Check and DropCheck ([adb5339](https://github.com/AlexGalhardo/vukem/commit/adb5339ade090c3766de28ac6e0744a123a33061))
- **orm:** Add row locks with LockForUpdate, ForShare, SkipLocked and NoWait ([9f70c15](https://github.com/AlexGalhardo/vukem/commit/9f70c15c805f92705daeff1a53cf89881ea3577c))
- **orm:** Add CreateMany for batched multi-row inserts ([f420758](https://github.com/AlexGalhardo/vukem/commit/f4207582c9a2ae7cb349643d440c835459291918))
- **z:** Add FieldErrorKey for translatable refine messages ([c4a3e06](https://github.com/AlexGalhardo/vukem/commit/c4a3e06d3026a17b45a413ad7db5d7b988d53e59))
- **z:** Add StringSchema.Preprocess to normalize before checks ([39c6e26](https://github.com/AlexGalhardo/vukem/commit/39c6e2666f7a09b62455cc060264d364d97c5d7b))
- **errs:** Add ValidationError.Append without a result ([1bc6fc1](https://github.com/AlexGalhardo/vukem/commit/1bc6fc18ec935d2cc1e45b21a8caec4aaf6bce77))
- **vukemtest:** Add Build to assert that an app fails to build or boot ([c9d0b10](https://github.com/AlexGalhardo/vukem/commit/c9d0b10e84744c566a484fac3e207491bc88cd2c))
- **vukemtest:** Add flash assertions for web and Inertia responses ([ff26679](https://github.com/AlexGalhardo/vukem/commit/ff266790908cd13947ca635bf050378fe0e9e0dc))
- **vukemtest:** Add AssertNoFlash and require a present flash value ([d5f7c8b](https://github.com/AlexGalhardo/vukem/commit/d5f7c8becdeeed943a7b1b4e938b9faa34a69688))
- **vukem:** Run the scheduler inside serve ([fab0830](https://github.com/AlexGalhardo/vukem/commit/fab0830383ec035c12f5f9514f2758e8fca46aa8))
- **BREAKING:** **vukem:** Run the scheduler inside serve by default ([0cf2d69](https://github.com/AlexGalhardo/vukem/commit/0cf2d69be198fb0b6d5f4dee7d033ff06ec002d6))
- **s3:** Bind presigned PUT URLs to content type and length ([74a04f4](https://github.com/AlexGalhardo/vukem/commit/74a04f48dbd638a76a89f40e394a4e94f45b3163))
- **mongo:** Add OpenLazy and document MONGO_DATABASE and authSource ([c567860](https://github.com/AlexGalhardo/vukem/commit/c56786011764831395620c8bd8155daa1df48cc8))
- **redis:** Add OpenLazy so the app can boot while Redis is down ([3362b03](https://github.com/AlexGalhardo/vukem/commit/3362b030d9ffb5cfd83d54c4e0d9eaa964ddcd99))
- **middleware:** Add CSPSources and ReportURI to SecureHeadersConfig ([b245387](https://github.com/AlexGalhardo/vukem/commit/b245387ee1e69c35f2aa2c177c05af7d87b583ba))
- **vukem:** Add DefaultMiddlewareWith with CSP, stateless and bypass options ([f0aac24](https://github.com/AlexGalhardo/vukem/commit/f0aac246ef655890bec177e4bb0d42c8ec6935ac))
- **auth:** Add a configurable prefix to api tokens ([0c7a622](https://github.com/AlexGalhardo/vukem/commit/0c7a6220349a91c386736e4e4f6127219efb09be))
- **webhook:** Add hmac, a middleware constructor that fails at boot ([ffc315a](https://github.com/AlexGalhardo/vukem/commit/ffc315ae392e9637afc9542684f93f0709c19fb2))
- **BREAKING:** **cli:** Translate install:auth messages with en and pt-BR catalogs ([b1b3926](https://github.com/AlexGalhardo/vukem/commit/b1b39264ca2ed289f3a3ee71561c44e79c27cc3c))
- **database:** Add the vukem_nosqlite build tag to leave SQLite out ([063cabc](https://github.com/AlexGalhardo/vukem/commit/063cabcd19c4694fb49865445f8546feecb67adc))
- **cli:** Add vukem new --minimal and project build tags ([a879c31](https://github.com/AlexGalhardo/vukem/commit/a879c3131a0b9004d3f1149a8b224e6914a6f72b))
- **session:** Store hashed ids and revoke a user's other sessions ([a79cc72](https://github.com/AlexGalhardo/vukem/commit/a79cc7237e1f590df365be5e48bbca8d99feec39))
- **auth:** Record the session user on login ([9a33939](https://github.com/AlexGalhardo/vukem/commit/9a339398346080d1e97c051c542108befcba5cc8))
- **cli:** End other sessions on password reset and 2fa enable ([be85d8b](https://github.com/AlexGalhardo/vukem/commit/be85d8b13fe1466cf4326814aad0755e5c7ca058))
- **database:** Add Tx.AfterCommit hooks ([55ec70e](https://github.com/AlexGalhardo/vukem/commit/55ec70e4ae951046746b7d60db15b55535462cb9))
- **app:** Add WithJobsFunc so job handlers reach the app ([4dc4cf0](https://github.com/AlexGalhardo/vukem/commit/4dc4cf0763d22b992f22b84071ec067f7a73831a))
- **mail:** Add Mailer.SendLater ([d5740c3](https://github.com/AlexGalhardo/vukem/commit/d5740c3a1289b49c31470c494604fe4f8dfa0c40))
- **app:** Encrypt queued mail payloads and wire Mailer.SendLater ([9640bd9](https://github.com/AlexGalhardo/vukem/commit/9640bd93b07ec0d3e3aab5010f4e497a48a009fc))
- **vukem:** Add ReadyHandler, CheckHealth and per-check health timeouts ([16b84b5](https://github.com/AlexGalhardo/vukem/commit/16b84b5f07ac638ac8b604655a95969f5f9f0a33))
- **vukem:** Expose Stdout, Stderr and Stdin to app commands ([9b520b7](https://github.com/AlexGalhardo/vukem/commit/9b520b7703dd48d3d5d731b1c8b2b97187e5e5c4))
- **BREAKING:** **vukem:** Boot without APP_KEY outside production and fail closed on use ([2b73e60](https://github.com/AlexGalhardo/vukem/commit/2b73e601042499d966a14a15e857761e801f2209))
- **vukem:** Add lazy cache, session and queue store options ([515e4fc](https://github.com/AlexGalhardo/vukem/commit/515e4fcd1cae6e49586c4222d58c8de7186c3d58))
- **middleware:** Honour X-Forwarded-Proto only from trusted proxies ([d6fc83f](https://github.com/AlexGalhardo/vukem/commit/d6fc83f6a5c9a936d23ebcf3666c65dc14e55b53))
- **vukem:** Read TRUSTED_PROXIES and TRUST_FORWARDED_PROTO from the environment ([59b87a4](https://github.com/AlexGalhardo/vukem/commit/59b87a4e09b89be136d0f8c893aab041c1b22010))
- **cli:** Ship a production Dockerfile and .dockerignore in every starter ([48742fb](https://github.com/AlexGalhardo/vukem/commit/48742fb1bbf1e2276e2723b893f6ed787dbe2a9e))
- **web:** Read the client IP from a named proxy header ([5290e94](https://github.com/AlexGalhardo/vukem/commit/5290e94f94cd049876089721dcc9028ffd21c42a))
- **cli:** Build the starter image with the tags in .buildtags ([1584b64](https://github.com/AlexGalhardo/vukem/commit/1584b64227503e4505053f43912439e38e6a6b9c))
- **vukem:** Warn at boot when TRUSTED_PROXIES trusts every address ([d6081c3](https://github.com/AlexGalhardo/vukem/commit/d6081c3c90b7d1b1f398aab793c1636e99cf5b01))

### Fixed

- **orm:** Make CreateMany fail closed on unordered ids and pk-only models ([c4a849f](https://github.com/AlexGalhardo/vukem/commit/c4a849f7425706ace5f38337fbb89280ad8403ce))
- **z:** Revalidate Preprocess output and reject a nil func ([ad38233](https://github.com/AlexGalhardo/vukem/commit/ad38233e9ea3d0be1cc1b4da0ed90ec978c07f95))
- **BREAKING:** **z:** Honor FieldError path in Custom parser errors ([b3c5a4b](https://github.com/AlexGalhardo/vukem/commit/b3c5a4b60849f0d6945cae8c8dbaa7fe9064e26e))
- **session:** Expire store sessions by the manager clock ([b1d8ac6](https://github.com/AlexGalhardo/vukem/commit/b1d8ac62417b122af0a4a19bf81303dfc70e8728))
- **session:** Make the store clock race-free and test Prune with it ([83ba81a](https://github.com/AlexGalhardo/vukem/commit/83ba81a995d78ce0cbfa05665e388edc41bd8def))
- **schedule:** Bound the wait for running tasks on shutdown ([8d48a39](https://github.com/AlexGalhardo/vukem/commit/8d48a391dc19018a37bbe7e659f36b03bf04165b))
- **admin:** Match % and _ literally in list search ([18933b5](https://github.com/AlexGalhardo/vukem/commit/18933b5bb9322b74280697992b514e981671a69b))
- **middleware:** Refuse wildcard hosts and unsafe schemes in CSPSources ([4ca3a65](https://github.com/AlexGalhardo/vukem/commit/4ca3a6570b0ee5b15168171204a7ea8932c854a7))
- **vukem:** Refuse SessionBypass with Stateless and document bypass matching ([3c1c541](https://github.com/AlexGalhardo/vukem/commit/3c1c541407d75fa9f68ef4da62e87d00795f7f8b))
- **social:** Record used oauth states in the cache store ([d7b5e3f](https://github.com/AlexGalhardo/vukem/commit/d7b5e3f22e3c75924bf1aafba44943326c880d09))
- **BREAKING:** **jwt:** Require audience and issuer with a remote key set ([c0717df](https://github.com/AlexGalhardo/vukem/commit/c0717df46dea99aba59c185e9c4a453c441d5839))
- **social:** Stop reflecting the provider error parameter ([e0e36fd](https://github.com/AlexGalhardo/vukem/commit/e0e36fd72d8a1b461c2e43a540f1fbed7455f4a9))
- **BREAKING:** **api:** Bound query page and filter value lists ([ca25efb](https://github.com/AlexGalhardo/vukem/commit/ca25efbd65e2d2a96b8b4b941e5e07662a08cd3c))
- **BREAKING:** **api:** Scope idempotency keys by the authenticated principal ([035a90f](https://github.com/AlexGalhardo/vukem/commit/035a90f007da70db1be3ff0a357e370d574dc346))
- **auth:** Keep the session when the user lookup fails ([9f3cc10](https://github.com/AlexGalhardo/vukem/commit/9f3cc1012c12ebe81a51e2327e78362d27d43de3))
- **auth:** Treat wrapped no-rows errors from providers as not found ([2eb7730](https://github.com/AlexGalhardo/vukem/commit/2eb7730d7676049302811e83ca5ed30896a784b0))
- **BREAKING:** **jwt:** Detect shared key sets by interface and fail guards at boot ([df31f51](https://github.com/AlexGalhardo/vukem/commit/df31f51635547a00d5c7d05007d88121f64f3909))
- **api:** Make idempotency scopes unambiguous across guards and prefixes ([a58f81c](https://github.com/AlexGalhardo/vukem/commit/a58f81cece8f5d45050bcda7f75f5e3167548d8d))
- **api:** Keep default query limits for non-positive values ([9cd965e](https://github.com/AlexGalhardo/vukem/commit/9cd965e0f9a3ea8269053afb22348f8489fd13c3))
- **vite:** Serve public files from the app while the dev server runs ([c73b941](https://github.com/AlexGalhardo/vukem/commit/c73b94104ace2b1d2d306a38119d8e9abb3fa63a))
- **cli:** Load htmx before the vite entry in starter layouts ([450f18e](https://github.com/AlexGalhardo/vukem/commit/450f18e2322f40c3f3cbb7166f385f1386bf8452))
- **cli:** Create the social user before returning it ([7f2b27f](https://github.com/AlexGalhardo/vukem/commit/7f2b27fc1a1f620f7d70a74d97bae3ec0dca8a56))
- **cli:** Let the app's language catalog and English subjects back auth messages ([fec8ced](https://github.com/AlexGalhardo/vukem/commit/fec8cede458bb4a17241417ade101726382b938d))
- **session:** Never resurrect revoked sessions and harden legacy re-key ([ed1a998](https://github.com/AlexGalhardo/vukem/commit/ed1a998e8a585665a500420850eab33465b20b96))
- **auth:** Namespace the session owner by guard ([5c9170a](https://github.com/AlexGalhardo/vukem/commit/5c9170a8d85b73fb3003a6a78fee985dfaa36bcc))
- **cli:** Flash recovery codes before revoking sessions on 2fa enable ([e20e534](https://github.com/AlexGalhardo/vukem/commit/e20e5341beabbf3e276102bb33b7b327b542580b))
- **BREAKING:** **queue:** Refuse delays on sync and dispatch after commit without TxPusher ([01d1f13](https://github.com/AlexGalhardo/vukem/commit/01d1f13bdeae283362fb2a97d9e16468bc78e51e))
- **cli:** Queue the password reset mail in the auth scaffold ([da122af](https://github.com/AlexGalhardo/vukem/commit/da122af3447aa374080d2691736e82f683133041))
- **database:** Keep Tx comparable ([07e5add](https://github.com/AlexGalhardo/vukem/commit/07e5add84bfaffc3479bd0f897758e27bb9d0ed8))
- **mail:** Keep Mailer comparable ([74d7ef5](https://github.com/AlexGalhardo/vukem/commit/74d7ef596b86cd6a00bac0ae6187a2a90988c84a))
- **database:** Isolate AfterCommit hook panics and reject foreign Tx ([6acdbd0](https://github.com/AlexGalhardo/vukem/commit/6acdbd0710c28c9b92c42916186b4a437f4bf4a6))
- **app:** Turn a WithJobsFunc panic into the Queue error ([336f95c](https://github.com/AlexGalhardo/vukem/commit/336f95cdb73cbef2e78d0639d49dff46388617a5))
- **BREAKING:** **app:** Refuse clear-text queued mail jobs by default ([af48ee4](https://github.com/AlexGalhardo/vukem/commit/af48ee40dd8966855e0181a0f05bc41629ac00f9))
- **vukem:** Type the readiness body and log write errors ([82f8a76](https://github.com/AlexGalhardo/vukem/commit/82f8a7621eeca2015cff99b659a5116c63b45c35))
- **BREAKING:** **signed,social:** Refuse signing keys shorter than 32 bytes ([1308ec2](https://github.com/AlexGalhardo/vukem/commit/1308ec2320ea277c91fd681cc984cd6f98eaf12c))
- **vukem:** Tolerate a missing APP_KEY only with an explicit development or test APP_ENV ([39e64d5](https://github.com/AlexGalhardo/vukem/commit/39e64d5325902fb5252bd844fa60406b1014ae33))
- **cli:** Derive scaffold signing keys with app.DeriveKey ([b846bd5](https://github.com/AlexGalhardo/vukem/commit/b846bd59cdf744af0c54873fd3f0d18dcaaa2e1d))
- **vukem:** Run at most one goroutine per health check ([cd7941c](https://github.com/AlexGalhardo/vukem/commit/cd7941ca0f3e2287dd8666b59f462171237602fe))
- **vukem:** Reject nil lazy stores and detect the memory cache through any option ([eb51232](https://github.com/AlexGalhardo/vukem/commit/eb5123290e631c7c6799614b7173dabc381e1d03))
- **upload:** Log presign response write errors ([4338d07](https://github.com/AlexGalhardo/vukem/commit/4338d07250f9b65c4882c7a269c0ceee06e076f3))
- **vukem:** Bracket IPv6 hosts in the listen address ([0c9f8c2](https://github.com/AlexGalhardo/vukem/commit/0c9f8c2de72b8feb13ea559d7094bac1fd76f990))
- **web:** Return client IPs from headers in canonical form ([113d0ff](https://github.com/AlexGalhardo/vukem/commit/113d0ffb1669af74b7aeaa0d81bd4741c0678171))
- **middleware:** Require one X-Forwarded-Proto line and trusted proxies ([50ca1a4](https://github.com/AlexGalhardo/vukem/commit/50ca1a48b804d3b097fc8ed81718d7183df0c797))

### Security

- **api:** State that op.security does not authenticate the route ([7cd989c](https://github.com/AlexGalhardo/vukem/commit/7cd989c4952c5d9dceae24deb686f46b37af2079))

## [0.23.0] - 2026-10-17

### Added

- **webhook:** Add signed outgoing webhooks with retries and delivery log ([6706466](https://github.com/AlexGalhardo/vukem/commit/670646646d621cfd9266d698b1ece735e2fd8735))
- **postgres:** Add pgtest helper for per-test databases ([c7d563c](https://github.com/AlexGalhardo/vukem/commit/c7d563cdb7a384cb088b5000d2665fe2f0497899))
- **vukem:** Add key:generate app command ([95d074a](https://github.com/AlexGalhardo/vukem/commit/95d074a622f4660af2baf93d1d14aebc05bd5f3c))
- **imaging:** Add contrib/imaging for safe image thumbnails ([ddd6588](https://github.com/AlexGalhardo/vukem/commit/ddd658883517fd57d34ae640075a22f787ba378d))
- **ratelimit:** Add a fixed-window limiter over a shared counter ([5da8705](https://github.com/AlexGalhardo/vukem/commit/5da87059fff3fe9a018ed9eb1f3de4b570b68edf))
- **app:** Choose the rate limit store with RATE_LIMIT_STORE ([a3d7737](https://github.com/AlexGalhardo/vukem/commit/a3d7737993ad05d97ba505441398287aaf8fd7e9))
- **cli:** Build scaffold rate limiters from RATE_LIMIT_STORE ([9ee7034](https://github.com/AlexGalhardo/vukem/commit/9ee7034e7f5185aa5bc1a34d6f4d84ffe2ebd06c))
- **app:** Prune expired database cache entries hourly ([a1f9c0d](https://github.com/AlexGalhardo/vukem/commit/a1f9c0d48b613e8b7d2b1ad2fba369cfb9355707))
- **app:** Find the cache pruner through wrapped stores ([8c1bd87](https://github.com/AlexGalhardo/vukem/commit/8c1bd876fb4c4814d7fba5069670c2935e979a5d))
- **site:** Add last-updated dates and a pt-BR locale ([e06cde3](https://github.com/AlexGalhardo/vukem/commit/e06cde3cde2d6cd5df54062a0aaeca8d66ddb110))

### Changed

- **cache:** Increment database counters in one statement ([7a4814e](https://github.com/AlexGalhardo/vukem/commit/7a4814e84a65713df9870cee86eb202fafa1a5a1))
- **site:** Remove the docs version switcher ([9ebc88d](https://github.com/AlexGalhardo/vukem/commit/9ebc88d133d7879c56f3c211a2592713f04463a6))

### Fixed

- **webhook:** Harden outgoing deliveries after review ([91fd77b](https://github.com/AlexGalhardo/vukem/commit/91fd77bd00cefb33e7978c73a197d40136e2af6e))
- **postgres:** Make pgtest cleanup robust to reconnecting pools ([1493ae4](https://github.com/AlexGalhardo/vukem/commit/1493ae433db0e97593464f3611d31ab34214a766))
- **vukem:** Send key:generate diagnostics to stderr ([7ceadeb](https://github.com/AlexGalhardo/vukem/commit/7ceadebf10a862a4307b431579b5ce74bf875316))
- **imaging:** Apply review findings to thumbnails ([2e33e83](https://github.com/AlexGalhardo/vukem/commit/2e33e83a005e9298cb38f68c1a10cb16a53c6ea8))
- **cache:** Make database Increment atomic across connections ([96eff2c](https://github.com/AlexGalhardo/vukem/commit/96eff2c952810e6b2fb13a77b488903e07d9aedb))
- **ratelimit:** Hash every window key and validate limiter names ([162b6fc](https://github.com/AlexGalhardo/vukem/commit/162b6fc93a14342016b30d6963b3af4b601a9daf))
- **app:** Refuse RATE_LIMIT_STORE=cache over the memory cache ([e977575](https://github.com/AlexGalhardo/vukem/commit/e97757518a47a011011c2adb18911cf51e3d113c))

### Security

- **webhook:** Document outgoing webhooks and their security guarantees ([eb9127b](https://github.com/AlexGalhardo/vukem/commit/eb9127baf398ff7e7bf9e6121628e342377584c0))
- **app:** Check unknown RATE_LIMIT_STORE values as a security default ([6e05120](https://github.com/AlexGalhardo/vukem/commit/6e05120b6fed51b4875531b9ad317ceaa2847a98))
- **ratelimit:** Document shared rate limits and RATE_LIMIT_STORE ([2a790ca](https://github.com/AlexGalhardo/vukem/commit/2a790ca24a57a931b3760def6228cca33e8c9a40))

## [0.22.0] - 2026-10-17

### Added

- **auth:** Add owner-scoped Tokens.RevokeFor and Tokens.List ([08e001d](https://github.com/AlexGalhardo/vukem/commit/08e001d3476bd3fb4afe8128ba13adbf98923ce9))
- **webhook:** Add Standard Webhooks verifier and event dedupe ([bc69938](https://github.com/AlexGalhardo/vukem/commit/bc699383e60abcadd01de6813830e310d4781c17))
- **inertia:** Share locale and translations with I18n middleware ([9813474](https://github.com/AlexGalhardo/vukem/commit/9813474ab999748209620799036aa73cbe0bc78b))
- **upload:** Cap image dimensions to stop decompression bombs ([d1bb6b8](https://github.com/AlexGalhardo/vukem/commit/d1bb6b8020caa2cf6a8578f42fa1a292efceb84a))
- **auth:** Redirect unverified browsers to the verification notice ([230c19c](https://github.com/AlexGalhardo/vukem/commit/230c19cfd017a5c525b9954afd136b2b5adb4d11))
- **cli:** Detect the JavaScript package manager from the lockfile ([317867f](https://github.com/AlexGalhardo/vukem/commit/317867f95cad29de6704acfd51999568e10399be))
- **cli:** Tailwind-only Inertia starters with class-based dark mode ([d7f908d](https://github.com/AlexGalhardo/vukem/commit/d7f908d7785433a2756f68190206e7546d3246b9))

### Fixed

- **queue:** Settle database jobs only while holding the reservation ([d9655c2](https://github.com/AlexGalhardo/vukem/commit/d9655c2d04d3400936c468f977dff7a7d4a1ea54))
- **cache:** Store database cache values as BYTEA on Postgres ([64c3d20](https://github.com/AlexGalhardo/vukem/commit/64c3d204a77de1e827a6ffd9d3f525cbcb1d97a6))
- **session:** Do not create a session for read-only visits ([e08a266](https://github.com/AlexGalhardo/vukem/commit/e08a266f162adde5b7709ba08154f2573556eafa))
- **vukemtest:** Send the app's asset version from Inertia("") ([b0c348d](https://github.com/AlexGalhardo/vukem/commit/b0c348d167583559e37b1b2dbe04f5dcdf53c4d4))
- **vukemtest:** Keep GetJSON's Accept header to that request ([54b6716](https://github.com/AlexGalhardo/vukem/commit/54b67161a175286322342c65a06581c81fdebdae))
- **cli:** Use a real PNG header in the starter upload test ([50994e0](https://github.com/AlexGalhardo/vukem/commit/50994e04976132a5b2717a10840e788f4fb29c15))
- **cli:** Embed starter resources when APP_ENV=production ([c5c6d54](https://github.com/AlexGalhardo/vukem/commit/c5c6d547d7a17f011290416ed695af136f67f180))
- **cli:** Render the 2FA QR code as an image, not raw HTML ([61b3696](https://github.com/AlexGalhardo/vukem/commit/61b3696afda6c3caca648777dcfb77e73b513711))

### Security

- **api:** Refuse idempotency keys with an empty caller scope ([9963ad9](https://github.com/AlexGalhardo/vukem/commit/9963ad90c9f050e442e7e60d8533678f53342dc5))
- **session:** Prune expired database sessions hourly ([043c3a4](https://github.com/AlexGalhardo/vukem/commit/043c3a4ae19e095959dc5d0c20dee72762d844b8))
- **vukemtest:** Add AssertHeaderContains and AssertHeaderMissing ([fa713e9](https://github.com/AlexGalhardo/vukem/commit/fa713e9ebbf23420716d673c8eb9d302142a3869))

## [0.21.1] - 2026-10-17

### Added

- **logging:** Aggregate repeated identical errors ([023820b](https://github.com/AlexGalhardo/vukem/commit/023820bc3cd5049a2f066a219d8ccbaa320b05c8))
- **otel:** Add trace and span ids to log records ([48a79fd](https://github.com/AlexGalhardo/vukem/commit/48a79fd3ae4b36bc815e57fc24c99df49b257ea2))

### Fixed

- **crypt:** Bound integer arithmetic flagged by CodeQL ([db3662c](https://github.com/AlexGalhardo/vukem/commit/db3662cf1141aa1c58b8453c7dfe08d985232cdf))

### Security

- **cli:** Pin govulncheck as a go tool in new apps ([a56c203](https://github.com/AlexGalhardo/vukem/commit/a56c20384518ac88ab665647d4c94412d08da450))

## [0.21.0] - 2026-10-17

### Added

- **site:** Version the docs per minor with a header switcher ([77ffd25](https://github.com/AlexGalhardo/vukem/commit/77ffd25eec967a3590231a731bedeba142959b55))
- **examples:** Add the JSON API example with typed task routes ([b40242e](https://github.com/AlexGalhardo/vukem/commit/b40242e342b81377d1b1c9a4880f7a81732942f6))
- **examples:** Add the SaaS example with Inertia, auth and billing ([27129b8](https://github.com/AlexGalhardo/vukem/commit/27129b8721326954d7e8725510b76026da898158))

### Changed

- **BREAKING:** **dbtest:** Unexport the conformance fixture models ([0e2650a](https://github.com/AlexGalhardo/vukem/commit/0e2650a7810c159b74ae93c7190a8c36cb3e8c36))

### Deprecated

- **orm:** Rename create hook interfaces to BeforeCreator and AfterCreator ([acdab3e](https://github.com/AlexGalhardo/vukem/commit/acdab3e5065ccd5674fa0ae60f1c4e99c4f5bf3e))
- **mail:** Rename WithAttachments to Attachable ([3fb8926](https://github.com/AlexGalhardo/vukem/commit/3fb89261cea70b3344b4db167cb0323d9e1e444e))

### Fixed

- **web:** Reject same-host redirects to scheme-relative paths ([56dd98e](https://github.com/AlexGalhardo/vukem/commit/56dd98eaa3388e2949650eb5f2745cbca296c53e))
- **graphql:** Bound introspection selections ([f04dd80](https://github.com/AlexGalhardo/vukem/commit/f04dd804facb20bff184357a2e409057aec13af5))
- **graphql:** Mask 4xx errors that wrap a cause ([c3aa7e4](https://github.com/AlexGalhardo/vukem/commit/c3aa7e4195a1fe9bf293e2df2b37899a4e32ff0e))
- **web:** Read every X-Forwarded-For line in Ctx.IP ([8188ae4](https://github.com/AlexGalhardo/vukem/commit/8188ae4275fbe4779b604012018f31f11bd8f496))
- **middleware:** Parse the media type before treating a body as JSON ([c5cb0d6](https://github.com/AlexGalhardo/vukem/commit/c5cb0d61a110ce95c3a3c0d4dc6f1f0b018ecb27))
- **httpclient:** Block IPv6 ranges that embed IPv4 addresses ([4254b55](https://github.com/AlexGalhardo/vukem/commit/4254b55e5cf3f92201ac56fd22b8d6fef61da410))
- **grpc:** Hide the text of server-fault connect errors ([30e98a9](https://github.com/AlexGalhardo/vukem/commit/30e98a992941c2e06dc2680ecb3b7e1733b499e6))
- **BREAKING:** **app:** Refuse to boot with an unknown APP_ENV ([4824717](https://github.com/AlexGalhardo/vukem/commit/48247172b537620beaaf0469c3df972c5ad94974))
- **BREAKING:** **ratelimit:** Key by client IP unless a verified token is available ([994276b](https://github.com/AlexGalhardo/vukem/commit/994276b1dcdc3468360715949835015befc9844f))
- **cli:** Require the second factor for generated API tokens ([c34e04e](https://github.com/AlexGalhardo/vukem/commit/c34e04e68ed07c37329662a3bb45b3a4379491e2))
- **cli:** Normalize login throttle keys and revoke tokens on reset ([1ef3674](https://github.com/AlexGalhardo/vukem/commit/1ef3674db4ec75eb56c5c2f4f5125a3d84485603))
- **upload:** Verify only keys that Presign could have issued ([31f6178](https://github.com/AlexGalhardo/vukem/commit/31f6178f8282a52bc22933c3d1e961127dc88cd7))
- **upload:** Strip format characters from sanitized file names ([5077f1c](https://github.com/AlexGalhardo/vukem/commit/5077f1c3833eab28ce37bf2c1501149c547197dd))
- **upload:** Count nameless multipart parts toward the limits ([5897d11](https://github.com/AlexGalhardo/vukem/commit/5897d11d04f2142c9c419d926a8cbdcc907817a6))
- **middleware:** Handle an empty path in the maintenance bypass check ([95a19ba](https://github.com/AlexGalhardo/vukem/commit/95a19ba7c34db82390e40a01e876e358730f1245))
- **mail:** Reject attachment content types with line breaks ([6396868](https://github.com/AlexGalhardo/vukem/commit/63968683eb7c358f04ea1d2feab2f5eaad35570f))
- **auth:** Consume remember-me tokens atomically ([9afea58](https://github.com/AlexGalhardo/vukem/commit/9afea586e9343d733096457c7658e37ebed57c31))

### Security

- **web:** Add package overviews and examples for the http core ([f2d6475](https://github.com/AlexGalhardo/vukem/commit/f2d64754fe6ed4ba3a9412753537b18026986ba6))
- **repo:** Publish the 1.x stability policy, release cadence and upgrade guides ([e317f16](https://github.com/AlexGalhardo/vukem/commit/e317f166c8c4f1cc7039a33320e7aeff7992929b))
- **repo:** Rename the agent security rules to secure-coding.md ([3d4003b](https://github.com/AlexGalhardo/vukem/commit/3d4003b3b4c7e27279e6844952f2765f2b2d0b66))
- **BREAKING:** **hash:** Replace DefaultArgon2 var with DefaultArgon2Params ([0405c29](https://github.com/AlexGalhardo/vukem/commit/0405c29fbdec325c30742c828f07cc44b26fae9b))
- **repo:** Add fuzz targets for untrusted-input parsers ([231f5e9](https://github.com/AlexGalhardo/vukem/commit/231f5e93feb03bb39eefdd7a85f7e917d629da73))
- **web:** Check the cleaned redirect path in SafeRedirect ([bdf0fbe](https://github.com/AlexGalhardo/vukem/commit/bdf0fbe36b365f50be22f783870eadab85296670))
- **storage:** Hide dot directories and sandbox served files ([88da87a](https://github.com/AlexGalhardo/vukem/commit/88da87a0692ef3c2a04d010278905b92e6bd33cd))
- **repo:** Name the test or mechanism behind every security default ([e31231c](https://github.com/AlexGalhardo/vukem/commit/e31231c8f082078c1031e8d45dfed6eebef536ef))
- **security:** Record the B.5.2 security review ([8ebefd8](https://github.com/AlexGalhardo/vukem/commit/8ebefd8fed76687ccc048238db823004a578613f))

## [0.20.1] - 2026-10-17

### Changed

- **web:** Allocate the response writer inside Ctx and add WriteString ([aa08aba](https://github.com/AlexGalhardo/vukem/commit/aa08aba42c66c1aa8c6ce07714408ae6c7884432))
- **orm:** Reuse scan buffers across rows and presize limited Get ([b1b628b](https://github.com/AlexGalhardo/vukem/commit/b1b628b349d75e988019214c4d6b4ddbe07c2f56))

### Fixed

- **release:** Ignore driver and contrib tags when finding the previous release ([c71f5fe](https://github.com/AlexGalhardo/vukem/commit/c71f5fe49df35b7f61e36f87dcab563a12eec4d4))

### Security

- **docs:** Document release signing, sboms and verification ([8b8c995](https://github.com/AlexGalhardo/vukem/commit/8b8c995a1a0aa7a4fd4418bb1d32b83a98740478))

## [0.20.0] - 2026-10-17

### Added

- **graphql:** Add the contrib/graphql module with lexer and parser ([fc7fa9d](https://github.com/AlexGalhardo/vukem/commit/fc7fa9d8d2e367bf1cb839efa01da8704bfcd6ae))
- **graphql:** Add schemas, validation, execution and introspection ([85a1b9a](https://github.com/AlexGalhardo/vukem/commit/85a1b9ab14c6ed38e4a1651adc2bd37e00b93735))
- **graphql:** Add the batching loader with ORM helpers ([68a6a36](https://github.com/AlexGalhardo/vukem/commit/68a6a36adb9fb9895d4fbffbeab5d99bfa86047a))
- **cli:** Add install:billing for Stripe subscriptions ([a6295fd](https://github.com/AlexGalhardo/vukem/commit/a6295fd791ce4de54a36deda63f5951a56fa3c98))
- **graphql:** Add subscriptions over graphql-ws and SSE ([fbf13ae](https://github.com/AlexGalhardo/vukem/commit/fbf13ae4de7638db547d859b63fe7b0d41cd7149))
- **app:** Serve http/2 cleartext outside production ([5724c54](https://github.com/AlexGalhardo/vukem/commit/5724c54e48d9ecffffbd0fa4a83e31feac644c38))
- **grpc:** Add the contrib/grpc module on connect-go ([3bfc1d2](https://github.com/AlexGalhardo/vukem/commit/3bfc1d2f13e2de929251147053788ecfc4608fbd))
- **cli:** Add make:proto and codegen:rpc ([30147cb](https://github.com/AlexGalhardo/vukem/commit/30147cb94638cb386f7225c4e87b9790b08a4cad))
- **admin:** Add the admin panel package ([28a8c8c](https://github.com/AlexGalhardo/vukem/commit/28a8c8c13612de56b8bf8ba1df334c0ac4949df5))
- **cli:** Add install:admin and make:admin-resource ([ba460a5](https://github.com/AlexGalhardo/vukem/commit/ba460a5ee41fe0fdd3b6048f92d6fd3cf14ec2c6))

### Changed

- **BREAKING:** **grpc:** Name the production reflection opt-out insecure ([724a841](https://github.com/AlexGalhardo/vukem/commit/724a8411895ccd066a72cdd6f2038d4338529690))

### Fixed

- **session:** Flush through writers that only implement Unwrap ([bfae772](https://github.com/AlexGalhardo/vukem/commit/bfae77267038292008dd181602fafb3ce14fefd1))

### Security

- **graphql:** Serve GraphQL over HTTP with persisted queries ([08527d5](https://github.com/AlexGalhardo/vukem/commit/08527d5d434f6717daf2209c41dd2f4e808b4c3a))
- **graphql:** Document the contrib/graphql module ([e966941](https://github.com/AlexGalhardo/vukem/commit/e9669411fbf1bafd4674f1b9e377ff643cb51512))

## [0.19.0] - 2026-10-16

### Added

- **social:** Add Apple, LinkedIn and X sign-in ([79df84d](https://github.com/AlexGalhardo/vukem/commit/79df84d23921cdbbfa620c9da9f2adddbfcfdf79))
- **stripe:** Add the Stripe API client and webhook module ([fc3b71b](https://github.com/AlexGalhardo/vukem/commit/fc3b71bd2af8a8e68120dd4aadd7680c36fa7519))
- **s3:** Add the S3 storage driver module ([4f8294a](https://github.com/AlexGalhardo/vukem/commit/4f8294a3cc43a2de7f04fc947e82e33e1f37382f))
- **database:** Add the mysql dialect and a url-to-dsn driver hook ([6ac1996](https://github.com/AlexGalhardo/vukem/commit/6ac199694d4fce6eef12c11b6885c72076b2038f))
- **schema:** Generate mysql ddl and drop tables on mysql ([a543b86](https://github.com/AlexGalhardo/vukem/commit/a543b8603f2ed42957e69babd22680be314cb85d))
- **orm:** Support the mysql dialect in inserts and queries ([87c0e0a](https://github.com/AlexGalhardo/vukem/commit/87c0e0ad56812d44273aa177036dc91040259148))
- **queue:** Run the database queue on mysql ([2a1f0ff](https://github.com/AlexGalhardo/vukem/commit/2a1f0ffb12f165d97d947bc4f179020ce043af5e))
- **cache:** Run the database cache store on mysql ([7729bf4](https://github.com/AlexGalhardo/vukem/commit/7729bf4f383e8e1130b5dd96c51df9a40bd9af76))
- **session:** Add a mysql option to the sql session store ([9519aee](https://github.com/AlexGalhardo/vukem/commit/9519aee6eb4d2958cc5784b1b39e30a86e31915f))
- **boost:** Read the database schema on mysql ([3c431da](https://github.com/AlexGalhardo/vukem/commit/3c431dae7e76041e856d27cfbb1d7a77738ad38f))
- **mysql:** Add the drivers/mysql module for mysql 8 and mariadb 11 ([6fd67f0](https://github.com/AlexGalhardo/vukem/commit/6fd67f0de9d42c2a46faf562d579998cba21f2c7))
- **openapi:** Generate typed TypeScript and Go API clients ([7c36729](https://github.com/AlexGalhardo/vukem/commit/7c36729ec542568f324a30225a5135962c39c0be))
- **cli:** Add the codegen:client app command ([d7e4ad4](https://github.com/AlexGalhardo/vukem/commit/d7e4ad44f46c2d403fa966878cd98ccfa1ba79bc))
- **web:** Add context session helpers and Partial for components ([2b600b5](https://github.com/AlexGalhardo/vukem/commit/2b600b5db115d29f09ee6151916608d3c86d314d))
- **vite:** Carry the Vite instance in the request context ([2dd3849](https://github.com/AlexGalhardo/vukem/commit/2dd3849b18e537c62b9b6926b025fd5aee91507f))
- **cli:** Add the templ starter ([be88bcd](https://github.com/AlexGalhardo/vukem/commit/be88bcd053e946d6e656081a67aade7a467dec9e))
- **database:** Add a query observer hook ([9d47072](https://github.com/AlexGalhardo/vukem/commit/9d4707235ae594813b6c1b71f5143238f87133a4))
- **queue:** Add a job observer hook ([3caf7c2](https://github.com/AlexGalhardo/vukem/commit/3caf7c28fa1232586f2fc1427da17212b7c9ea45))
- **events:** Add a dispatch observer hook ([8a4952a](https://github.com/AlexGalhardo/vukem/commit/8a4952a7640071868c2500503d96623249ebf01a))
- **devtools:** Add the /_vukem development dashboard ([ea5bb39](https://github.com/AlexGalhardo/vukem/commit/ea5bb39e78f2678f1bf78bf95ab39831b4621b84))
- **broadcast:** Add channel broadcasting with an SSE transport ([c9d297f](https://github.com/AlexGalhardo/vukem/commit/c9d297fd228c8c9597e3af6501de6a69ad05dee6))
- **websocket:** Serve the broadcast protocol over WebSockets ([e3319f1](https://github.com/AlexGalhardo/vukem/commit/e3319f1accfd735863468ec22de281700044b985))
- **web:** Let a route body limit replace the router default ([3b7b0c0](https://github.com/AlexGalhardo/vukem/commit/3b7b0c0487d799daa5b68e8c8f95631e080408fb))
- **BREAKING:** **middleware:** Read _method from the query for multipart forms ([2aee55f](https://github.com/AlexGalhardo/vukem/commit/2aee55f8b2f15191293d78f96f337b1203440b0d))
- **z:** Add z.File for uploaded files ([eee6e80](https://github.com/AlexGalhardo/vukem/commit/eee6e80f8830234af0a686b5023718b50e873e7a))
- **cli:** Add an upload demo with progress to the HTMX starter ([9f7966d](https://github.com/AlexGalhardo/vukem/commit/9f7966d65bc16fc8294ef08c377a50f89ec75e60))
- **cli:** Add install:auth --2fa for TOTP two-factor authentication ([443cbfa](https://github.com/AlexGalhardo/vukem/commit/443cbfab652ebe01b038c37f27c1ef801701b631))

### Fixed

- **cli:** Drop the html/template upload demo from the templ starter ([cd40371](https://github.com/AlexGalhardo/vukem/commit/cd4037105913390e52ce14b1d6380e892c6a4ab9))

### Security

- **websocket:** Add the WebSocket driver module ([02b4f59](https://github.com/AlexGalhardo/vukem/commit/02b4f597d235dddcd0969b26a7b1b82c20d7c421))
- **upload:** Add streaming, resumable and direct uploads ([c201582](https://github.com/AlexGalhardo/vukem/commit/c2015821d50f8ad5321f8cd5a4a1751533f99795))

## [0.18.0] - 2026-10-16

### Added

- **vite:** Add the vite package and view tag functions ([a118c48](https://github.com/AlexGalhardo/vukem/commit/a118c48fd7ad189d158fd4e445343a3dc2e39d99))
- **inertia:** Add the Inertia v3 core adapter ([b7eb1cd](https://github.com/AlexGalhardo/vukem/commit/b7eb1cd864aaed4e8e4bcf1d28d873b26f15b37d))
- **cli:** Add Vukem Boost for coding agents ([979619a](https://github.com/AlexGalhardo/vukem/commit/979619ac4e7a21d65c964138e86ee0d09cc15e4a))
- **inertia:** Add prop types and partial reload filtering ([bc79c40](https://github.com/AlexGalhardo/vukem/commit/bc79c40f3677a2364d30b41c42e15e0224d45c45))
- **inertia:** Add history encryption, flash, prefetch and precognition ([bfde089](https://github.com/AlexGalhardo/vukem/commit/bfde0897cc9eec4ffe898c925e321d56cb896e63))
- **inertia:** Add the SSR client with client-side fallback ([0b1de61](https://github.com/AlexGalhardo/vukem/commit/0b1de619e19b3a6c50c85ecf9afee97a1ec7671b))
- **cli:** Ship Vite and Tailwind v4 in the HTMX starter and prompt for the stack ([810e832](https://github.com/AlexGalhardo/vukem/commit/810e832592f57775ea36724667405cc66d3c2781))
- **vukemtest:** Add WithInertia and Inertia assertions ([c850fdb](https://github.com/AlexGalhardo/vukem/commit/c850fdb555793ca8c7704da34cc54c45ee3da828))
- **cli:** Add codegen:routes for typed frontend routes ([28ed3b5](https://github.com/AlexGalhardo/vukem/commit/28ed3b5cc4c89e9546c4c9f315def8fc1450adf6))
- **cli:** Add the Inertia React starter and install:inertia ([9e671dc](https://github.com/AlexGalhardo/vukem/commit/9e671dc6c6c77f84ba30be1ee795caf9e56bab0c))
- **cli:** Add Inertia auth pages to install:auth ([907a4cc](https://github.com/AlexGalhardo/vukem/commit/907a4cc4c957a5df1b68000aa82231c5566f4c44))
- **web:** Add a youch-style debug error page ([a6499ea](https://github.com/AlexGalhardo/vukem/commit/a6499ea10483e73cfd24715733bec27fdf1af229))
- **sse:** Add server-sent event streams and a topic hub ([441b456](https://github.com/AlexGalhardo/vukem/commit/441b4567e8f208e76d0b8261eb7739462671eb0e))
- **cli:** Add maintenance mode with vukem down and up ([684a942](https://github.com/AlexGalhardo/vukem/commit/684a942b1e96b4d65851b415b26286a29c2e17d9))
- **auth:** Add the JWT guard with JWKS rotation and refresh tokens ([55fdb77](https://github.com/AlexGalhardo/vukem/commit/55fdb7755448f6806b5a856832eb1d6a63cce62b))
- **crypt:** Add encryption with key rotation and encrypted cookies ([2ce4c00](https://github.com/AlexGalhardo/vukem/commit/2ce4c005a49f6968d95f8d0b37cb260e692eee18))
- **orm:** Add the Encrypted column type ([2f1d6df](https://github.com/AlexGalhardo/vukem/commit/2f1d6df647f8cf4448c556aaf709c95448c91f3b))
- **z:** Record message keys and localize issues ([8b2a353](https://github.com/AlexGalhardo/vukem/commit/8b2a353b53695f2aaf874384c8db9e63a306b87e))
- **web:** Localize validation issues from the request context ([334454d](https://github.com/AlexGalhardo/vukem/commit/334454db8eae60ebe39479411eab8691ad863201))
- **i18n:** Add JSON catalogs, plurals and locale resolution ([b85a9ad](https://github.com/AlexGalhardo/vukem/commit/b85a9adbea1b0f2edee36c2d958f9e2173ba03f4))
- **view:** Add the t and locale template functions ([5818c2e](https://github.com/AlexGalhardo/vukem/commit/5818c2eed23d086850145ecd2fd6b75da07f6fc8))
- **i18n:** Wire translations into the app ([b9a736e](https://github.com/AlexGalhardo/vukem/commit/b9a736e828ae8597b8b5e80f93a7c12d1637cacf))
- **cli:** Add the Inertia Vue starter ([f35e398](https://github.com/AlexGalhardo/vukem/commit/f35e398bdbf7d42436ee3b710ce50768e2104237))
- **auth:** Add the TOTP package with a stdlib QR encoder ([a117db6](https://github.com/AlexGalhardo/vukem/commit/a117db6a8ed1c72628b883da6c36bcfff9f1b16b))
- **cli:** Add the Inertia Svelte starter ([051cfbe](https://github.com/AlexGalhardo/vukem/commit/051cfbe98e4439f792c374797f1cef97efe50025))

### Fixed

- **site:** Align the docs TOC with the header and drop the pre-1.0 callouts ([d3d0332](https://github.com/AlexGalhardo/vukem/commit/d3d0332e70532821a4a0442f76177f9502f5d0f5))

### Security

- **otel:** Add the OpenTelemetry driver module ([ea81f06](https://github.com/AlexGalhardo/vukem/commit/ea81f0619da67fa54adf056173c5344507cb9748))

## [0.17.0] - 2026-10-16

### Added

- **site:** Add the Vukem icon, align the header and open on the docs ([0eb9623](https://github.com/AlexGalhardo/vukem/commit/0eb962399c1ab6c77e8ddc636d6574b1a2c1c75c))
- **redis:** Add the Redis driver module ([8691588](https://github.com/AlexGalhardo/vukem/commit/86915887cb756525aea72d6f13f4b865e9f2b9ee))
- **mongo:** Add the MongoDB driver module ([7a4d0f3](https://github.com/AlexGalhardo/vukem/commit/7a4d0f304694f85f53f45023394b835c01133142))

### Security

- **repo:** Rewrite the README after the Laravel layout ([90360c0](https://github.com/AlexGalhardo/vukem/commit/90360c01c327263ad344eae3c54ca413315f7d23))

## [0.16.0] - 2026-10-16

### Added

- **queue:** Add typed jobs, workers and the sync and memory drivers ([405c978](https://github.com/AlexGalhardo/vukem/commit/405c9788e65495694310c6f752eaf32777aa74b4))
- **queue:** Add the database driver, DispatchTx and failed-job commands ([91be143](https://github.com/AlexGalhardo/vukem/commit/91be143cd3017d2a4c2fc07914a68c890b4bf5c1))
- **queue:** Add make:job, queue.Fake, queued mail and queued listeners ([305e9ca](https://github.com/AlexGalhardo/vukem/commit/305e9ca94b7a149500de75221179a2baef10adf2))
- **schedule:** Add the scheduler with cron expressions and overlap locks ([e42e3d8](https://github.com/AlexGalhardo/vukem/commit/e42e3d84c6ffff5eafc5eff54fe1f0ebcad4cc16))

## [0.15.0] - 2026-10-16

### Added

- **api:** Add typed handlers with api.Register ([36b8638](https://github.com/AlexGalhardo/vukem/commit/36b8638ea7c6d662f5966409d44deb2984d7ddf7))
- **openapi:** Generate OpenAPI 3.1 documents from the router ([7334aa8](https://github.com/AlexGalhardo/vukem/commit/7334aa8455cea6f1c001fcefbf6c27d37a83eb37))
- **vukemtest:** Assert responses match the OpenAPI document ([186c1e0](https://github.com/AlexGalhardo/vukem/commit/186c1e01dab5d1363a18391202c5569111ad80a5))
- **openapi:** Export JSON Schema and generate TypeScript and Zod types ([e9b2c71](https://github.com/AlexGalhardo/vukem/commit/e9b2c71eb8fc8abd57c14ed173010ea40180fb92))
- **api:** Add the Idempotency-Key middleware ([f11fdd9](https://github.com/AlexGalhardo/vukem/commit/f11fdd96729dc65deb76890104e7233f14d1114e))
- **apidocs:** Serve a Scalar API reference for the OpenAPI document ([e94f6fa](https://github.com/AlexGalhardo/vukem/commit/e94f6fabd24d4a1285814b7c4549feccf6dbbc5c))

## [0.14.0] - 2026-10-16

### Added

- **examples:** Add the blog example and auth.Optional ([5e13d26](https://github.com/AlexGalhardo/vukem/commit/5e13d26aa9d43cb3d7376340f06b0b80beeb1800))

### Security

- **site:** Document every alpha feature ([a1e5d62](https://github.com/AlexGalhardo/vukem/commit/a1e5d622d0f03a808a2e6bb837982772ef325350))

## [0.13.0] - 2026-10-16

### Security

- **security:** Add SSRF-safe client, webhook signatures and security events ([393b165](https://github.com/AlexGalhardo/vukem/commit/393b16543d50e8f212b9ee1af9a3a368fe380fd1))

## [0.12.0] - 2026-10-16

### Added

- **logging:** Add the Telegram slog handler ([8fc0ad3](https://github.com/AlexGalhardo/vukem/commit/8fc0ad3a86d9f6cdc931e2f53c46152f1fd15def))

## [0.11.0] - 2026-10-16

### Added

- **subsystems:** Add cache, file storage and typed events ([b0dceeb](https://github.com/AlexGalhardo/vukem/commit/b0dceeb720dd86fd972dbcef4785f2c3f2cf1330))

## [0.10.0] - 2026-10-16

### Added

- **cli:** Add the JSON API starter and install:api ([253f10f](https://github.com/AlexGalhardo/vukem/commit/253f10fab563bc882c2dbafd5a3fc15622b88f2e))

## [0.9.0] - 2026-10-16

### Added

- **cli:** Add install:auth --social with account linking ([120ea0f](https://github.com/AlexGalhardo/vukem/commit/120ea0faf0da76d708fef626ad1908f42f729021))

## [0.8.0] - 2026-10-16

### Added

- **cli:** Add install:auth with registration, verification and password resets ([e68047e](https://github.com/AlexGalhardo/vukem/commit/e68047e7704e9d8666ebdbf5cc21a6352f40bf0c))

## [0.7.0] - 2026-10-16

### Added

- **mail:** Add mailables with SMTP, Resend, log and array transports ([052df72](https://github.com/AlexGalhardo/vukem/commit/052df7259f4c3356ac3e73fc976eec910ea9915b))

## [0.6.0] - 2026-10-16

### Added

- **authz:** Add gates and policies with fail-closed authorization ([a84543d](https://github.com/AlexGalhardo/vukem/commit/a84543df77529072bbc2161bff527bbc61814728))

## [0.5.0] - 2026-10-16

### Added

- **auth:** Add hashers, session and token guards, and auth middleware ([4a5a95f](https://github.com/AlexGalhardo/vukem/commit/4a5a95f60123d2b2aa71a6befdbdf5a8c8eaa05c))
- **social:** Add OAuth2/OIDC social login with PKCE ([d60638f](https://github.com/AlexGalhardo/vukem/commit/d60638ffd61aa370f46e895df6155fc9c76014dd))

## [0.4.0] - 2026-10-16

### Added

- **orm:** Add relations, factories, seeders and database rules ([0ca2389](https://github.com/AlexGalhardo/vukem/commit/0ca2389816ea75ced1e49059970022aec714d9fb))

## [0.3.0] - 2026-10-16

### Added

- **postgres:** Add the PostgreSQL driver module and conformance suite ([8283d33](https://github.com/AlexGalhardo/vukem/commit/8283d337af67ece45ab98710c77fa5b74f25a9f7))

### Changed

- **BREAKING:** Rename the framework to Vukem ([f7e19e0](https://github.com/AlexGalhardo/vukem/commit/f7e19e00e863584d240f609b14e4ad11fb1eb92d))

## [0.2.0] - 2026-10-16

### Added

- **api:** Add REST toolkit with pagination, query conventions and rate limiting ([6e779bd](https://github.com/AlexGalhardo/vukem/commit/6e779bd237b0796f3bb5a1352783309eb5d38423))

## [0.1.1] - 2026-10-16

### Added

- **cli:** Answer JSON from the starter's post controller ([fc2cf50](https://github.com/AlexGalhardo/vukem/commit/fc2cf5018c6b8f9ee49f22a443bf40cdb83fc14d))

## [0.1.0] - 2026-10-16

### Changed

- Maintenance release (no user-facing changes).

## [0.0.12] - 2026-10-16

### Added

- **BREAKING:** **cli:** Add vukem new with the HTMX + SQLite starter ([92d44b2](https://github.com/AlexGalhardo/vukem/commit/92d44b29bb21104f5fbd79028102c21b94949d94))

## [0.0.11] - 2026-10-16

### Added

- **cli:** Add the vukem command with generators, serve, build and delegation ([0abc848](https://github.com/AlexGalhardo/vukem/commit/0abc8480357a7f4ac495e243c3cb7675f38e0aec))

## [0.0.10] - 2026-10-16

### Added

- **vukemtest:** Add testing toolkit with fluent client and DB assertions ([c3a9400](https://github.com/AlexGalhardo/vukem/commit/c3a940067e1431fe89243f0a72838ef9489dec45))

## [0.0.9] - 2026-10-16

### Added

- **view:** Add html/template views, htmx 4 helpers and fragments ([e45d36e](https://github.com/AlexGalhardo/vukem/commit/e45d36e9dd67282aa1453ee09c04fac498a6cd47))

## [0.0.8] - 2026-10-16

### Added

- **orm:** Add database connections, generic query builder and migrations ([2d17f79](https://github.com/AlexGalhardo/vukem/commit/2d17f79bc819e7a49d6a0ceb4b616ab45202003e))

## [0.0.7] - 2026-10-16

### Added

- **session:** Add sessions, flash, old input and csrf protection ([a62538f](https://github.com/AlexGalhardo/vukem/commit/a62538fbb031986d94d1e70cfebb9f4313ea26ec))

## [0.0.6] - 2026-10-16

### Added

- **z:** Add zod-style typed schemas and web parsing ([23b1438](https://github.com/AlexGalhardo/vukem/commit/23b14380bb39f1774129f5ff72d4a293d770b867))
- **site:** Add documentation site with Next.js 16, Tailwind v4 and Base UI ([aaa45f6](https://github.com/AlexGalhardo/vukem/commit/aaa45f6a80ebb483efa917a588f071e4bb2f9349))

### Fixed

- **deps:** Require go1.27.2 toolchain for standard library security fixes ([bcab5a8](https://github.com/AlexGalhardo/vukem/commit/bcab5a825ca907dd9b20c55e9d1d9a3eb44c61da))

## [0.0.5] - 2026-10-16

### Added

- **middleware:** Add built-in middleware, app HTTP wiring and commands ([2af9fd4](https://github.com/AlexGalhardo/vukem/commit/2af9fd4c992b12eddc4a47785f5e2c801ea545cf))

## [0.0.4] - 2026-10-16

### Added

- **router:** Add ServeMux router, web.Ctx, errs and problem details ([9ba13bd](https://github.com/AlexGalhardo/vukem/commit/9ba13bd1580847fd0a224fb649530bcf370f9263))

## [0.0.3] - 2026-10-16

### Added

- **app:** Add App, provider lifecycle, runners and HTTP server ([255eee4](https://github.com/AlexGalhardo/vukem/commit/255eee484f01db8ee6cb1ffdb4a0196f7df1f878))

## [0.0.2] - 2026-10-16

### Added

- **config:** Add .env parser, typed struct loading, secrets and app key ([736332a](https://github.com/AlexGalhardo/vukem/commit/736332a10e04c12565158579e061bee66ea627ef))

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

[Unreleased]: https://github.com/AlexGalhardo/vukem/compare/v0.43.0...HEAD
[0.43.0]: https://github.com/AlexGalhardo/vukem/compare/v0.42.0...v0.43.0
[0.42.0]: https://github.com/AlexGalhardo/vukem/compare/v0.41.0...v0.42.0
[0.41.0]: https://github.com/AlexGalhardo/vukem/compare/v0.40.0...v0.41.0
[0.40.0]: https://github.com/AlexGalhardo/vukem/compare/v0.39.1...v0.40.0
[0.39.1]: https://github.com/AlexGalhardo/vukem/compare/v0.39.0...v0.39.1
[0.39.0]: https://github.com/AlexGalhardo/vukem/compare/v0.38.0...v0.39.0
[0.38.0]: https://github.com/AlexGalhardo/vukem/compare/v0.37.0...v0.38.0
[0.37.0]: https://github.com/AlexGalhardo/vukem/compare/v0.36.0...v0.37.0
[0.36.0]: https://github.com/AlexGalhardo/vukem/compare/v0.35.0...v0.36.0
[0.35.0]: https://github.com/AlexGalhardo/vukem/compare/v0.34.0...v0.35.0
[0.34.0]: https://github.com/AlexGalhardo/vukem/compare/v0.33.0...v0.34.0
[0.33.0]: https://github.com/AlexGalhardo/vukem/compare/v0.32.0...v0.33.0
[0.32.0]: https://github.com/AlexGalhardo/vukem/compare/v0.31.0...v0.32.0
[0.31.0]: https://github.com/AlexGalhardo/vukem/compare/v0.30.0...v0.31.0
[0.30.0]: https://github.com/AlexGalhardo/vukem/compare/v0.29.0...v0.30.0
[0.29.0]: https://github.com/AlexGalhardo/vukem/compare/v0.28.0...v0.29.0
[0.28.0]: https://github.com/AlexGalhardo/vukem/compare/v0.27.0...v0.28.0
[0.27.0]: https://github.com/AlexGalhardo/vukem/compare/v0.26.0...v0.27.0
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
