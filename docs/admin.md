# Admin panel

Package `admin` (stdlib only, core) generates an admin panel from ORM models: paginated lists with search, sort and filters, detail pages, create/edit forms built from `z` schemas, deletes with a confirmation page, and an audit log of every change. Plan tasks `B.7.1` (package) and `B.7.2` (`install:admin`, `make:admin-resource`).

## Quick start

```sh
vukem install:auth
vukem install:admin
vukem make:admin-resource Post
vukem migrate
ADMIN_EMAILS=you@example.com vukem serve   # then open /admin
```

`install:admin` (needs `install:auth`) writes `routes/admin.go` (the panel with its own `authz.Gate`), `app/admin/admin.go` (`Admins`, read from `ADMIN_EMAILS`; nobody is an admin when it is empty, and a listed address counts only once verified, so signing up first with an admin's address grants nothing), `app/admin/registry.go` (`Resources()`, `Policies(admins)`), the audit-log migration and `tests/admin_test.go`; it registers the migration and the route file and adds `ADMIN_EMAILS` to `.env.example`. `make:admin-resource <Model>` checks that `models.<Model>` exists, writes `app/admin/<model>.go` (the resource, using `requests.<Model>Schema` when it exists, and a policy allowing admins every action) and `tests/admin_<model>_test.go`, and registers both in `app/admin/registry.go`.

## Wiring

```go
gate := authz.New(func(ctx context.Context) (*models.User, error) {
	u, err := auth.UserFrom[models.User](ctx, "web")
	return &u, err
})
gate.Define(admin.AbilityAdmin, func(_ context.Context, u *models.User) bool { return u.IsAdmin })
gate.Define(admin.AbilityAudit, func(_ context.Context, u *models.User) bool { return u.IsAdmin })
if err := gate.Register(authz.For[*models.Post](PostPolicy{})); err != nil {
	return err
}

panel, err := admin.New(admin.Config{DB: db, Guard: acc.Web, Authorizer: gate})
if err != nil {
	return err
}
if err := panel.Register(
	admin.NewResource[models.Post](requests.PostSchema).
		Search("title", "body").
		Filter("status", "draft", "published").
		List("id", "title", "status", "created_at"),
); err != nil {
	return err
}
return panel.Mount(r)
```

| `Config` field | Default | Meaning |
| --- | --- | --- |
| `DB` | required | Stores records and the audit log |
| `Guard` | required | Authenticates admins (`auth.Checker`); guests go to the `login` route (401 for JSON) |
| `Authorizer` | `Services.Authorizer` | Decides the panel and policy abilities (usually an `authz.Gate`); with none, everything is denied |
| `Prefix` | `/admin` | URL prefix |
| `Ability` | `admin` | Gate ability required on every page |
| `Title` | `Admin` | Header text |
| `Views` | embedded | `fs.FS` overriding templates by path (`layouts/admin.html`, `index.html`, `show.html`, `form.html`, `delete.html`, `dashboard.html`, `audit.html`, `partials/pager.html`); `admin.Templates()` returns the originals to copy |

## Resources

`admin.NewResource[T](schema)` takes the model type explicitly and infers the input type `I` from a `z.StructSchema[I]`. Columns come from the ORM metadata (`orm.FieldsOf[T]`); form fields come from the schema keys, each written to the model column with the same name (or the snake_case of the input field name). Form controls follow the schema's JSON Schema: enums → select, booleans → checkbox (with a hidden `false`), numbers → number, `Email()` → email, `URL()` → url, dates → `datetime-local`, strings with `Max(n ≤ 255)` → text, other strings → textarea.

| Option | Effect |
| --- | --- |
| `Slug(s)` / `Labels(singular, plural)` / `Label(col, text)` | URL segment (default: table in kebab-case) and UI names |
| `List(cols...)` | List columns, in order (default: every visible column) |
| `Column(label, func(T) string)` | Computed list and detail column (escaped) |
| `Search(cols...)` | Search box over string columns (`LIKE`, `ILIKE` on Postgres, bound parameters; `%` and `_` in the text match literally) |
| `Sortable(cols...)` | Sortable columns (default: the listed ones) |
| `Filter(col, options...)` | Exact-match filter; a select with options (yes/no for booleans), else a text box |
| `PerPage(n)` | Page size (default 20, max 200) |
| `Hidden(cols...)` | Not shown anywhere; hidden schema keys are never written |
| `ReadOnly(cols...)` | Shown disabled on edit, never written |
| `Sensitive(cols...)` | Secret columns (see below) |
| `Input(key, kind)` | Override a form control (`admin.InputTextarea`, ...) |
| `BeforeSave(fn)` | Runs before create/update inside the transaction (hash a password, set an owner); returning `z` issues re-renders the form |

Configuration mistakes (unknown or sensitive columns in lists, a schema key without a column, a schema key for the primary key or timestamps, duplicate slugs) are returned by `Panel.Register`, at boot.

## Routes

| Method | Path | Ability |
| --- | --- | --- |
| GET | `/admin` | `admin` (dashboard: counts of resources the user may `viewAny`) |
| GET | `/admin/_audit` | `admin` + `viewAuditLogs` |
| GET | `/admin/{slug}` | `viewAny` on `(*T)(nil)` |
| GET / POST | `/admin/{slug}/create`, `/admin/{slug}` | `create` on `(*T)(nil)` |
| GET | `/admin/{slug}/{id}` | `view` on the record |
| GET / POST | `/admin/{slug}/{id}/edit`, `/admin/{slug}/{id}` | `update` on the record |
| GET / POST | `/admin/{slug}/{id}/delete` | `delete` on the record (GET is the confirmation page) |

Route names are `admin.dashboard`, `admin.audit` and `admin.<slug>.<action>`. Lists answer htmx partial requests (search, sort, pagination) with the `list` block only.

## Security

- **Fail closed:** every route needs the guard, the panel ability and the policy ability; a missing ability, policy or authorizer denies (403) and logs an `authz.denied` security event (`TestSecurity_A01_AdminRequiresAuthAndPolicy`, `TestSecurity_A01_AdminRequiresAbility`).
- **CSRF:** the panel adds `csrf.Middleware` with the session-token fallback on its routes; forms carry `{{csrfField}}` (`TestSecurity_A01_AdminCSRF`).
- **Mass assignment:** only schema keys are read from the form and written; the primary key, timestamps, hidden and read-only columns never are (`TestSecurity_A01_AdminMassAssignment`).
- **Injection:** sort, filter and search columns are allowlists; anything else is ignored and values are bound parameters (`TestSecurity_A05_AdminSortAndSearchAllowlisted`).
- **Secrets:** columns tagged `json:"-"`, matching `password|secret|token|api_key|private_key|recovery|otp`, or marked `Sensitive` are never displayed, listed, searched, sorted or filtered; as form fields they are empty password inputs that keep the stored value when left blank; the audit log records only that they changed (`TestSecurity_A09_AdminAuditOmitsSensitive`).
- **Strict CSP:** templates and assets (htmx and CSS) are embedded and served from `/admin/_assets/`, with no inline scripts, styles or handlers; admin pages are `Cache-Control: no-store` and get a strict CSP when the app sent none (`TestSecurity_A02_AdminStrictHeaders`, `TestTemplatesHaveNoInlineCode`).

## Audit log

Every create, update (when something changed) and delete writes an `admin.AuditLog` row in the same transaction: actor (`AuthID` of the guard's user), resource slug, record id, action and a JSON map `column → {before, after}` of the changed columns (`{redacted: true}` for sensitive ones). Register the table with `admin.Migration("<timestamp>_create_admin_audit_logs_table")` (`install:admin` does). The log is browsable at `/admin/_audit` with the `viewAuditLogs` ability.
