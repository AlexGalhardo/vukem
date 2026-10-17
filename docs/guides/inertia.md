# Inertia starters (React, Vue, Svelte)

Go controllers and routing on the server, React, Vue or Svelte pages in the browser through the [Inertia.js v3](https://inertiajs.com) protocol, on Vite + Tailwind + SQLite. The three starters share the Go side; only `resources/js` differs. Owning docs: [frontend.md](../frontend.md#inertia-v3-beta).

## Create

```sh
vukem new blog --inertia react     # or: --inertia vue, --inertia svelte
cd blog
vukem migrate
```

`--inertia` takes the framework name as its value. Without stack flags on a terminal, `vukem new blog` asks for the stack and then the framework. An existing HTMX starter project converts in place (its pages, controllers and tests are replaced, hence `--force`):

```sh
vukem install:inertia --force --react      # or --vue, --svelte; --no-install skips npm install
```

## Run

```sh
vukem serve        # http://localhost:3000, plus the Vite dev server with hot reload
```

`vukem serve` rebuilds and restarts the Go app on `.go` changes and runs `npm run dev`; page edits reload through Vite. `npm run typecheck` type-checks the pages: `tsc` for React, `vue-tsc` for Vue, `svelte-check` for Svelte.

## Project layout

The Go side is the [HTMX starter's](htmx.md#project-layout) with these differences:

```text
bootstrap/app.go                vukem.WithInertia(inertia.Config{})
routes/web.go                   r.Use(app.Inertia.Middleware, shareProps(app)); shared props (appName, ...)
app/controllers/                handlers return inertia.Render(c, "Posts/Index", props)
resources/views/app.html        the root view: CSP nonce, {{vite}}, {{.Head}}, {{.Inertia}}
resources/js/app.*              the Inertia client entry
resources/js/pages/             one component per page: Home, Posts/Index, Posts/Show, ...
resources/js/layouts/           AppLayout, the default layout
resources/js/components/        Field, PostForm
resources/js/types.ts           TypeScript shapes of the props the controllers send
tests/                          tests asserting on the page component and props
```

## Add a page

The Go handler names the page component and passes its props:

```go
// app/controllers/about.go
func About(c *web.Ctx) error {
	return inertia.Render(c, "About", inertia.Props{"version": "1.0"})
}

// routes/web.go, inside Web
r.Get("/about", controllers.About).Name("about")
```

Then add the component under `resources/js/pages`, in the starter's framework.

### React

```tsx
// resources/js/pages/About.tsx
import { Head } from "@inertiajs/react";

export default function About({ version }: { version: string }) {
  return (
    <>
      <Head title="About" />
      <h1 className="text-2xl font-semibold">About (v{version})</h1>
    </>
  );
}
```

### Vue

```vue
<!-- resources/js/pages/About.vue -->
<script setup lang="ts">
import { Head } from "@inertiajs/vue3";

defineProps<{ version: string }>();
</script>

<template>
  <Head title="About" />
  <h1 class="text-2xl font-semibold">About (v{{ version }})</h1>
</template>
```

### Svelte

```svelte
<!-- resources/js/pages/About.svelte -->
<script lang="ts">
  let { version }: { version: string } = $props();
</script>

<svelte:head>
  <title>About</title>
</svelte:head>

<h1 class="text-2xl font-semibold">About (v{version})</h1>
```

Props can be lazy, optional, deferred or merged (`inertia.Optional`, `inertia.Defer`, `inertia.Merge`), and `inertia.Share` adds per-request shared props; see [frontend.md](../frontend.md#inertia-v3-beta). `vukem codegen:routes` writes typed route helpers to `resources/js/routes.ts` so pages can call `route("posts.show", { post: 7 })`.

## Add a model and a migration

```sh
vukem make:model Comment -m -f -c
vukem migrate
```

The model, migration and factory are the same as in the [HTMX guide](htmx.md#add-a-model-and-a-migration). The generated controller is plain Go: return `inertia.Render(c, "Comments/Index", inertia.Props{"comments": comments})` from its actions, add the pages under `resources/js/pages/Comments/` and the prop shape to `types.ts`. Validation errors are flashed and redirected back with `303`, and arrive in the page as `props.errors`.

## Authentication

```sh
vukem install:auth                       # Auth/* pages (login, register, reset, verify) and Dashboard
vukem install:auth --2fa                 # also TOTP two-factor authentication
vukem install:auth --social github,google
vukem migrate
```

On an Inertia app `install:auth` writes `Auth/*` and `Dashboard` pages in the project's framework instead of HTMX views, shares `auth.user` with every page, clears the Inertia history on logout and throttles logins by the email of JSON submissions. `--2fa` and `--social` behave as in the [HTMX guide](htmx.md#authentication); details in [authentication.md](../authentication.md#installauth-scaffold). `vukem install:billing` adds Inertia pages for Stripe subscriptions ([billing.md](../billing.md)).

## Tests

```sh
go test ./...
npm run typecheck
```

Go tests assert on the Inertia response, both for first visits (HTML) and Inertia visits (JSON):

```go
app := vukemtest.New(t, bootstrap.New)
c := app.Client(t).Inertia("")

c.PostJSON("/posts", map[string]any{"title": "Hello", "body": "First."}).AssertRedirect("/posts/1")
c.Get("/posts").AssertOK().AssertInertia("Posts/Index").HasProp("posts", 1)
c.Get("/posts/1").AssertInertia("Posts/Show").PropEquals("post.title", "Hello")
```

See [testing.md](../testing.md) and the testing row of [frontend.md](../frontend.md#inertia-v3-beta).

## Build and deploy

```sh
vukem build        # vite build into resources/public/build, then go build into bin/app
```

The asset version sent to the Inertia client is the hash of the Vite manifest, so a deploy with new assets makes open pages reload. Deploy `bin/app` as in the [HTMX guide](htmx.md#build-and-deploy).

Server-side rendering is off by default. To enable it, set `inertia.Config{SSR: &inertia.SSRConfig{Bundle: "bootstrap/ssr/ssr.js"}}`, build the SSR bundle, and run `vukem ssr:start` beside the app (it restarts the Node server when it crashes; `vukem ssr:check` probes `/health`). Any SSR failure falls back to client-side rendering.
