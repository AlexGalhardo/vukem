---
paths:
  - "site/**"
  - "CHANGELOG.md"
---

# Docs site rules

- `site/` is Next.js + MDX, deployed by Vercel from `main` (Root Directory `site`); `site.yml` builds it on every push and PR that touches `site/**` or `CHANGELOG.md`.
- In MDX prose, braces are JavaScript: write placeholders such as `{field}` inside backticks or code fences, never bare (a bare `{field}` fails prerendering with `ReferenceError: field is not defined`).
- `/changelog` renders the root `CHANGELOG.md` at build time as plain Markdown (`format: "md"`), so braces there stay literal; do not copy the file into `site/`.
- Validate with `npm run lint` and `npm run build` (the site uses `package-lock.json`); delete a stale `site/.next` left by `next dev` before a production build.
- UI strings go in both dictionaries of `lib/i18n.ts` (`en`, `pt-br`); pages exist under `app/(en)` and `app/(pt-br)/pt-br`.
- Visual changes: check the page in a browser (light/dark, desktop/mobile) before committing.
