# Vukem documentation site

The public docs at <https://vukem.dev>: Astro 7.4 (static HTML), Tailwind CSS v4, Base UI and MDX. See [docs/site.md](../docs/site.md) for the decisions behind it. A parent repository lockfile does not change the site root: Astro builds `site/` on its own, so there is no Turbopack workspace-root setting to copy.

Next.js used to treat a parent `bun.lock` as the workspace root and then fail on `@vercel/turbopack-next` while loading a Google font. This site does not use Next.js. Do not copy `turbopack.root` into the Astro config.

## Run

```sh
cd site
npm install
npm run dev     # http://localhost:3000
npm run build   # static build of every page; must pass before a release
npm run lint
npm run check:mdx   # compile MDX, check components, /docs links and code line length (add -- <files> to narrow)
npm run last-updated   # refresh content/last-updated.json (needs full git history)
```

## Add a page

1. Create `content/docs/<category>/<slug>.mdx` with front matter:

   ```mdx
   ---
   title: Sessions
   description: One sentence shown under the title and in search results.
   ---
   ```

2. Add `<slug>` to the category's `pages` in `content/navigation.ts` (order = sidebar order). A new category also needs an entry there.
3. The page is served at `/docs/<category>/<slug>` (Getting started pages at `/docs/<slug>`), and is picked up by search, `llms.txt` and `llms-full.txt` automatically.

A page here is MDX. A `docs/*.md` file written for GitHub does not compile: an autolink written as `<https://...>`, a bare `{`, a raw `<` in prose, and a relative `.md` link fail or break. Write the page as `.mdx`, or render that Markdown with `format: "md"` so braces stay literal. `renderMarkdown` in `lib/changelog.tsx` is the second path. This site does not ship a script that converts `docs/` into `content/docs`.

Mark features that are not implemented yet with `<Callout type="warning">Planned for ALPHA</Callout>` (or BETA). Go samples use tabs and must match the real APIs.

## Last updated

Every page shows "Last updated" with the date and time of the last commit that touched its MDX file, formatted with `Intl.DateTimeFormat` in English, in UTC.

- At build time `lib/last-updated.ts` runs `git log` over `content/` once (`lib/git-dates.ts`).
- The production checkout is shallow (and may not ship `.git` at all). Commits at the shallow boundary are ignored, because git reports them as touching every file, and any page git cannot date falls back to `content/last-updated.json`. `lib/last-updated.ts` reads that file from disk. A copied site that does not have it yet still builds, and the line stays hidden until git or the file has a date. This repository's CI check still requires the file.
- Refresh the fallback map with `npm run last-updated` (from a clone with full history: `git fetch --unshallow` first if needed) and commit it.

On `main` the check also accepts a stale date when the newest commit of the page is a squash merge (`(#123)` subject) and the recorded date is not older than the change before it, so a squash-merged PR needs no refresh after merging `origin/main`.

Rule: Any PR that adds, changes or removes a page under `site/content/docs/` (including a frozen copy under `site/content/versions/`) must refresh `site/content/last-updated.json` (`npm run last-updated` in `site/`, from a clone with full history) in the same PR. The "Last updated" date is part of the page contract; never edit it by hand. CI checks every docs page, not only the files the PR touched (`Site / Last updated` on site changes, and `Docs last updated / Last updated` on every pull request and every push to `main`). Locally, `npm run check:last-updated` runs the same check (`npm run test:scripts` tests it). Release checklist: refresh `last-updated.json` before tagging ([maintenance](../docs/maintenance.md#release-routine)).

## A copied shell

`content/versions/0.20` is this framework's frozen snapshot. It is registered in `lib/versions.ts`, `lib/content.ts` and `scripts/check-mdx.ts`. A product that copies the shell deletes that directory and those `0.20` entries before the first build. This repository keeps them: `/docs/0.20/` is a published snapshot.

`lib/changelog.tsx` reads `../CHANGELOG.md`, one directory above `site/`. A standalone copy keeps the changelog at that path. The build reads that file; a missing file fails the build.

## Language

The site is English only. Old `/pt-br/...` links hit the 404 page, which sends them to the English page. UI strings live in `lib/ui-text.ts`. Details in [docs/site.md](../docs/site.md#language).

## MDX components

| Component | Usage |
| --- | --- |
| `<Callout type="info\|tip\|warning\|danger">` | Highlighted note |
| `<Steps>` / `<Step title="…">` | Numbered steps |
| `<Tabs>` / `<Tab title="…">` | Tabbed content |
| `<CodeGroup>` | Wrap fenced code blocks to show them as tabs |
| `<CardGroup cols="2">` / `<Card title href icon>` | Link cards (`icon`: book, boxes, code, database, layers, lock, rocket, route, shield, terminal, zap) |

Fenced code blocks accept a file name: ` ```go title="main.go" `.

## Layout

| Path | Contents |
| --- | --- |
| `src/pages/` | Routes: `docs/[...slug]`, `/changelog`, the 404, `/search-index.json`, `/llms.txt`, `/llms-full.txt`, `/robots.txt`, `/sitemap.xml` |
| `components/` | Site shell, header, language switcher, sidebar, doc page, search, TOC, theme toggle, MDX components |
| `lib/` | Content loading, UI strings, last-updated dates, MDX rendering, Shiki highlighting, search index, TOC |
| `content/` | MDX pages, `navigation.ts`, `last-updated.json` |
| `scripts/` | `last-updated.ts` (run by Node directly, no build step) |
| `public/` | Logo |

## Landing page

The root page (`src/pages/index.astro`) is a standalone composition. From 768px wide it fits one viewport with no vertical scroll; below that, or at 360px tall or less, it scrolls normally and the feature tabs become a swipeable chip row. The code panels reuse the docs code block markup and `CopyButton`. The hero carries the copyable install commands (`go install`, `vukem new`, and the Finance scaffold with `--seed`).

The feature tabs (Routing, DB / ORM, Auth, Jobs, Admin, CLI) switch in place with the View Transitions API, with no URL change. Each code sample is trimmed from the docs page listed in the `tabs` array of the page; keep them in sync with the docs.

The mascot is one static SVG, `src/assets/gopher.svg`, inlined by the header logo. Credit: Go Gopher by Renee French, CC BY 4.0.
