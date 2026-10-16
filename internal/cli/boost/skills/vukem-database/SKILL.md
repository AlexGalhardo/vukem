---
name: vukem-database
description: Models, queries, relations, migrations, factories and seeders in a Vukem app. Use when touching the database, adding tables or columns, or writing queries.
---

# Vukem database

- Inspect the real schema first with the `database_schema` MCP tool; try read-only queries with `database_query`.
- Models embed `orm.Model` (ID, timestamps) in `app/models`; `vukem make:model Post -m -f` also creates the migration and factory.
- Queries: `orm.Query[models.Post](db).Where("published", true).OrderBy("created_at", orm.Desc).Limit(20).Get(ctx)`, `.Find(ctx, id)`, `.Count`, `.Exists`, `.Each` for streaming. Writes: `orm.Create`, `orm.Update`, `orm.Delete`. Transactions: `db.Tx(ctx, func(tx *database.Tx) error { ... })`.
- Relations: `orm.BelongsTo[T]`, `orm.HasMany[T]`, `orm.HasOne[T]`, `orm.ManyToMany[T]`; eager-load to avoid N+1.
- Schema changes only through migrations (`vukem make:migration create_posts_table`, then `vukem migrate`). Never edit a migration that already ran; add a new one.
- Raw SQL only with bound parameters; never concatenate input.
- Seed test data with factories (`vukem make:factory`) and seeders (`vukem db:seed`).
