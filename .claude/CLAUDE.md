# CLAUDE.md

@../AGENTS.md

## Claude Code notes

- On any Go task, load the `golang-how-to` skill first; it routes to the right `golang-*` skills.
- Use `tdd` / `superpowers-test-driven-development` for implementation tasks from docs/PLAN.md.
- Path-scoped rules in `.claude/rules/` load automatically when matching files are touched.
- Repository upkeep (PR flow, release through a PR, weekly dependency and security routines, Scorecard metrics) lives in [docs/maintenance.md](../docs/maintenance.md); `.github/` changes follow `.claude/rules/workflows.md`.
- Use the `vukem-release` skill for releases; it follows the PR-based flow once the ruleset requires pull requests.
