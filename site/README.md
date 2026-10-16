# Lumek documentation site

The public docs at <https://lumek.vercel.app>: Next.js 16 (App Router, static generation), Tailwind CSS v4, Base UI and MDX. See [docs/site.md](../docs/site.md) for the decisions behind it.

## Run

```sh
cd site
npm install
npm run dev     # http://localhost:3000
npm run build   # static build of every page; must pass before a release
npm run lint
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
| `app/` | Routes: landing, `/docs/[...slug]`, `/search-index.json`, `/llms.txt`, `/llms-full.txt` |
| `components/` | Header, sidebar, search, TOC, theme toggle, MDX components |
| `lib/` | Content loading, MDX rendering, Shiki highlighting, search index, TOC |
| `content/` | MDX pages and `navigation.ts` |
| `public/` | Logo |
