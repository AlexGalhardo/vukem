# Validation and typed data (`z`)

`z` is Lumek's Zod-inspired schema library: build a schema once, then **parse untrusted input into a typed Go value** or **check outgoing data** against the same contract. Decision `H1` in [brainstorming.md](brainstorming.md).

## Goals

- Every byte that enters the app (JSON, forms, query strings, params, headers, env, queue payloads, external API responses) is parsed through a schema into a concrete Go type.
- Data that leaves the app (JSON responses, Inertia props, queued payloads) can be checked against a schema (always in development/test, opt-in in production).
- Schemas are typed: a schema for `int` cannot be attached to a `string` field; mismatches are detected when the schema is built (at boot), not per request.
- One engine: struct tags (`validate:"required,email"`) are shorthand compiled into the same schemas.
- Schemas export to JSON Schema / OpenAPI (BETA) and TypeScript types (BETA) for frontends.

## A taste

```go
package requests

type StorePost struct {
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Tags        []string   `json:"tags"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at"`
	Author      Author     `json:"author"`
}

type Author struct {
	Email string `json:"email"`
}

var StorePostSchema = z.Struct[StorePost](z.Shape{
	"title":        z.String().Trim().Min(3).Max(255),
	"body":         z.String().Min(1),
	"tags":         z.Slice(z.String().Trim().ToLower().Alphanum()).Max(5).Default([]string{}),
	"status":       z.Enum("draft", "published").Default("draft"),
	"published_at": z.Time().Optional(),
	"author": z.Struct[Author](z.Shape{
		"email": z.String().Email(),
	}),
}).Refine(func(p StorePost) error {
	if p.Status == "published" && p.PublishedAt == nil {
		return z.FieldError("published_at", "required when status is published")
	}
	return nil
})
```

```go
// In a handler: decode + parse + validate in one step; failures become 422.
in, err := web.Parse(c, requests.StorePostSchema) // in is requests.StorePost
if err != nil {
	return err
}
```

```go
// Anywhere: parse arbitrary data.
post, err := requests.StorePostSchema.Parse(map[string]any{"title": "Hi"})
var issues z.Issues
if errors.As(err, &issues) {
	for _, is := range issues {
		fmt.Println(is.Path, is.Code, is.Message) // "body" "required" "Required"
	}
}
```

## Schemas

| Constructor | Go type | Checks / transforms |
| --- | --- | --- |
| `z.String()` | `string` | `Min`, `Max`, `Len`, `Email`, `URL`, `UUID`, `ULID`, `Regex`, `Alpha`, `Alphanum`, `Numeric`, `StartsWith`, `EndsWith`, `Includes`, `OneOf`; `Trim`, `ToLower`, `ToUpper` |
| `z.Int()`, `z.Int64()`, `z.Uint()`… | integer types | `Min`, `Max`, `Positive`, `NonNegative`, `MultipleOf` |
| `z.Float()` | `float64` | `Min`, `Max`, `Positive`, `Finite` |
| `z.Bool()` | `bool` | — |
| `z.Time()` | `time.Time` | `After`, `Before`, `Layout(...)` (default RFC 3339) |
| `z.Enum(vals...)` | `string` (or any `~string` via `z.EnumOf[T]`) | membership |
| `z.Slice(elem)` | `[]E` (E = element schema type) | `Min`, `Max`, `Len`, `NonEmpty`, `Unique` |
| `z.Map(key, val)` | `map[K]V` | `Min`, `Max` |
| `z.Struct[T](z.Shape{...})` | `T` | field schemas mapped by `json`/`form`/`query` tag or field name; `Strict()` rejects unknown keys |
| `z.Custom[T](fn)` | `T` | user parser |

Modifiers on every schema:

| Modifier | Meaning |
| --- | --- |
| `Optional()` | key may be absent; target must be a pointer, or the zero value is kept |
| `Nullable()` | `null` accepted (pointer target) |
| `Default(v)` | value used when absent |
| `Refine(fn)` | custom check on the parsed value |
| `Transform(fn)` | map the parsed value (same type) |
| `Message(code, msg)` | override a message |
| `Describe(text)` | description for OpenAPI/TypeScript |

## Coercion

Inputs arrive as JSON values (`string`, `float64`, `bool`, `[]any`, `map[string]any`) or as strings (forms, query, params, env). Schemas coerce only where it is lossless:

| Schema | Accepts |
| --- | --- |
| `Int` | JSON number without fraction; numeric string (forms/query) |
| `Float` | JSON number; numeric string |
| `Bool` | JSON bool; `"true"/"false"/"1"/"0"/"on"/"off"` (checkbox) |
| `Time` | RFC 3339 string (or configured layout) |
| `Slice` | JSON array; repeated form keys (`tags=a&tags=b`) or `tags[]=` |
| `Struct` | JSON object; bracketed form keys (`author[email]=…`) |

`Check` (outgoing data) never coerces strings. Empty strings from forms count as absent for numbers, booleans, times and enums, so `Optional()`/`Default()` apply to empty inputs.

## Errors

`Parse` returns `z.Issues` (a `[]z.Issue` implementing `error`), each with:

- `Path` — dotted path with indexes: `tags.2`, `author.email`
- `Code` — stable machine code: `required`, `invalid_type`, `too_small`, `too_big`, `invalid_string`, `invalid_enum`, `custom`
- `Message` — human message (i18n in BETA)
- `Params` — values used by the message (`{"min": 3}`)

Issues are collected for the whole input (not first-error-only). The web error handler converts them into `*errs.ValidationError` → 422 / redirect + flash / problem+json ([http.md](http.md#errors)).

## Struct-tag shorthand

```go
type Login struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=12"`
}

