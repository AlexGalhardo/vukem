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
| `z.String()` | `string` | `Min`, `Max`, `Len`, `Email`, `URL`, `UUID`, `ULID`, `Regex`, `Alpha`, `Alphanum`, `Numeric`, `StartsWith`, `EndsWith`, `Includes`, `HasLower`, `HasUpper`, `HasDigit`, `HasSymbol`, `OneOf`; formats `IP`, `IPv4`, `IPv6`, `CIDR`, `MAC`, `Hostname`, `E164`, `Slug`, `Hex`, `Base64`, `Base64URL`, `JWT`, `ISODate`, `ISOTime`, `ISODateTime`, `ISODuration`; `Trim`, `ToLower`, `ToUpper`, `NormalizeEmail`, `Escape`, `Preprocess(fn)` |
| `z.Int()`, `z.Int64()`, `z.Uint()`… | integer types | `Min`, `Max`, `Positive`, `NonNegative`, `MultipleOf` (integer arithmetic, exact above 2^53; floats compare by quotient with a relative tolerance of 1e-9, so `MultipleOf(0.01)` accepts `0.07`) |
| `z.Float()` | `float64` | `Min`, `Max`, `Positive`, `MultipleOf` (NaN and infinities are always rejected) |
| `z.Bool()` | `bool` | — |
| `z.Time()` | `time.Time` | `After`, `Before`, `AfterOrEqual`, `BeforeOrEqual`, `Future`, `Past` (read the clock at parse time; `FutureAt(now)` and `PastAt(now)` take your own), `Layouts(...)` (default RFC 3339) |
| `z.Enum(vals...)` | `string` (or any `~string` via `z.EnumOf[T]`) | membership |
| `z.Slice(elem)` | `[]E` (E = element schema type; a `z.Schema[[]E]`) | `Min`, `Max`, `Len`, `NonEmpty`, `Unique`; `Default([]E)`, `Refine`, `Transform`, `Message`, `Describe` |
| `z.Map(value)` | `map[string]V` (a `z.Schema[map[string]V]`) | `Min`, `Max` (entry count); `Default(map[string]V)`, `Refine`, `Transform`, `Message`, `Describe` |
| `z.Struct[T](z.Shape{...})` | `T` | field schemas mapped by `json`/`form`/`query` tag or field name; `Strict()` rejects unknown keys |
| `z.Custom[T](fn)` | `T` | user parser |
| `z.Codec(wire, decode, encode)` | the domain type `Out` | parses the wire form, decodes it into `Out`; `Encode(v)` goes back and `Check` uses it; see [Codecs and branded types](#codecs-and-branded-types) |
| `z.Brand[B](schema)` | the defined type `B` | the inner schema, converted to `B` (`type UserID int64`) |
| `z.Literal[T](values...)` | `T` (string, bool or number kinds) | exactly one of the values (code `invalid_literal`) |
| `z.Union[T](variants...)` | `T`, usually `any` or a domain interface | the first variant that parses wins; when none does, one `invalid_union` issue at the union's path. Order matters with coercion: `Union[any](Int(), String())` turns `"5"` into `5`; `Check` does not coerce, so it can refuse what `Parse` takes from a form, and the `Refine`/`Transform` of a variant run on every attempt |
| `z.DiscriminatedUnion[T](key, variants)` | `T`, usually a domain interface | picks the variant by the string value of `key`, then reports that variant's own issues; an unknown or missing value is `invalid_union_discriminator` at `key` |
| `z.Tuple[T](items...)` | struct `T` | a fixed-length array read into the exported fields of `T` in order; errors point at the position (`location.0`) |
| `z.Lazy[T](fn)` | `T` | builds the schema on first use, so a schema can contain itself; a deeper nesting than 1000 is a `too_deep` issue. `JSONSchema` exports it as a `$ref` into `$defs` |

`z.Union`, `z.DiscriminatedUnion`, `z.Tuple`, `z.Lazy` and `z.Literal` have no form in `validate:"..."` struct tags: a tag describes a field by its Go type, and these need an explicit schema.

Every schema has the same modifiers: `Optional`, `Nullable`, `Default`, `Refine`, `Transform`, `Message` and `Describe`, plus `Parse` and `Check`. `z.File()` is the exception: it has no `Nullable`, `Default` or `Transform`.
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

### Deriving schemas

Build the schema of an update from the one of a create without repeating the rules. The target type is the first type argument; the source type is inferred.

```go
type CreatePost struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type UpdatePost struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

var CreatePostSchema = z.Struct[CreatePost](z.Shape{
	"title": z.String().Trim().Min(3).Max(120),
	"body":  z.String().Trim().Max(10_000),
})

var UpdatePostSchema = z.Partial[UpdatePost](CreatePostSchema)
```

| Function | Result |
| --- | --- |
| `z.Pick[U](s, keys...)` | only `keys`; panics for a key `s` does not have |
| `z.Omit[U](s, keys...)` | everything but `keys`; panics for a key `s` does not have |
| `z.Extend[U](s, extra)` | the fields of `s` plus `extra`; a key of `extra` replaces the one of `s` |
| `z.Partial[U](s)` | every field may be absent; use pointer fields in `U` to tell absent from zero |

They reuse the field schemas and check the shape against `U` when built, like `z.Struct`. `Strict()`, `EmptyAsAbsent()` and `Bail()` are inherited, and so are the cross-field rules (`Confirmed`, `SameAs`, `RequiredIf`, `AfterField` and the others) whose keys all remain in the derived schema: `z.Partial` keeps `Confirmed("password")`, while `z.Omit(s, "password")` drops it, as it would have nothing to compare. `RequiredWhen` is never carried over, and neither are the struct-level `Refine`, `Transform`, `Default` and `Passthrough` of `s`: they are written for `s`'s type. `Strict()` rejects the keys outside the shape; `Passthrough(func(dst *T, extra map[string]any))` hands them to a callback instead (the extra keys are untrusted and unvalidated: copy and validate them before storing) (the two cannot be combined).

## Cross-field rules

Rules that compare fields are methods of `StructSchema[T]`, or `validate` tags on the field they apply to. They run after every field was read, see the input as sent (to know whether a key "exists": present, not null and not empty) and the parsed struct (to compare values), and never add an issue to a field that already failed. The issue is reported at the field the rule is on, and its params carry field names, never values: a confirmation is usually a password.

| Method | Tag (on the field) | Key | Code |
| --- | --- | --- | --- |
| `Confirmed(key)` | `confirmed` | `confirmed` | `custom` |
| `SameAs(key, other)` | `same=other` | `same` | `custom` |
| `NotSameAs(key, other)` | `different=other` | `different` | `custom` |
| `RequiredIf(key, other, values...)` | `required_if=other v1 v2` | `required_if` | `required` |
| `RequiredWhen(key, func(T) bool)` | none | `required` | `required` |
| `RequiredIfExists(key, others...)` | `required_with_all=a b` | `required_with` | `required` |
| `RequiredIfAnyExists(key, others...)` | `required_with=a b` | `required_with` | `required` |
| `RequiredIfMissing(key, others...)` | `required_without_all=a b` | `required_without` | `required` |
| `RequiredIfAnyMissing(key, others...)` | `required_without=a b` | `required_without` | `required` |
| `AfterField(key, other)` | `gtfield=other` | `date.after_field` | `too_small` |
| `AfterOrEqualField(key, other)` | `gtefield=other` | `date.after_or_equal_field` | `too_small` |
| `BeforeField(key, other)` | `ltfield=other` | `date.before_field` | `too_big` |
| `BeforeOrEqualField(key, other)` | `ltefield=other` | `date.before_or_equal_field` | `too_big` |

- `Confirmed("password")` compares the input `password` with `password_confirmation`, which need not be in the shape; a missing confirmation counts as different.
- `Strict()` does not reject the `<key>_confirmation` input of a `Confirmed` rule, and `Passthrough` does not hand it over.
- On a typed value (`Check(v)`, or `Parse` given a `T`) there is no raw input to ask whether a key was *sent*, so the rules that depend on it are skipped: `Confirmed` and the `RequiredIf*`, `RequiredWhen`, `required_with*` and `required_without*` rules. The comparison rules (`SameAs`, `NotSameAs`, the `*Field` date rules) still apply, to the fields that hold a non-zero value. `RequiredIf` reads the parsed value of the field it watches, so a `Default` on it counts.
- A tag cites the other field by its **input key** (`json`/`form` name), not the Go name. Tag values are separated by spaces, so a value with a space needs the method.
- The `RequiredIf*` and `RequiredWhen` rules need the target to be optional in the shape (a required key could never be missing); a key that is not in the shape, or `SameAs` over two fields of different Go types, is a panic at boot.
- Rules that need a database or the network do not exist in `z`: they live in `orm.Validate`.
- `JWT()` only checks the shape of a token (three base64url segments, a JSON header with an `alg`): it never verifies a signature, so use `auth/jwt` to accept one. `Escape()` stores HTML entities (`Tom &amp; Jerry`), which is wrong anywhere that is not HTML: `html/template` already escapes on output. It is not idempotent (a form that posts back the stored value escapes it twice) and the checks after it, `Min` and `Max` among them, count the escaped text. `Hex()`, `Base64()`, `Base64URL()` and `JWT()` read the whole value to check it: put a `Max(n)` on them to bound what a client can make the server read, and note that `JWT()` accepts a token whose `alg` is `none`: it is only a shape check.

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

For a string, `""` is a valid value. `EmptyAsAbsent()` on a struct reads an empty string sent for any field as a missing key, the way an HTML form sends a blank input: an optional field takes its `Default`, a required one reports `required`. `Bail()` makes each field report only its first issue (the checks of strings and numbers stop at the first failure); without it every issue of every field is collected.

## Codecs and branded types

`z.Transform` keeps the type. A `z.Codec` changes it and says how to go back, which `Check` and the JSON Schema need:

```go
// Seconds on the wire, a time.Duration in the domain.
var TTL = z.Codec(z.Int64().Min(0),
	func(sec int64) (time.Duration, error) { return time.Duration(sec) * time.Second, nil },
	func(d time.Duration) (int64, error) { return int64(d / time.Second), nil },
)

d, err := TTL.Parse(json.Number("90")) // 1m30s
sec, err := TTL.Encode(d)              // 90, validated by the wire schema
```

An error from `decode` becomes an issue like a `Refine` error does: `z.FieldError` and `z.FieldErrorKey` are honored and any other error gets the generic message, its text kept in `Issue.Err` for the logs. The JSON Schema is the wire schema's.

`z.Brand[B](schema)` converts the result to the defined type `B`, whose underlying type must be the schema's: `type UserID int64` over `z.Int64().Positive()`.

## Errors

`Parse` returns `z.Issues` (a `[]z.Issue` implementing `error`), each with:

- `Path` — dotted path with indexes: `tags.2`, `author.email`
- `Code` — stable machine code: `required`, `invalid_type`, `too_small`, `too_big`, `invalid_string`, `invalid_enum`, `invalid_literal`, `invalid_union`, `invalid_union_discriminator`, `custom`
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

`web.Bind` and `web.Parse` ignore keys the schema does not name; to reject them, parse with a strict schema (`web.Parse(c, z.For[T]().Strict())`, or `.Strict()` on a `z.Struct`) or decode with `c.BindJSON`.

Supported rules, by the field kind they apply to:

| Rule | Applies to | Meaning |
| --- | --- | --- |
| `required` | any kind | the key must be present and not null |
| `omitempty` | string | `OmitEmpty()`: skips every other rule when the value is empty after the pre-steps, so `omitempty,oneof=a b` accepts `a`, `b` and `""`. On any other kind it is a boot error, because skipping the rules for a zero number or an empty list would make `omitempty,min=1` accept `0` and `[]` |
| `dive` | any kind | the rules after it apply to the slice elements or the map values |
| `min=N` | string, number, list, map | minimum length, value, element count or entry count |
| `max=N` | string, number, list, map | maximum length, value, element count or entry count |
| `len=N` | string, list | exact length or element count |
| `email` | string | `Email()` |
| `url` | string | `URL()` |
| `uuid` | string | `UUID()` |
| `ulid` | string | `ULID()` |
| `alpha` | string | `Alpha()` |
| `alphanum` | string | `Alphanum()` |
| `numeric` | string | `Numeric()` |
| `oneof=a b c` | string | `OneOf("a", "b", "c")`, values separated by spaces |
| `regex=…` | string | `Regex(...)`; must be the last rule of the tag |
| `startswith=…` | string | `StartsWith(...)` |
| `endswith=…` | string | `EndsWith(...)` |
| `contains=…` | string | `Includes(...)` |
| `trim` | string | `Trim()`, a pre-step: the value is trimmed before the checks run |
| `lowercase` | string | `ToLower()`, a pre-step, like `trim` |
| `positive` | number | value > 0 |
| `default=V` | string, number | `Default(V)`: used when the key is absent, or empty with `omitempty`; it also reaches the OpenAPI document. Never re-validated, so a value outside the field's own rules is a boot error. Contradicts `required` |
| `unique` | list | `Unique()` |
| `ip` | string | `IP()`: an IPv4 or IPv6 address, no zone |
| `ipv4` | string | `IPv4()` |
| `ipv6` | string | `IPv6()`, no zone |
| `cidr` | string | `CIDR()`: an address with a prefix length |
| `mac` | string | `MAC()`: a 48-bit hardware address |
| `hostname` | string | `Hostname()` |
| `e164` | string | `E164()`: an international phone number |
| `slug` | string | `Slug()` |
| `hex` | string | `Hex()` |
| `base64` | string | `Base64()` |
| `base64url` | string | `Base64URL()` |
| `jwt` | string | `JWT()`: the shape of a token only, never its signature (`alg: none` passes) |
| `iso_date` | string | `ISODate()` |
| `iso_time` | string | `ISOTime()` |
| `iso_datetime` | string | `ISODateTime()` |
| `iso_duration` | string | `ISODuration()` |
| `uppercase` | string | `ToUpper()`, a pre-step, like `trim` |
| `normalize_email` | string | `NormalizeEmail()`, a pre-step: trimmed and lower-cased |
| `escape` | string | `Escape()`, a pre-step: HTML entities are stored, escaped again if posted back, and counted by `min`/`max`; see [Cross-field rules](#cross-field-rules) |
| `confirmed` | any kind | `Confirmed(key)`: equals the `<key>_confirmation` input |
| `same=KEY` | any kind | `SameAs(key, other)`; cites the other field by its input key |
| `different=KEY` | any kind | `NotSameAs(key, other)` |
| `required_if=KEY V1 V2` | any kind | `RequiredIf(key, other, values...)`; values separated by spaces, the field must be optional |
| `required_with=K1 K2` | any kind | `RequiredIfAnyExists`: required when any of the keys was sent |
| `required_with_all=K1 K2` | any kind | `RequiredIfExists`: required when all of the keys were sent |
| `required_without=K1 K2` | any kind | `RequiredIfAnyMissing`: required when any of the keys is missing |
| `required_without_all=K1 K2` | any kind | `RequiredIfMissing`: required when none of the keys was sent |
| `gtfield=KEY` | time field | `AfterField` |
| `gtefield=KEY` | time field | `AfterOrEqualField` |
| `ltfield=KEY` | time field | `BeforeField` |
| `ltefield=KEY` | time field | `BeforeOrEqualField` |

A bool, a `time.Time`, an `any` (any JSON value), a `map[string]any` (a JSON object) and a nested struct take no rule but `required`; any other rule on them is a boot panic, as is an unknown rule or a bad argument. `required` on a string means non-empty; on a list it means present and not null (an empty list passes, as with OpenAPI `required`), so use `min=1` for a non-empty list. Pointer fields are optional and nullable. A `map[string]V` field is parsed from a JSON object with `V` derived recursively; `min`/`max` count entries and the rules after `dive` apply to each value (`map[string]string` with `validate:"min=1,dive,max=20"`). Only string keys are supported; any other key type is a boot panic, as is an unsupported field kind.

The table above is checked against the implementation by `TestTagRules_DocumentedInValidationDocs` in `z`: a rule cannot be added to the tag parser without appearing here.

## Typed boundaries

| Boundary | API |
| --- | --- |
| JSON / form body | `web.Parse(c, schema)`, `web.Bind[T](c)` |
| Query string | `web.ParseQuery(c, schema)` |
| Route params | `web.ParseParams(c, schema, "id")` (invalid params → 404) |
| Config / env | `config.Load[T]` + `z.For[T]().Check(cfg)` at boot |
| A typed value that must also be cleaned | `schema.Normalize(v)` returns it with `Trim`, defaults and `Transform` applied (`Check` only validates) |
| Outgoing JSON | `web.JSONWith(c, status, schema, v)`; outside production a contract violation is logged and answered with 500 instead of sending bad data |
| External HTTP | `z.ParseJSON(resp.Body, schema)` |

## Exports

- `z.JSONSchema(schema)` → JSON Schema 2020-12 (types, required keys, lengths, ranges, formats, patterns, enums, defaults, descriptions; `Refine`/`Custom` logic cannot be expressed).
- `vukem codegen:types [-zod] [-o file]` → TypeScript types (and optional Zod schemas) for every schema and operation of the app's OpenAPI document, so frontend and backend share one contract ([api.md](api.md#types-for-clients)).

## Design notes

- Rules never do I/O; database-backed rules live in `orm.Validate`.
- No reflection on the hot path beyond struct field assignment: field offsets and setters are resolved once when `z.Struct` is built.
- Schemas are immutable values; every modifier returns a new schema, so sharing them across goroutines is safe.
- Inspired by [Zod](https://zod.dev/) and, in Go, by [zog](https://github.com/Oudwins/zog); implemented in-house to keep the core dependency-free.
