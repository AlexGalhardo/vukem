# World-class documentation site workflow

How the docs at <https://vukem.dev> (`site/`) are written, structured and kept current. It distils what makes the [AdonisJS](https://docs.adonisjs.com/introduction), [Laravel](https://laravel.com/docs) and [Resend](https://resend.com/docs/introduction) docs excellent and turns it into rules for Vukem. The short, enforceable version lives in [`.claude/rules/docs-content.md`](../.claude/rules/docs-content.md); [docs/site.md](site.md) covers the site's code and hosting.

## Who we write for

Write for a reader who is learning **Go and Vukem at the same time** — picture a curious ten-year-old with a laptop. Assume they know what a website is and can type commands in a terminal. Do not assume they know Go, HTTP, SQL or how a web framework works. Experienced developers must still find every detail they need: depth and simplicity are not opposites, they are layers.

- Explain **why** before **how**: the problem a feature solves, then the concept, then the code.
- Explain **every** detail the framework exposes: each option, default value, environment variable, error and edge case. If the code does it, the page says it.
- Go concepts the page relies on (interfaces, `context.Context`, errors, goroutines, struct tags, generics) get one plain sentence and a link to the official Go documentation (<https://go.dev/doc>, <https://go.dev/tour>, <https://pkg.go.dev/std>).

## Information architecture

Sidebar categories, in learning order:

| Category | Purpose |
| --- | --- |
| Getting started | Introduction, prerequisites (with a Go primer), installation, starters, the first-app tutorial, directory structure, configuration, showcase |
| Architecture | Request lifecycle, providers, the app container |
| The basics | Routing, middleware, controllers, the HTTP context, requests, responses, validation, errors, static files, sessions, CSRF, views |
| APIs | REST resources, pagination, typed handlers, OpenAPI, clients, idempotency, GraphQL, gRPC |
| Database | Connections, ORM, relations, migrations, factories and seeders, Redis, MongoDB |
| Security | Overview, authentication, social login, JWT, API tokens, 2FA, authorization, rate limiting, headers, signed URLs, encryption, building blocks |
| Digging deeper | Mail, cache, storage, uploads, exports, events, queues, scheduler, SSE, broadcasting, webhooks, localization, billing, admin, feature flags, devtools, OpenTelemetry, Telegram logging |
| Frontend | HTMX, Vite, Inertia, Inertia i18n |
| Testing | The test client, database assertions, fakes |
| Deployment | Docker, Railway, production checklist |
| CLI | Every command, generated files |

Rules:

- **Guides and reference live apart.** Guides teach a task in order; reference pages list every option. A guide links to the reference instead of repeating it.
- **One topic per page.** The page anatomy alone takes about seven H2 sections, so a feature page usually has 10–14. Split when a page passes ~15 H2 sections or ~800 lines, or when two sections serve different readers (a guide and a reference, a transport and its deployment).
- **Slugs are short nouns** (`/docs/basics/routing`), titles are the feature name ("Routing", "Database: Migrations" style prefixes are not needed because the category already says it).
- **Getting started is a path, not a list**: Introduction → Prerequisites → Installation → Pick a starter → Your first app → Directory structure → Configuration. Each page ends by pointing to the next.

## Page anatomy

Every guide page follows this order. Skip a section only when it would be empty.

1. **Front matter** — `title` (the feature name) and `description` (one sentence, used for SEO and the page subtitle).
2. **Opening** — two or three sentences: what the feature is and the problem it solves, in plain words. Then a "You will learn" list of 3–6 bullets.
3. **The idea** — the concept with an everyday analogy ("middleware is a series of layers, like the security checks at an airport"), a small diagram or table when it helps, and links to Go docs for language concepts.
4. **Quick start** — the smallest complete example that works, as `<Steps>`: create or edit the file (with its path), run the command, see the output.
5. **In depth** — one H2 per capability. Each one: a sentence of motivation, the code, then what happens under the hood (order of execution, defaults, errors).
6. **Configuration** — a table of every option and environment variable: name, type, default, what it does.
7. **Security** — what Vukem does by default, what stays the app's job, linked to [docs/security.md](security.md).
8. **Testing** — how to test the feature with `vukemtest`.
9. **Common mistakes** — "wrong" vs "right" side by side, with the error message the reader will actually see.
10. **Next steps** — two to four links (or a `CardGroup`) to the pages a reader needs next.

Reference pages (CLI, configuration lists) replace 3–5 with complete tables, one row per item.

## Writing style

- **Second person, present tense, active voice**: "You register a route", not "Routes are registered".
- **Short sentences.** One idea per sentence; split anything over ~25 words.
- **Motivation before API.** "Instead of writing the same login check in every handler, put it in a middleware" — then the code.
- **"may" for options, "should" for recommendations, "must" for requirements.**
- **Name things consistently**: the same word for the same thing on every page (handler, middleware, provider, guard, seeder, migration). A term's first use on a page can carry a `<Term>` tooltip.
- **Say the edge cases out loud**: "`r.Get("/")` matches only the root page, not every path" is better than letting the reader discover it.
- **No marketing words** ("powerful", "blazing", "simply", "just", "easy"). Show it is easy instead.
- **English only** on the English pages; Brazilian Portuguese pages translate meaning, not word for word.

## Code examples

- **Complete enough to run.** Include the `package` and `import` block whenever a snippet is the whole file; label every file snippet with its path (` ```go title="routes/web.go" `).
- **One running example per page** (a blog with posts and comments, a shop with products): build it up section by section instead of switching domains.
- **Show the command, then the generated file, then where it lives** for anything the CLI creates.
- **Show the output**: the HTTP response, the JSON body, the terminal output, the error message.
- **Comments explain why, not what**, and use full sentences.
- **No horizontal scroll.** Lines wrap (the site sets `white-space: pre-wrap`), but keep Go lines under ~80 characters anyway so wrapped code stays readable; break long calls one argument per line, as `gofmt` would.
- **Fixed placeholders**: `example.com` domains, `user@example.com`, `APP_KEY=base64:...`, `sk_test_...`. Never a real key, email or domain.
- **Verified**: every snippet compiles against the current API. Read the package source (and its tests) before writing; when unsure, write a scratch program and run it.

## Components

Every component below is registered in `site/components/mdx/index.tsx`; `npm run check:mdx` fails on any other capitalised tag.

| Component | Props | Use it for |
| --- | --- | --- |
| ` ```go title="routes/web.go" ` | `title` in the fence meta (or a bare first word: ` ```go routes/web.go `) | Every code block that is a file: the header shows the path instead of the language. Lines wrap, never scroll; the copy button copies the original text |
| `<Callout type="info">` | `type`: `info` (default), `tip`, `warning`, `danger`; optional `title` | A note that helps but is not required |
| `<Callout type="tip">` | as above | A shortcut or a better way |
| `<Callout type="warning" title="…">` | as above | Data loss, security risk or a breaking behaviour — nothing else |
| `<Steps>` / `<Step title="…">` | `title` on each step | Any ordered procedure (quick starts, tutorials, upgrades) |
| `<Tabs>` / `<Tab title="…">` | `title` on each tab | The same thing in alternatives (React/Vue/Svelte, PostgreSQL/MySQL/SQLite) |
| `<CodeGroup>` | none; wrap fenced code blocks, each tab is labelled by the block's `title` (or language) | The same snippet in alternative files or languages |
| `<CardGroup cols="2">` / `<Card title="…" href="/docs/…">` | `cols`: `1`, `2` (default), `3`; `title`, optional `href` and `icon` (`book`, `boxes`, `code`, `database`, `layers`, `lock`, `rocket`, `route`, `shield`, `terminal`, `zap`) | "Next steps" and category landing grids |
| `<Term tip="…">word</Term>` | `tip`: the definition, plain text | Defining a term on its first use on a page. Opens on hover, keyboard focus and tap; dotted underline |
| `> quote` | — | A short quotation; Markdown blockquotes render as a citation (left border, muted text). Put the source link on the next line |
| `<Quote source="https://…" author="…" title="…">` | `source` (URL, required), `author` and `title` (link text, default: the URL's host and path) optional | Quoting a specification, an RFC or the Go docs with a linked source line |
| Tables | — | Options, environment variables, mappings (HTTP verb → route → handler), comparisons. Wide tables scroll inside their frame, never the page |

Example:

````mdx
A <Term tip="A function that receives the request and writes the response.">handler</Term> answers each request.

<Callout type="warning" title="Breaking">A bare `/` matches every path in ServeMux.</Callout>

<Quote source="https://pkg.go.dev/net/http#hdr-Patterns-ServeMux" author="The Go standard library" title="net/http: Patterns">
A pattern that ends in a slash matches every path that begins with that prefix.
</Quote>
````

Keep callouts rare: at most three per page, one sentence or two each.

## Links

- Link Go concepts to the official docs: [Effective Go](https://go.dev/doc/effective_go), [A Tour of Go](https://go.dev/tour), the [standard library](https://pkg.go.dev/std), the [Go spec](https://go.dev/ref/spec).
- Link standards to their source: RFC 9457 for problem details, the OWASP Top 10 for security, the Inertia and HTMX docs for those protocols.
- Link the package reference on pkg.go.dev (`https://pkg.go.dev/github.com/AlexGalhardo/vukem/<pkg>`) at the end of the page.
- Internal links are root-relative (`/docs/basics/routing`) and checked by the build.

## Workflow: changing a feature

1. Change the code and its design doc under `docs/` in the same branch.
2. Update every site page that mentions the feature (search `site/content` for its names) — options, defaults, examples, upgrade notes.
3. Validate: `npm run check:mdx -- <files>` (seconds; compiles the MDX with the site's plugins, rejects unregistered components, `/docs/...` links without a page and bare `{placeholders}`, warns on code lines over 100 characters), then `npm run lint` and `npm run build` in `site/`.
4. Look at the changed pages in a browser in light and dark mode, desktop and mobile.
5. The `Site` workflow builds the site on every PR and push; Vercel deploys `main`.

## Workflow: reviewing a page

Ask these questions; fix anything that fails:

- Could a beginner follow it from top to bottom without opening another tab, except for the linked Go concepts?
- Is every option, default, environment variable and error of the feature on the page?
- Does every code block compile, carry its file path and stay readable without horizontal scroll?
- Does it say what happens under the hood and what can go wrong?
- Does it end with next steps?
