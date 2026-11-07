# CLAUDE.md

@../AGENTS.md

## Claude Code notes

- On any Go task, load the `golang-how-to` skill first; it routes to the right `golang-*` skills.
- Use `tdd` / `superpowers-test-driven-development` for implementation tasks from docs/PLAN.md.
- Path-scoped rules in `.claude/rules/` load automatically when matching files are touched.
- Repository upkeep (PR flow, release through a PR, weekly dependency and security routines, Scorecard metrics) lives in [docs/maintenance.md](../docs/maintenance.md); `.github/` changes follow `.claude/rules/workflows.md`.
- Any PR that adds, changes or removes a page under `site/content/docs/` must refresh `site/content/last-updated.json` (`npm run last-updated` in `site/`, from a clone with full history) in the same PR. The "Last updated" date is part of the page contract; never edit it by hand. CI enforces it (`Site / Last updated`).
- Use the `vukem-release` skill for releases; it follows the PR-based flow once the ruleset requires pull requests.
- During development, apply the installed `security-audit` skill (`.claude/skills/security-audit`, six phases: reconnaissance, coverage-led hunting, candidate validation, structured `findings.json`, independent record verification, target-neutral report). Audit security issues as part of the work. A full six-phase run is only for an explicit audit or pen-test request; other work uses the skill's guidance mode.
