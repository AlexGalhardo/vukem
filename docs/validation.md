# Validation and typed data (`z`)

`z` is Vukem's Zod-inspired schema library: build a schema once, then **parse untrusted input into a typed Go value** or **check outgoing data** against the same contract. Decision `H1` in [brainstorming.md](brainstorming.md).

## Goals

- Every byte that enters the app (JSON, forms, query strings, params, headers, env, queue payloads, external API responses) is parsed through a schema into a concrete Go type.
- Data that leaves the app (JSON responses, Inertia props, queued payloads) can be checked against a schema (always in development/test, opt-in in production).
- Schemas are typed: a schema for `int` cannot be attached to a `string` field; mismatches are detected when the schema is built (at boot), not per request.
- One engine: struct tags (`validate:"required,email"`) are shorthand compiled into the same schemas.
- Schemas export to JSON Schema (`z.JSONSchema`), OpenAPI and TypeScript/Zod types (`vukem codegen:types`) for frontends.

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
| `z.String()` | `string` | `Min`, `Max`, `Len`, `Email`, `URL`, `UUID`, `ULID`, `Regex`, `Alpha`, `Alphanum`, `Numeric`, `StartsWith`, `EndsWith`, `Includes`, `OneOf`; `Trim`, `ToLower`, `ToUpper`, `Preprocess(fn)` |
| `z.Int()`, `z.Int64()`, `z.Uint()`… | integer types | `Min`, `Max`, `Positive`, `NonNegative`, `MultipleOf` |
| `z.Float()` | `float64` | `Min`, `Max`, `Positive`, `Finite` |
| `z.Bool()` | `bool` | — |
| `z.Time()` | `time.Time` | `After`, `Before`, `Layout(...)` (default RFC 3339) |
| `z.Enum(vals...)` | `string` (or any `~string` via `z.EnumOf[T]`) | membership |
| `z.Slice(elem)` | `[]E` (E = element schema type) | `Min`, `Max`, `Len`, `NonEmpty`, `Unique` |
| `z.Map(key, val)` | `map[K]V` | `Min`, `Max` |
| `z.Struct[T](z.Shape{...})` | `T` | field schemas mapped by `json`/`form`/`query` tag or field name; `Strict()` rejects unknown keys |
| `z.Custom[T](fn)` | `T` | user parser |
| `z.File()` | `*multipart.FileHeader` | `MaxSize(bytes)`, `Types(mime...)` (detected from the first 512 bytes of the content), `Extensions(...)` |

Modifiers on every schema:

| Modifier | Meaning |
| --- | --- |
| `Optional()` | key may be absent; target must be a pointer, or the zero value is kept |
| `Nullable()` | `null` accepted (pointer target) |
| `Default(v)` | value used when absent |
| `Refine(fn)` | custom check on the parsed value; return `z.FieldError` / `z.FieldErrorKey` to target a field (other errors get a generic message) |
| `Transform(fn)` | map the parsed value (same type), after the checks |
| `Message(code, msg)` | override a message |
| `Describe(text)` | description for OpenAPI/TypeScript |

Order of a parse: coercion and type check → string pre-steps (`Trim`, `ToLower`, `ToUpper`, `Preprocess`, in declaration order) → checks (`Min`, `Regex`, `Email`…) → `Transform` → `Refine`. To validate a normalized value, normalize with `Preprocess`:

```go
z.String().Preprocess(digitsOnly).Regex(e164) // Regex sees "11987654321"
z.String().Transform(digitsOnly).Regex(e164)  // Regex sees the raw input
```

