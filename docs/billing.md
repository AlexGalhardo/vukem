# Billing (`vukem install:billing`)

Stripe subscriptions for the users of `install:auth`, built on [`contrib/stripe`](subsystems.md#stripe-contribstripe): plans, Stripe Checkout, the Stripe Billing Portal, subscriptions synced from webhooks, and middleware that gates routes on a subscription. Like the other `install:*` scaffolds, the code is copied into the project and is yours to change.

```sh
vukem install:auth       # required first: billing bills its users
vukem install:billing
vukem migrate
```

It works for the HTMX starter and for Inertia apps (React, Vue, Svelte): the shared code is the same, only the billing page differs. It refuses to run without `install:auth`.

## What it adds

| File | Contents |
| --- | --- |
| `config/billing.go` | `config.Billing{Key, WebhookSecret, Plans}` from `STRIPE_KEY`, `STRIPE_WEBHOOK_SECRET` and one `STRIPE_PRICE_<PLAN>` per plan; the plans (`basic`, `pro`) are listed in `plans()` |
| `database/migrations/<ts>_add_billing_tables.go` | `users.stripe_customer_id` (nullable, unique) and a `subscriptions` table: `user_id`, `stripe_subscription_id` (unique), `status`, `price_id`, `current_period_end`, `cancel_at_period_end`, timestamps |
| `app/models/billing.go` | `models.Billable` (the Stripe columns of `users`, so `User` is untouched) and `models.Subscription` with `Valid()` (`active` or `trialing`) |
| `app/billing/billing.go` | The `Billing` service: `Customer`, `Checkout`, `Portal`, `Current`, `SyncSubscription`, `HandleWebhook`, the `Webhook` mount and the `Subscribed`/`OnPlan` middleware |
| `app/controllers/account/billing.go`, `billing_page.go` | Handlers; the page is `resources/views/billing/show.html` (HTMX) or `resources/js/pages/Billing.{tsx,vue,svelte}` (Inertia) |
| `routes/billing.go` | The routes below, registered in `routes/registry.go` |
| `tests/billing_test.go`, `tests/billing_page_test.go` | Tests against a fake Stripe API |

`.env.example` gets `STRIPE_KEY`, `STRIPE_WEBHOOK_SECRET` and `STRIPE_PRICE_*` with empty values, and `go.mod` requires `github.com/AlexGalhardo/vukem/contrib/stripe` pinned to `v0.1.0` (a `replace` to the checkout's `contrib/stripe` for projects created with `vukem new --framework`).

## Routes

| Route | Behaviour |
| --- | --- |
| `GET /billing` (`billing.show`) | Plans and the current subscription; `?checkout=success` only shows a thank-you note, it grants nothing |
| `POST /billing/checkout/{plan}` (`billing.checkout`) | Creates the Stripe customer on first use (`users.stripe_customer_id`), then a Checkout Session in `subscription` mode with `client_reference_id` = user id, and redirects with 303. Unknown plans are 404; subscribers are sent back to `/billing` (plan changes happen in the portal) |
| `POST /billing/portal` (`billing.portal`) | Redirects to a Billing Portal session (return URL `/billing`); users without a customer are sent back to `/billing` |
| `POST /stripe/webhook` | `stripe.WebhookHandler`, mounted with `r.UseServer` ahead of the session and CSRF middleware (not listed by `route:list`) |

The checkout and portal routes need `auth.Required`. They leave the app with a 303 for forms, `inertia.Location` (409 + `X-Inertia-Location`) for Inertia visits, `HX-Redirect` for htmx and `{"url": …}` for JSON clients. Checkout never passes `payment_method_types`: payment methods come from the Dashboard.

## Webhooks keep subscriptions in sync

Access comes from webhooks only, never from the success page. `routes/billing.go` registers `b.HandleWebhook` with `events.Listen` (synchronous, so a failure answers 500 and Stripe redelivers). It handles:

- `checkout.session.completed` and `checkout.session.async_payment_succeeded`: links the customer to the `client_reference_id` user when that user has none (a session cannot move another user's customer), then syncs the subscription only when `payment_status` is `paid` or `no_payment_required`; delayed payment methods wait for `async_payment_succeeded`.
- `customer.subscription.created`, `.updated`, `.deleted`, `.paused`, `.resumed`.
- `invoice.paid` and `invoice.payment_failed` (subscription from `parent.subscription_details`, or `subscription` on older API versions).

Each sync fetches the subscription from Stripe (events can arrive out of order) and upserts its row by `stripe_subscription_id`, for the user whose `stripe_customer_id` matches; unknown customers are logged and acknowledged. Duplicate deliveries are acknowledged without processing: processed event ids live in the app cache, so use a shared store (`CACHE_STORE=database` or Redis) when several instances run.

Subscribe the endpoint to those events in the Dashboard (Developers → Webhooks).

## Middleware

```go
b, err := billing.New(app)
// ...
r.Get("/reports", reports).Use(auth.Required(acc.Web), b.Subscribed())
r.Get("/exports", exports).Use(auth.Required(acc.Web), b.OnPlan("pro"))
```

`Subscribed()` lets through users whose subscription is `active` or `trialing`; `OnPlan(plans...)` also requires one of the named plans. Everyone else is redirected to `/billing` with a flash message, or gets 402 problem+json when the client wants JSON. Missing users, unknown plans and database errors deny too (fail closed).

## Configuration and development

| Key | Value |
| --- | --- |
| `STRIPE_KEY` | A restricted key (`rk_…`) with write access to Customers, Checkout Sessions and Customer portal and read access to Subscriptions; a secret key (`sk_…`) works but logs a warning |
| `STRIPE_WEBHOOK_SECRET` | The endpoint signing secret (`whsec_…`) |
| `STRIPE_PRICE_<PLAN>` | A recurring price id (`price_…`) per plan in `config/billing.go` |

In production the app refuses to boot while any of them is missing. Elsewhere billing answers 503 until they are set, and the webhook refuses every request without a signing secret.

Develop against a Stripe sandbox, never live mode:

```sh
stripe sandbox create
stripe listen --forward-to localhost:3000/stripe/webhook   # prints the whsec_ secret
```

## Tests

The generated tests boot the app with `STRIPE_API_BASE` pointing at an `httptest` fake of the Stripe API (`STRIPE_API_BASE` is ignored outside tests, so the key only ever goes to `api.stripe.com`) and sign webhooks with `stripe.SignForTest`. They cover checkout (customer created once, session parameters, the success page granting nothing), the webhook sync (unpaid, async success, `past_due`, cancellation), a duplicate event processed once, forged, replayed and unsigned webhooks rejected with 400, the middleware blocking non-subscribers and the wrong plan, the portal redirect, and an unconfigured app answering 503. The framework checks the scaffold wiring in `TestSecurity_A08_BillingWebhookOutsideCSRFButSigned` and runs the generated tests end to end.
