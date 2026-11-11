# Vukem example: JSON API

A JSON API with access tokens, built in the [JSON API tutorial](../../docs/tutorials/json-api.md) and run by CI.

It starts from `vukem new api --api` (tokens, `/api/v1`, CORS and rate limiting from `install:api`, no views or sessions), then adds:

- A `Task` model owned by the token's user (`vukem make:model Task -m -f`, `vukem make:resource Task`).
- Typed routes (`api.Register`) for `/api/v1/tasks`: `?status=`, `?q=`, `?sort=`, `?page=` and `?per_page=`, validated from `requests.ListTasks`.
- 60 requests per minute per token on `/api/v1/tasks`.
- The OpenAPI 3.1 document at `/openapi.json` (development) and `vukem openapi:export`.
- A `task_flow` load-test scenario (`app/benchmarks`, listed in `bootstrap/bench.go`): log in for a token, list the tasks, create one and list again.
- A Go client generated with `vukem codegen:client -lang go` in `internal/apiclient`, checked for drift and exercised against the app by the tests.

```sh
cd examples/api
vukem migrate
vukem serve

GOWORK=off go test ./...
vukem openapi:export > openapi.json
vukem codegen:client -lang go     # refresh internal/apiclient after changing routes
```

## Load test

`task_flow` is one API session of four requests: `POST /api/v1/tokens`, `GET /api/v1/tasks`, `POST /api/v1/tasks` and a filtered `GET /api/v1/tasks`. `bench:run` boots a copy of the app on a free loopback port with a temporary SQLite database (never the one in `DATABASE_URL`), creates the verified user `bench@example.com` in the scenario's `Setup`, applies the load and deletes the database.

The limits of the app (5 tokens per minute per address, 60 task requests per minute per token) are meant for real clients, so a load from one address answers `429` almost at once: a 2 s run gave 99.8% failures. `RATE_LIMIT_SCALE` multiplies every limit (1 to 10000, default 1); set it for the run only.

```sh
cd examples/api
vukem bench:run --list                       # the scenarios and their weights

RATE_LIMIT_SCALE=1000 vukem bench:run -d 20s     --threshold 'p95<250ms' --threshold 'error_rate<1%'      # exit code 3 if one fails

RATE_LIMIT_SCALE=1000 vukem bench:run -d 20s --save baseline.json      # record a baseline
RATE_LIMIT_SCALE=1000 vukem bench:run -d 20s --compare baseline.json   # exit code 4 on a regression
```

A short run on a development machine (10 workers, 5 s): 287 requests per second, p95 108 ms, 0% failures. The thresholds are a starting point: raise or lower them after a run on the machine that will gate. Every login hashes a password, which is most of the latency. There is no baseline in the repository: the numbers depend on the machine, so record one where you will compare (`baseline.json` is not committed). See [Performance](../../docs/performance.md#scenarios-in-process-mode-thresholds-and-baselines).

## Fuzz the API

With the app running and a token from `POST /api/v1/tokens`:

```sh
VUKEM_FUZZ_TOKEN=<token> vukem api:fuzz -prefix /api/v1 -dry-run   # print the docker command and config
VUKEM_FUZZ_TOKEN=<token> vukem api:fuzz -prefix /api/v1            # needs Docker
```

See the Fuzz testing page of the docs site (rest-apis/fuzz-testing).
