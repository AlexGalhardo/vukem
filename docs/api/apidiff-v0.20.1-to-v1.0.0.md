# API diff: v0.20.1 to v1.0.0

Generated with `go run ./internal/tools/apicheck -major v0 -head v1.0.0 -report docs/api/apidiff-v0.20.1-to-v1.0.0.md`; each change is reviewed in [review.md](review.md).

| Module | Base tag | Incompatible | Compatible |
| --- | --- | --- | --- |
| `github.com/AlexGalhardo/vukem` | `v0.20.1` | 5 | 4 |
| `github.com/AlexGalhardo/vukem/contrib/graphql` | `contrib/graphql/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/contrib/grpc` | `contrib/grpc/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/contrib/stripe` | `contrib/stripe/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/mongo` | `drivers/mongo/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/mysql` | `drivers/mysql/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/otel` | `drivers/otel/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/postgres` | `drivers/postgres/v0.3.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/redis` | `drivers/redis/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/s3` | `drivers/s3/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/websocket` | `drivers/websocket/v0.1.0` | 0 | 0 |

## github.com/AlexGalhardo/vukem

Compared with `v0.20.1`.

### github.com/AlexGalhardo/vukem/auth/hash

Incompatible:

- `DefaultArgon2`: removed

Compatible:

- `DefaultArgon2Params`: added

### github.com/AlexGalhardo/vukem/database/dbtest

Incompatible:

- `Author`: removed
- `Book`: removed
- `Meta`: removed
- `Tag`: removed

### github.com/AlexGalhardo/vukem/mail

Compatible:

- `Attachable`: added

### github.com/AlexGalhardo/vukem/orm

Compatible:

- `AfterCreator`: added
- `BeforeCreator`: added

## github.com/AlexGalhardo/vukem/contrib/graphql

Compared with `contrib/graphql/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/contrib/grpc

Compared with `contrib/grpc/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/contrib/stripe

Compared with `contrib/stripe/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/mongo

Compared with `drivers/mongo/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/mysql

Compared with `drivers/mysql/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/otel

Compared with `drivers/otel/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/postgres

Compared with `drivers/postgres/v0.3.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/redis

Compared with `drivers/redis/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/s3

Compared with `drivers/s3/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/websocket

Compared with `drivers/websocket/v0.1.0`.

No API changes.