var LoginSchema = z.FromTags[Login]() // panics at boot on unknown rules or type mismatch
```

`web.Bind[T]` uses `z.For[T]()`: the tag-derived schema, built once per type and memoized (no mutable registry). For hand-written schemas call `web.Parse(c, schema)`.

Supported rules: `required`, `min=N`, `max=N`, `len=N`, `email`, `url`, `uuid`, `ulid`, `alpha`, `alphanum`, `numeric`, `oneof=a b`, `regex=…` (last), `startswith=`, `endswith=`, `contains=`, `trim`, `lowercase`, `positive`, `unique`, `dive` (following rules apply to slice elements). `required` on a string means non-empty. Pointer fields are optional and nullable.

## Typed boundaries

| Boundary | API |
| --- | --- |
| JSON / form body | `web.Parse(c, schema)`, `web.Bind[T](c)` |
| Query string | `web.ParseQuery(c, schema)` |
| Route params | `web.ParseParams(c, schema, "id")` (invalid params → 404) |
| Config / env | `config.Load[T]` + `z.For[T]().Check(cfg)` at boot |
| Queue payloads | job schemas checked on dispatch and on execution (BETA) |
| Outgoing JSON | `web.JSONWith(c, status, schema, v)`; outside production a contract violation is logged and answered with 500 instead of sending bad data |
| Inertia props | `inertia.Render` checks props against the page schema when registered (BETA) |
| External HTTP | `z.ParseJSON(resp.Body, schema)` |

## Exports (BETA)

- `schema.JSONSchema()` → JSON Schema 2020-12 (used by the OpenAPI generator).
- `lumek codegen:types` → `resources/js/types/*.ts` with TypeScript types (and optional Zod schemas) for every registered schema, so frontend and backend share one contract.

## Design notes

- No reflection on the hot path beyond struct field assignment: field offsets and setters are resolved once when `z.Struct` is built.
- Schemas are immutable values; every modifier returns a new schema, so sharing them across goroutines is safe.
- Inspired by [Zod](https://zod.dev/) and, in Go, by [zog](https://github.com/Oudwins/zog); implemented in-house to keep the core dependency-free.
