# Vukem documentation site

The public docs at <https://vukem.dev>: Next.js 16 (App Router, static generation), Tailwind CSS v4, Base UI and MDX. See [docs/site.md](../docs/site.md) for the decisions behind it.

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

Mark features that are not implemented yet with `<Callout type="warning">Planned for ALPHA</Callout>` (or BETA). Go samples use tabs and must match the real APIs.

## Last updated

Every page shows "Last updated" with the date and time of the last commit that touched its MDX file, formatted with `Intl.DateTimeFormat` in English, in UTC.

- At build time `lib/last-updated.ts` runs `git log` over `content/` once (`lib/git-dates.ts`).
- Vercel clones shallowly (and may not ship `.git` at all). Commits at the shallow boundary are ignored, because git reports them as touching every file, and any page git cannot date falls back to the committed `content/last-updated.json`. If neither has the file, the line is hidden.
- Refresh the fallback map with `npm run last-updated` (from a clone with full history) and commit it, e.g. before a release. Pages changed after the last refresh still get the right date from git whenever the deploy's clone contains that commit.

## Language

The site is English only. Old `/pt-br/...` links redirect to the English page. UI strings live in `lib/ui-text.ts`. Details in [docs/site.md](../docs/site.md#language).

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
| `app/` | Routes: `(en)/docs/[...slug]` (the root layout), global 404, `/search-index.json`, `/llms.txt`, `/llms-full.txt` |
| `components/` | Site shell, header, language switcher, sidebar, doc page, search, TOC, theme toggle, MDX components |
| `lib/` | Content loading, UI strings, last-updated dates, MDX rendering, Shiki highlighting, search index, TOC |
| `content/` | MDX pages, `navigation.ts`, `last-updated.json` |
| `scripts/` | `last-updated.ts` (run by Node directly, no build step) |
| `public/` | Logo |
