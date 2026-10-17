# Documentation site

The public "how to use Vukem" website lives in [`site/`](../site) and is deployed to **<https://vukem.vercel.app>**. Its layout and DX are inspired by the [Resend docs](https://resend.com/docs/introduction); its information architecture follows the Laravel and AdonisJS v7 docs.

## Decision

| Option | Outcome |
| --- | --- |
| Mintlify Starter (free) | Rejected: Mintlify cannot be built and served by Vercel on the free plan (static export is Enterprise-only; the `mint` CLI is Elastic-2.0). Vercel could only reverse-proxy Mintlify's hosting, and preview deployments, assistant and analytics are Pro. |
| **Own Next.js site** | **Chosen (2026-10-16):** full control of the Resend-like layout, native Vercel builds and preview deploys, MDX content in the repo, MIT dependencies. |

## Stack

| Concern | Choice |
| --- | --- |
| Framework | Next.js 16 (App Router, React Server Components, static generation of every page) |
| Styling | Tailwind CSS v4 (CSS-first config, `@theme` tokens, `dark` variant via class) |
| UI primitives | Base UI (`@base-ui/react`): Dialog (search), Toast (copy feedback), Tooltip, Popover, Collapsible |
| Content | MDX files in `site/content/docs/**` with front matter (`title`, `description`); custom components (`<Callout>`, `<Steps>`, `<Tabs>`, `<CodeGroup>`, `<Card>`) |
| Code blocks | Shiki highlighting (light and dark themes), copy button with clipboard.js + toast "Copied to clipboard" |
| Search | `Ctrl+K` / `⌘K` command palette over a build-time index of titles, headings and text |
| Theme | Light/dark/system toggle, persisted, no flash on load |
| Navigation | Left sidebar grouped by category, right "On this page" table of contents, previous/next links, "Edit on GitHub" link |
| Agents | `llms.txt` and `llms-full.txt` generated from the MDX content |
| Hosting | Vercel project `lumek` (created before the rename; Root Directory `site`), production at `vukem.vercel.app` (the old `lumek.vercel.app` still serves the same site), preview per branch |

## Layout (Resend-inspired)

- Top bar: logo, version switcher, search trigger (`Search… ⌘K`), GitHub link, theme toggle.
- Left sidebar: categories with page links, active page highlighted; collapses into a drawer on mobile.
- Content: large title and description, MDX body, generous spacing, monospace code blocks with filename/language tabs and copy button.
- Right rail: "On this page" headings with scroll-spy.
- Footer of each page: previous/next and "Edit this page".

## Categories

1. **Getting started** — Introduction, Installation, Starters, Your first app, Configuration, Directory structure
2. **Architecture** — Request lifecycle, Application and providers, Context and shutdown
3. **The basics** — Routing, Middleware, Controllers, HTTP context, Requests and input, Responses, Validation (`z`), Error handling, Logging, Static files, Sessions, CSRF, Views
4. **APIs** (folder `rest-apis`) — Overview, Pagination, Typed handlers, OpenAPI and the Scalar reference, Client generation, Idempotency, GraphQL, gRPC
5. **Database** — Getting started, Query builder, Models, Relations, Migrations, Factories and seeders, Transactions, PostgreSQL, Redis, MongoDB
6. **Security** — Overview (OWASP Top 10:2025), Authentication, Social login, JWT, Two-factor, Authorization, Encryption, Building blocks
7. **Frontend** — HTMX, Vite, Inertia (React, Vue, Svelte)
8. **Digging deeper** — Mail, Cache, Storage (with S3), File uploads, Events, Queues, Scheduler, SSE, WebSockets and broadcasting, Localization, Billing (Stripe), Admin panel, Dev dashboard, Telegram logging
9. **Testing** — Getting started, HTTP tests, API tests, Database testing, Fakes
10. **Deployment** — Single binary, Docker, Reverse proxy and TLS

Pages describe only what is implemented; planned features are marked with a "Planned" callout.

## Versions

The site is versioned per minor (PLAN S.2.1). The current docs live in `site/content/docs/**` with `site/content/navigation.ts` and are served at `/docs/*`; each older minor is a frozen snapshot in `site/content/versions/<minor>/` (`docs/**` + `navigation.ts`) served at `/docs/<minor>/*`.

| Version | Content | URL |
| --- | --- | --- |
| `1.0` (latest) | `content/docs`, `content/navigation.ts` | `/docs/...` |
| `0.20` | `content/versions/0.20/` | `/docs/0.20/...` (`/docs/0.20` redirects to its introduction) |

- `site/lib/versions.ts` lists the versions (newest first; the first is current) and holds the URL helpers; `site/lib/content.ts` maps each version id to its navigation.
- The header's version switcher (`components/version-switcher.tsx`, a native `<select>`) keeps the current page when the target version has it, otherwise it opens that version's introduction. The sidebar and mobile drawer show the navigation of the version in the URL.
- Snapshot pages keep their MDX unchanged: `/docs/...` links and `<Card href>` are rewritten to the snapshot at render time, the title gets the version (`Routing (v0.20)`), a banner links to the same page in the latest docs, and "Edit this page" points at the snapshot file.
- Search, `llms.txt` and `llms-full.txt` cover the latest version only.
- **Cutting a minor:** copy `content/docs` and `content/navigation.ts` to `content/versions/<old minor>/` (fix the navigation's type import as in `0.20`), add the entry to `versions` in `lib/versions.ts` and to `NAVIGATION` in `lib/content.ts`, bump the current entry's id and label, then `task site:build`. Snapshots are never edited except to fix a broken page.

## Workflow

```sh
cd site
npm install
npm run dev        # http://localhost:3000
npm run build      # must pass before every release
npm run lint
```

Repo tasks: `task site:dev`, `task site:build` (part of `task ci:full`).

- Editing content: change or add `site/content/docs/<category>/<page>.mdx`, then register the page in `site/content/navigation.ts`. Edit only the current docs; snapshots under `site/content/versions/` are frozen.
- **Update cadence:** the site is updated at the end of each phase (Prototype, ALPHA, BETA) — a release cannot be tagged until `task site:build` passes and the pages for the phase's features exist.
- Deploy: `cd site && vercel deploy --prod` (project `lumek`, team `galhardos-projects`, linked via `site/.vercel/`, framework pinned in `site/vercel.json`). Live at <https://vukem.vercel.app>. Once the Vercel GitHub integration is connected (Root Directory `site`), every push to `main` deploys.
