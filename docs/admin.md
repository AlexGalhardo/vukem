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
| `Views` | embedded | `fs.FS` overriding templates by path (`layouts/admin.html`, `index.html`, `show.html`, `form.html`, `delete.html`, `dashboard.html`, `audit.html`, `flags.html`, `action.html`, `partials/pager.html`); `admin.Templates()` returns the originals to copy |

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
| GET / POST | `/admin/_flags`, `/admin/_flags/{name}` | `admin` + `manageFlags` (only with `Config.Flags` holding at least one registered flag) |
| GET | `/admin/{slug}` | `viewAny` on `(*T)(nil)` |
| GET / POST | `/admin/{slug}/create`, `/admin/{slug}` | `create` on `(*T)(nil)` |
| GET | `/admin/{slug}/{id}` | `view` on the record |
| GET / POST | `/admin/{slug}/{id}/edit`, `/admin/{slug}/{id}` | `update` on the record |
| GET / POST | `/admin/{slug}/{id}/delete` | `delete` on the record (GET is the confirmation page) |
| GET / POST | `/admin/{slug}/{id}/actions/{name}` | the action's `Name` on the record (GET is the confirmation page) |
| GET / POST | `/admin/{slug}/_bulk/{name}` (`Bulk` actions) | the action's `Name` on every selected record |

Route names are `admin.dashboard`, `admin.audit`, `admin.flags`, `admin.flags.update` and `admin.<slug>.<action>`. Lists answer htmx partial requests (search, sort, pagination) with the `list` block only.

## Custom actions

`Resource.Actions(admin.Action[T]{Name, Label, Reason, Bulk, Run})` adds operations such as "approve" or "reject with reason". `Name` (lower camel, at most 16 characters, not a built-in ability or `edit`) is the policy ability (method `Approve` for `approve`), the URL segment and the audit action. Each run goes through a confirmation page and one transaction: `Run(ctx, tx, rec, reason)` changes the record or writes through `tx`, the panel saves changed fields with `orm.Update` and writes an audit entry per record (the reason under `admin.ReasonKey`, `_reason`). `Reason` requires a reason (at most `admin.MaxReason`, 1000 characters; 422 and the form again otherwise). `Bulk` adds checkboxes and a button to the list; the selection (1 to `admin.MaxBulk`, 100, ids) is authorized record by record and a bad id, a missing record or one denial fails the whole batch before anything runs (`TestSecurity_A01_AdminActionsRequirePolicy`, `TestAdminRowAction`, `TestAdminActionReason`, `TestAdminBulkAction`). Assigning a record to a moderator is a column (`assignee_id`) edited by a form or set by an action.

## Security

- **Fail closed:** every route needs the guard, the panel ability and the policy ability; a missing ability, policy or authorizer denies (403) and logs an `authz.denied` security event (`TestSecurity_A01_AdminRequiresAuthAndPolicy`, `TestSecurity_A01_AdminRequiresAbility`).
- **CSRF:** the panel adds `csrf.Middleware` with the session-token fallback on its routes; forms carry `{{csrfField}}` (`TestSecurity_A01_AdminCSRF`).
- **Mass assignment:** only schema keys are read from the form and written; the primary key, timestamps, hidden and read-only columns never are (`TestSecurity_A01_AdminMassAssignment`).
- **Injection:** sort, filter and search columns are allowlists; anything else is ignored and values are bound parameters (`TestSecurity_A05_AdminSortAndSearchAllowlisted`).
- **Secrets:** columns tagged `json:"-"`, whose name contains `password|passwd|secret|token|api_key|private_key|recovery|otp|totp` anywhere (only the counters `tokens_used`, `tokens_count`/`_total`/`_remaining`/`_limit`, `max_tokens*`, `*_token_count` and `*footprint*` names are exempt, `TestSecurity_A09_AdminSensitiveColumnNames`), or marked `Sensitive` are never displayed, listed, searched, sorted or filtered; as form fields they are empty password inputs that keep the stored value when left blank; the audit log records only that they changed (`TestSecurity_A09_AdminAuditOmitsSensitive`).
- **Hardening (opt-in):** `Config.TwoFactor` makes 2FA mandatory (a `func(ctx) (bool, error)` the app answers from its users table; false or an error is 403 and an `admin.2fa_required` event, `TestSecurity_A07_AdminRequiresTwoFactor`); `Config.MaxSessionAge` invalidates sessions older than the cap (counted from `session.CreatedAt`) and sends the browser to log in again, with an `admin.session_expired` event (`TestSecurity_A07_AdminMaxSessionAge`); `Config.AllowCIDRs` limits the panel to client IPs (`web.Ctx.IP`, trusted-proxy aware) in the listed prefixes, 403 and an `admin.ip_denied` event otherwise, before any login redirect (`TestSecurity_A01_AdminAllowCIDRs`). A negative age or an invalid prefix fails `admin.New`.
- **Strict CSP:** templates and assets (htmx and CSS) are embedded and served from `/admin/_assets/`, with no inline scripts, styles or handlers; admin pages are `Cache-Control: no-store` and get a strict CSP when the app sent none (`TestSecurity_A02_AdminStrictHeaders`, `TestTemplatesHaveNoInlineCode`).

## Audit log

Every create, update (when something changed) and delete writes an `admin.AuditLog` row in the same transaction: actor (`AuthID` of the guard's user), resource slug, record id, action and a JSON map `column → {before, after}` of the changed columns (`{redacted: true}` for sensitive ones). Register the table with `admin.Migration("<timestamp>_create_admin_audit_logs_table")` (`install:admin` does). The log is browsable at `/admin/_audit` with the `viewAuditLogs` ability. When both sides of a change are JSON objects (a JSON column, stored as a map or as JSON text), the page lists only the differing keys as `column.key` (recursively) instead of two whole documents.

## Feature flags

With `admin.Config{Flags: ff}` (`ff, err := app.Flags()`) the panel lists every registered [feature flag](subsystems.md#feature-flags) at `/admin/_flags`: name, description, kind, default (code or `FLAG_<NAME>`), current global value and whether it is stored. Boolean flags toggle with one button, int and string flags take a value, and "Reset to default" deletes the stored value. The `install:admin` scaffold passes `app.Flags()` and gives `manageFlags` to the same admins; an app that ran `install:admin` before v0.27.0 adds `Flags: ff` to its `vadmin.Config` and defines the ability on its gate.

- Both routes need the `manageFlags` gate ability (fail closed: an undefined ability denies) and the panel's CSRF check (`TestSecurity_A01_AdminFlagsRequireAbilityAndRespectLocks`).
- Locked flags are shown read-only; a crafted write is refused with 403 and logged as a `flags.locked_write` security event.
- A save that leaves the effective value as it was records nothing. Every change writes a `flags.changed` audit event (`web.AuditEvent`, with `actor_id`, `flag`, `changed`, `old` and `new`) and an audit-log row (resource `_flags`, record = flag name, `value: {before, after}`), browsable at `/admin/_audit?resource=_flags`. String flag values are recorded only as their length and a SHA-256 prefix (`10 bytes, sha256:1a2b3c4d`), never in clear (`TestSecurity_A09_AdminFlagsRedactStringValues`).
- The flag write and the audit row are **not atomic** (the store is pluggable): when the row fails after the flag changed, the change is logged at ERROR (redacted the same way) and the page redirects with a warning flash.
- When the store is down the page answers 503 instead of showing defaults that would invite wrong toggles.
- Flag values are not secrets: never store credentials in a string flag.
