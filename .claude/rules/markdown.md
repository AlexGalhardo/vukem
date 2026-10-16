---
paths:
  - "**/*.md"
---

# Markdown rules

- Must pass `npx markdownlint-cli2` with `.markdownlint-cli2.jsonc`.
- ATX headings (`#`), dash lists, fenced code blocks with a language, blank lines around blocks.
- Indent with 2 spaces; hard tabs are allowed only inside `go` / `makefile` fences.
- English, concise, link to the owning doc instead of duplicating content.
- `AGENTS.md` stays ≤ 50 lines and links to `docs/`.
