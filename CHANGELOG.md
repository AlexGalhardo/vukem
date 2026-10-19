# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/AlexGalhardo/vukem/compare/v0.30.0...HEAD
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
