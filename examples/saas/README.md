# Vukem example: SaaS

A small SaaS with React pages over Inertia, built in the [SaaS tutorial](../../docs/tutorials/saas-inertia.md) and run by CI.

It starts from `vukem new saas --inertia react`, `vukem install:auth --2fa` and `vukem install:billing`, then replaces the starter posts with:

- A `Project` model owned by a user, with Inertia pages in `resources/js/pages/Projects`.
- `ProjectPolicy` (`app/policies`): projects are private (other users get 404) and free accounts own up to three; a valid subscription removes the limit.
- An `authz` gate wired in `routes/projects.go`.

The Go tests render the Inertia page objects, so they need neither Node nor a Vite build; Stripe is replaced by a fake server.

```sh
cd examples/saas
npm install
vukem migrate
vukem serve        # Go app and the Vite dev server

GOWORK=off go test ./...
npm run typecheck
```
