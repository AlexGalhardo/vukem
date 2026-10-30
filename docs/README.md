# Documentation

Design documents, one per area. The user guide lives at [vukem.dev](https://vukem.dev/docs/introduction).

Two sources, one owner per kind of content: `docs/` is canonical for contracts and decisions (security defaults, stability, architecture, API tables, tooling), and `site/content/docs` is canonical for tutorials and how-to pages. When both describe the same API, `docs/` wins and the site links to it or is fixed. Every API table in `docs/` gets a test that compares it with the code (as `web/docs_test.go` and `z/tags_rules_test.go` do), and `snippetcheck` type-checks the site's Go snippets in CI.

| Topic | Read |
| --- | --- |
| Roadmap and current task | [PLAN.md](PLAN.md) |
| Starter guides (HTMX, templ, Inertia, API) | [guides/](guides/README.md) |
| Tutorials (code in `examples/`) | [blog with HTMX](tutorials/blog-htmx.md), [SaaS with Inertia](tutorials/saas-inertia.md), [JSON API with tokens](tutorials/json-api.md) |
| Architecture | [architecture.md](architecture.md) |
| Configuration | [configuration.md](configuration.md) |
| Routing, validation, sessions, errors | [http.md](http.md) |
| Typed schemas (Zod-style validation) | [validation.md](validation.md) |
| REST APIs, OpenAPI, API reference | [api.md](api.md) |
| GraphQL (`contrib/graphql`) | [graphql.md](graphql.md) |
| QR codes (`contrib/qrcode`) | [qrcode.md](qrcode.md) |
| Static site generation | [ssg.md](ssg.md) |
| Security defaults (OWASP Top 10:2025) | [security.md](security.md) |
| Security review (B.5.2, ASVS 5.0 L2) | [security-review.md](security-review.md) |
| Databases, ORM, migrations | [database.md](database.md) |
| Authentication and authorization | [authentication.md](authentication.md) |
| Admin panel | [admin.md](admin.md) |
| Views, HTMX, SSE, WebSockets, broadcasting, Inertia, Vite | [frontend.md](frontend.md) |
| CLI | [cli.md](cli.md) |
| Mail, cache, feature flags, queue, scheduler and more | [subsystems.md](subsystems.md) |
| Stripe subscriptions (`install:billing`) | [billing.md](billing.md) |
| Cloudflare (client IP, origin lock) | [cloudflare.md](cloudflare.md) |
| Development dashboard (`/_vukem`) | [devtools.md](devtools.md) |
| Stability policy and upgrade guides | [stability.md](stability.md), [upgrade/](upgrade/README.md), [pre-1.0 API review](api/review.md), [v1.0 API review](v1-api-review.md) |
| Testing | [testing.md](testing.md) |
| Performance baseline (vs net/http, chi, echo) | [performance.md](performance.md) |
| Tooling and releases | [tooling.md](tooling.md) |
| Maintenance routines, PR flow, Scorecard metrics | [maintenance.md](maintenance.md) |
| Documentation site | [site.md](site.md) |
| Design decisions | [brainstorming.md](brainstorming.md) |
| Research behind the design | [golang-web-search.md](golang-web-search.md) |
