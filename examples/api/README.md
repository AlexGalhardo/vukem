# Vukem example: JSON API

A JSON API with access tokens, built in the [JSON API tutorial](../../docs/tutorials/json-api.md) and run by CI.

It starts from `vukem new api --api` (tokens, `/api/v1`, CORS and rate limiting from `install:api`, no views or sessions), then adds:

- A `Task` model owned by the token's user (`vukem make:model Task -m -f`, `vukem make:resource Task`).
- Typed routes (`api.Register`) for `/api/v1/tasks`: `?status=`, `?q=`, `?sort=`, `?page=` and `?per_page=`, validated from `requests.ListTasks`.
- 60 requests per minute per token on `/api/v1/tasks`.
- The OpenAPI 3.1 document at `/openapi.json` (development) and `vukem openapi:export`.
- A Go client generated with `vukem codegen:client -lang go` in `internal/apiclient`, checked for drift and exercised against the app by the tests.

```sh
cd examples/api
vukem migrate
vukem serve

GOWORK=off go test ./...
vukem openapi:export > openapi.json
vukem codegen:client -lang go     # refresh internal/apiclient after changing routes
```

## Fuzz the API

With the app running and a token from `POST /api/v1/tokens`:

```sh
VUKEM_FUZZ_TOKEN=<token> vukem api:fuzz -prefix /api/v1 -dry-run   # print the docker command and config
VUKEM_FUZZ_TOKEN=<token> vukem api:fuzz -prefix /api/v1            # needs Docker
```

See the Fuzz testing page of the docs site (rest-apis/fuzz-testing).
