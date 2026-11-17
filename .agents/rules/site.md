---
paths:
  - "site/**"
  - "CHANGELOG.md"
---

# Docs site rules

- `site/` is Astro 7.4 + MDX, built to static files in `site/out` and deployed to GitHub Pages from `main`; `site.yml` builds it on every push and PR that touches `site/**` or `CHANGELOG.md`, and deploys on pushes to `main`.
- In MDX prose, braces are JavaScript: write placeholders such as `{field}` inside backticks or code fences, never bare (a bare `{field}` fails prerendering with `ReferenceError: field is not defined`).
- `/changelog` renders the root `CHANGELOG.md` at build time as plain Markdown (`format: "md"`), so braces there stay literal; do not copy the file into `site/`.
- Validate with `npm run test:site`, `npm run lint`, `npm run check:mdx` and `npm run build` (the site uses `package-lock.json`); delete a stale `site/.astro` or `site/out` left by a dev server before a production build when the build complains about it.
- The site is English only: UI strings go in `lib/ui-text.ts`, pages under `src/pages`. Do not add translations.
- Visual changes: check the page in a browser (light/dark, desktop/mobile) before committing.
- The app docs sites (email, trading and studio at `*.vukem.dev`) mirror this shell with the same pinned `astro` and `@astrojs/react` versions; bump the pins together and tell the app maintainers.