The output of the pre-steps is checked again for invalid UTF-8 and control characters, and `Preprocess(nil)` panics. JSON Schema and OpenAPI keywords (`pattern`, `minLength`…) describe the normalized value, after `Trim`/`ToLower`/`Preprocess`, not the raw input.

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
- `Message` — human message (English by default; translated per request, see [Translated messages](#translated-messages))
- `Params` — values used by the message (`{"min": 3}`)

File rules report `too_big` (key `too_big.file`, params `max`, `max_kb`) and `invalid_file` (keys `file.type` with `types`, `file.extension` with `extensions`). Multipart forms put uploaded files in `c.Input()` (one `*multipart.FileHeader`, or a list for repeated or `name[]` fields), so `web.Parse` validates them with the other fields; `Check` runs the same rules on a header directly. For files too large to buffer, stream them with [`upload.Receive`](subsystems.md#uploads) instead.

Issues are collected for the whole input (not first-error-only). The web error handler converts them into `*errs.ValidationError` → 422 / redirect + flash / problem+json ([http.md](http.md#errors)).

## Translated messages

Every built-in message has a key, `Issue.Key`: the detailed key when there is one (`too_small.string`, `string.email`, `too_small.positive`), else the code (`required`, `invalid_enum`). Custom messages (`Message` overrides, `z.FieldError`) have no key and are never translated (plain `Refine`/`Custom` errors get the generic key `invalid`); return `z.FieldErrorKey` instead to make them translatable.

`Issues.Localize(lookup)` rebuilds messages from translated templates: it tries `lookup(Key)`, then `lookup(Code)`, and replaces `{field}` with `lookup("attributes.<last path segment>")` when present (else the humanized name). Untranslated issues keep their English message.

In an app, translation is opt-in and per request: `vukem.WithTranslations` + `DefaultMiddleware` install `i18n.Middleware`, which stores a localizer in the request context (`web.WithIssueLocalizer`). `web.Parse`, `Bind`, `ParseQuery`, `ParseParams` and `ParseMap` (an assembled map, used by `api.Register`) apply it before building the `*errs.ValidationError`; `web.ParseInput` has no request and stays English. Catalog keys live under `validation.`:

```json
{
  "validation": {
    "required": "O campo {field} é obrigatório.",
    "too_small": { "string": "O campo {field} deve ter pelo menos {min} caracteres." },
    "string": { "email": "O campo {field} deve ser um e-mail válido." },
    "attributes": { "email": "e-mail" }
  }
}
```

Refine callbacks (and `Custom` parsers) make their messages translatable with `z.FieldErrorKey(path, key, fallback, params)`: the issue gets `Code` `custom`, `Key` `key` and `Params` `params`, and `Message` is `fallback` with `{field}` and params filled in. `path` is relative to the refined value (`""` for the value itself). A key without a translation keeps the fallback (it never falls back to a `custom` template). With an empty `key`, `FieldErrorKey` behaves like `z.FieldError`. Placeholder names in `fallback` and in catalog templates are lower snake_case (`{field}`, `{other_field}`); other braces are left as is. Messages are plain text: escaping is the renderer's job (`html/template` does it in views).

```go
}).Refine(func(r Register) error {
	if r.Password != r.PasswordConfirmation {
		return z.FieldErrorKey("password_confirmation", "confirmed", "The {field} does not match.", nil)
	}
	return nil
})
```

`en.json` may override the fallback with `{"validation": {"confirmed": "…"}}`; `pt-BR.json`:

```json
{
  "validation": {
    "confirmed": "A {field} não confere.",
    "attributes": { "password_confirmation": "confirmação de senha" }
  }
}
```

A handler can also return `z.FieldError`, `z.FieldErrorKey` or `z.Issues` directly (wrapped with `%w` too), for checks that need the database or other services: the default error handler treats them like `*errs.ValidationError`, so the client gets a 422 with field errors (problem+json for JSON clients; with sessions, classic and Inertia form posts flash the errors and redirect back; an HTML page otherwise, and always for `GET`/`HEAD`, which never redirect back), localized when the i18n middleware runs. `z.AsIssues` returns the single issue of a `FieldError`, and `errs.Status` reports 422 for both. These 422s are logged at Info.

When the data being validated is not client input (a database row, an upstream API response), wrap the error so it is not reported to the client as its own mistake: `return errs.Wrap(http.StatusInternalServerError, err)` answers a plain 500 with no field errors.

Return `z.FieldError` or `z.FieldErrorKey` from `Refine` and `Custom` for any message meant for the client. Any other error fails closed: the issue gets the generic message "The {field} field is invalid." (key `invalid`, so `validation.invalid` translates it), its text stays in `Issue.Err`, and `web.Parse`/the error handler log it at Warn. Before v0.25 the raw `err.Error()` text was the message.

```go
if taken {
	return z.FieldErrorKey("email", "unique", "The {field} has already been taken.", nil)
}
```

A `Custom` parser that returns `z.FieldError` or `z.FieldErrorKey` reports the issue at the sub-path with the bare message, like `Refine` (before v0.24 it was one issue at the schema path reading `path: msg`).

Placeholders are the issue `Params` (`{min}`, `{max}`, `{len}`, `{options}`…). `{expected}` in `invalid_type` is English text (`a string`); translate the whole message instead of reusing it.

## Struct-tag shorthand

```go
type Login struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=12"`
}

var LoginSchema = z.FromTags[Login]() // panics at boot on unknown rules or type mismatch
```

`web.Bind[T]` uses `z.For[T]()`: the tag-derived schema, built once per type and memoized (no mutable registry). For hand-written schemas call `web.Parse(c, schema)`.

Supported rules: `required`, `min=N`, `max=N`, `len=N`, `email`, `url`, `uuid`, `ulid`, `alpha`, `alphanum`, `numeric`, `oneof=a b`, `regex=…` (last), `startswith=`, `endswith=`, `contains=`, `trim`, `lowercase`, `positive`, `unique`, `dive` (following rules apply to slice elements). `required` on a string means non-empty; on a list it means present and not null (an empty list passes, as with OpenAPI `required`), so use `min=1` for a non-empty list. Pointer fields are optional and nullable.

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

## Exports

- `z.JSONSchema(schema)` → JSON Schema 2020-12 (types, required keys, lengths, ranges, formats, patterns, enums, defaults, descriptions; `Refine`/`Custom` logic cannot be expressed).
- `vukem codegen:types [-zod] [-o file]` → TypeScript types (and optional Zod schemas) for every schema and operation of the app's OpenAPI document, so frontend and backend share one contract ([api.md](api.md#types-for-clients)).

## Design notes

- No reflection on the hot path beyond struct field assignment: field offsets and setters are resolved once when `z.Struct` is built.
- Schemas are immutable values; every modifier returns a new schema, so sharing them across goroutines is safe.
- Inspired by [Zod](https://zod.dev/) and, in Go, by [zog](https://github.com/Oudwins/zog); implemented in-house to keep the core dependency-free.
