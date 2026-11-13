# CLAUDE.md

Project conventions live in [AGENTS.md](AGENTS.md). Code, comments, and docs stay in English.

`vukem new <name> --example finance` always writes `Dockerfile` and `compose.yaml`. `--seed` migrates and runs `db:seed` in development and test only. Omitting `--seed` seeds nothing. Seed later with `vukem db:seed FinanceAdminSeeder FinanceDemoSeeder`. Run the app with `vukem serve`.

The Ubuntu CI job uploads `coverage.out` to Codecov at <https://app.codecov.io/gh/AlexGalhardo/vukem> with OIDC. If Codecov has not registered the repository, that upload does not fail the job. The root README badge points there.
