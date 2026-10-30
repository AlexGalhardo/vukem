---
paths:
  - "site/content/**"
---

# Docs content rules

Full rationale and examples: [docs/world-class-api-documentation-site-workflow.md](../../docs/world-class-api-documentation-site-workflow.md).

- Write for someone learning Go and Vukem at the same time; explain why before how, and document every option, default, environment variable, error and edge case the code has.
- Page order: opening (what + problem, "You will learn" bullets) → the idea (analogy, Go links) → quick start (`<Steps>`) → in depth (one H2 per capability, with what happens under the hood) → configuration table → security → testing → common mistakes (wrong vs right + real error text) → next steps.
- Second person, present tense, short sentences; "may" / "should" / "must"; no "simply", "just", "easy", "powerful".
- Code: verified against the current source, file path on every file snippet (` ```go title="routes/web.go" `), imports when it is the whole file, one running example per page, output shown, lines kept under ~80 columns (code wraps, never scrolls).
- Link Go language concepts to go.dev (tour, Effective Go, pkg.go.dev std, spec) and standards to their source; internal links are root-relative.
- Callouts: info, tip, warning (warning only for data loss, security or breaking behaviour), at most three per page; use `<Term tip="…">` for first-use definitions, tables for options and mappings.
- Any PR that adds, changes or removes a page under `site/content/docs/` must refresh `site/content/last-updated.json` (`npm run last-updated` in `site/`, from a clone with full history) in the same PR. The "Last updated" date is part of the page contract; never edit it by hand. CI enforces it (`Site / Last updated`).
- Validate with `npm run check:mdx -- <files>`, then `npm run lint` and `npm run build` in `site/`; MDX braces in prose go inside backticks.
