# Vukem example: blog

A small blog built with Vukem, used as a reference app and run by CI on SQLite and Postgres.

It starts from `vukem new blog` and `vukem install:auth`, then adds:

- `Post` and `Comment` models with `BelongsTo`/`HasMany` relations, eager loaded with `With`.
- `PostPolicy` (`app/policies`): anyone signed in can write, only the author can edit or delete; templates hide links with `{{can}}`.
- Routes mixing `auth.Optional` (public pages that know the user) and `auth.Required`.
- Factories and a seeder (`vukem db:seed`).
- The Postgres driver: set `DATABASE_URL=postgres://...` to use it.

```sh
cd examples/blog
vukem migrate
vukem db:seed
vukem serve

GOWORK=off go test ./...                                              # SQLite
TEST_DATABASE_URL=postgres://vukem:vukem@localhost:55432/vukem_test GOWORK=off go test ./tests/   # Postgres (task services:up)
```
