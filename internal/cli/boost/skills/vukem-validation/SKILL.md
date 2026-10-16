---
name: vukem-validation
description: Typed input validation with Vukem's Zod-style z schemas. Use when accepting form or JSON input, writing request structs, or exporting schemas to TypeScript.
---

# Vukem validation (`z`)

- Put request structs and their schemas in `app/requests` (`vukem make:request StorePost`).
- Build schemas with `z.Struct[T](z.Shape{...})`: `z.String().Trim().Min(3).Max(255)`, `z.Int().Min(0)`, `z.Enum(...)`, `z.Slice(...)`, `z.Time().Optional()`, `.Default(...)`, cross-field rules with `.Refine(func(T) error { return z.FieldError("field", "msg") })`.
- In handlers: `in, err := web.Parse(c, requests.StorePostSchema)`; return `err` unchanged so the framework answers 422 (JSON), a fragment (htmx) or redirects back with flashed errors and old input (forms).
- Views read `{{error "title"}}` and `{{old "title"}}` after a failed form post.
- Typed API handlers (`api.Register`) validate input and output from the same types; `vukem codegen:types` exports TypeScript (and Zod with `-zod`); `vukem codegen:client` generates a typed TypeScript or Go client.
- Never trust unvalidated `c.FormValue` for business logic.
