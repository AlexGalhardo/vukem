# Documentation site

The public "how to use Vukem" website lives in [`site/`](../site) and is deployed to **<https://vukem.dev>**. Its layout and DX are inspired by the [Resend docs](https://resend.com/docs/introduction); its information architecture follows the Laravel and AdonisJS v7 docs.

## Decision

| Option | Outcome |
| --- | --- |
| Mintlify Starter (free) | Rejected: Mintlify cannot be built and served by Vercel on the free plan (static export is Enterprise-only; the `mint` CLI is Elastic-2.0). Vercel could only reverse-proxy Mintlify's hosting, and preview deployments, assistant and analytics are Pro. |
| **Own static site** | **Chosen (2026-10-16):** full control of the Resend-like layout, MDX content in the repo, MIT dependencies. Production hosting moved to GitHub Pages on 2026-11-05 (static export, free on the public repository). The renderer moved from Next.js to Astro 7.4 on 2026-11-06; the URLs and the pages stayed. |

## Stack

| Concern | Choice |
| --- | --- |
| Framework | Astro 7.4 (static output) with React for the header, search and theme |
| Styling | Tailwind CSS v4 (CSS-first config, `@theme` tokens, `dark` variant via class) |
| UI primitives | Base UI (`@base-ui/react`): Dialog (search), Toast (copy feedback), Tooltip, Popover, Collapsible |
| Content | MDX files in `site/content/docs/**` with front matter (`title`, `description`); custom components (`<Callout>`, `<Steps>`, `<Tabs>`, `<CodeGroup>`, `<Card>`) |
| Code blocks | Shiki highlighting (light and dark themes), copy button with clipboard.js + toast "Copied to clipboard" |
| Search | `Ctrl+K` / `⌘K` command palette over a build-time index of titles, headings and text |
| Theme | Light/dark/system toggle, persisted, no flash on load |
| Navigation | Left sidebar grouped by category, right "On this page" table of contents, previous/next links, "Edit on GitHub" link |
| Changelog | `/changelog` renders the repository `CHANGELOG.md` at build time as plain Markdown (braces stay literal), linked from the header; every item links to its commit (`.config/cliff.toml` template) |
| Agents | `llms.txt` and `llms-full.txt` generated from the MDX content; every current page also has a Markdown twin at `/docs/<slug>.md` (`src/pages/docs/[...slug].md.ts`) |
| Hosting | GitHub Pages from the Astro static build (`site/out`). Production is `vukem.dev`. The Site workflow builds every pull request and publishes every push to `main` |

## Layout (Resend-inspired)

- Top bar: logo, search trigger (`Search… ⌘K`), GitHub link, language switcher (EN / PT), theme toggle.
- Left sidebar: categories with page links, active page highlighted; collapses into a drawer on mobile.
- Content: large title and description, "Last updated" date and time (last git commit of the page's file), MDX body, generous spacing, monospace code blocks with filename/language tabs and copy button.
- Right rail: "On this page" headings with scroll-spy.
- Footer of each page: previous/next and "Edit this page".

## Categories

1. **Getting started** — Introduction, Installation, Quick start, Key concepts, Starters, Your first app, Configuration, Directory structure, Showcase (the three reference apps)
   **Coming from** — Laravel, AdonisJS (concept maps with a side-by-side route; only these two frameworks, by decision of the maintainer)
2. **Architecture** — Request lifecycle, Application and providers, Context and shutdown, Best practices
3. **The basics** — Routing, Middleware, Controllers, HTTP context, Requests and input, Responses, Validation (`z`), Error handling, Logging, Static files, Sessions, CSRF, Views
4. **APIs** (folder `rest-apis`) — Overview, Pagination, Typed handlers, OpenAPI and the Scalar reference, Client generation, Idempotency, GraphQL, gRPC
5. **Database** — Getting started, Query builder, Models, Relations, Migrations, Factories and seeders, Transactions, PostgreSQL, Redis, MongoDB
6. **Security** — Overview (OWASP Top 10:2025), Authentication, Social login, JWT, API tokens, Two-factor, Authorization, Rate limiting, CSP and security headers, Signed URLs, Encryption, Building blocks
7. **Frontend** — HTMX, Vite, Inertia (React, Vue, Svelte), Inertia and i18n
8. **Digging deeper** — Mail, Cache, Storage (with S3), File uploads, Events, Queues, Scheduler, SSE, WebSockets and broadcasting, Webhooks, Localization, Billing (Stripe), Admin panel, Dev dashboard, OpenTelemetry, Telegram logging
9. **Testing** — Getting started, HTTP tests, API tests, Database testing, Fakes
10. **Deployment** — Single binary (reverse proxy and TLS), Docker and Railway

Pages describe only what is implemented; planned features are marked with a "Planned" callout. Guides follow the same shape: a short explanation of the concept, a step-by-step section with runnable code, then "Pitfalls" and "Security notes".

## Versions

The site is versioned per minor (PLAN S.2.1). The current docs live in `site/content/docs/**` with `site/content/navigation.ts` and are served at `/docs/*`; each older minor is a frozen snapshot in `site/content/versions/<minor>/` (`docs/**` + `navigation.ts`) served at `/docs/<minor>/*`.

| Version | Content | URL |
| --- | --- | --- |
| `1.0` (latest) | `content/docs`, `content/navigation.ts` | `/docs/...` |
| `0.20` | `content/versions/0.20/` | `/docs/0.20/...` (`/docs/0.20` redirects to its introduction) |

- `site/lib/versions.ts` lists the versions (newest first; the first is current) and holds the URL helpers; `site/lib/content.ts` maps each version id to its navigation.
- There is no version picker in the UI (removed at the owner's request): snapshots stay reachable by URL and from links. The sidebar and mobile drawer show the navigation of the version in the URL.
- Snapshot pages keep their MDX unchanged: `/docs/...` links and `<Card href>` are rewritten to the snapshot at render time, the title gets the version (`Routing (v0.20)`), a banner links to the same page in the latest docs, and "Edit this page" points at the snapshot file.
- Search, `llms.txt` and `llms-full.txt` cover the latest version only.
- **Cutting a minor:** copy `content/docs` and `content/navigation.ts` to `content/versions/<old minor>/` (fix the navigation's type import as in `0.20`), add the entry to `versions` in `lib/versions.ts` and to `NAVIGATION` in `lib/content.ts`, bump the current entry's id and label, then `task site:build`. Snapshots are never edited except to fix a broken page.

## Language

The site is English only (since v0.28). `/pt-br/...` URLs from the earlier Portuguese pages have no file in the export; the 404 page sends them to the same English path. The root layout lives in the `app/(en)` route group and renders `components/site-shell.tsx` with `<html lang="en">`; unmatched URLs use `app/global-not-found.tsx` (`experimental.globalNotFound`). Interface strings live in `site/lib/ui-text.ts`. There is no sitemap.

## Workflow

```sh
cd site
npm install
npm run dev        # http://localhost:3000
npm run build      # must pass before every release
npm run lint
npm run check:mdx  # every .mdx under content/; or: npm run check:mdx -- content/docs/basics/routing.mdx
```

Repo tasks: `task site:dev`, `task site:build` (part of `task ci:full`).

- Editing content: change or add `site/content/docs/<category>/<page>.mdx`, then register the page in `site/content/navigation.ts`. Edit only the current docs; snapshots under `site/content/versions/` are frozen.
- **Update cadence:** the site is updated at the end of each phase (Prototype, ALPHA, BETA) — a release cannot be tagged until `task site:build` passes and the pages for the phase's features exist.
- Deploy: a push to `main` that changes `site/**`, `CHANGELOG.md` or `.github/workflows/site.yml` publishes `site/out` to GitHub Pages (`.github/workflows/site.yml`). `site/public/CNAME` sets the custom domain. DNS for the apex is four `A` records (`185.199.108.153`, `185.199.109.153`, `185.199.110.153`, `185.199.111.153`) and four `AAAA` records (`2606:50c0:8000::153`, `2606:50c0:8001::153`, `2606:50c0:8002::153`, `2606:50c0:8003::153`); `www` is a `CNAME` to `alexgalhardo.github.io`. Pull requests build and do not deploy. Live at <https://vukem.dev>.
