# API diff: v0.39.1 to HEAD

Generated with `go run ./internal/tools/apicheck -major v0 -head HEAD -report docs/api/apidiff-v0.39.1-to-main.md`; each change is reviewed in [review.md](review.md).

| Module | Base tag | Incompatible | Compatible |
| --- | --- | --- | --- |
| `github.com/AlexGalhardo/vukem` | `v0.39.1` | 0 | 6 |
| `github.com/AlexGalhardo/vukem/contrib/cloudflare` | none (no stable v0 tag) | - | - |
| `github.com/AlexGalhardo/vukem/contrib/engine` | `contrib/engine/v0.1.0` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/contrib/graphql` | `contrib/graphql/v0.4.3` | 0 | 3 |
| `github.com/AlexGalhardo/vukem/contrib/grpc` | `contrib/grpc/v0.4.3` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/contrib/http3` | none (no stable v0 tag) | - | - |
| `github.com/AlexGalhardo/vukem/contrib/imaging` | `contrib/imaging/v0.1.3` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/contrib/qrcode` | none (no stable v0 tag) | - | - |
| `github.com/AlexGalhardo/vukem/contrib/stripe` | `contrib/stripe/v0.5.1` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/mongo` | `drivers/mongo/v0.2.3` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/mysql` | `drivers/mysql/v0.2.1` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/otel` | `drivers/otel/v0.4.3` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/postgres` | `drivers/postgres/v0.6.1` | 0 | 2 |
| `github.com/AlexGalhardo/vukem/drivers/redis` | `drivers/redis/v0.5.0` | 1 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/s3` | `drivers/s3/v0.4.2` | 0 | 0 |
| `github.com/AlexGalhardo/vukem/drivers/websocket` | `drivers/websocket/v0.1.4` | 0 | 0 |

## github.com/AlexGalhardo/vukem

Compared with `v0.39.1`.

### github.com/AlexGalhardo/vukem/auth

Compatible:

- `Allow`: added

### github.com/AlexGalhardo/vukem/auth/totp

Compatible:

- `ErrEmpty`: added

### github.com/AlexGalhardo/vukem/mail

Compatible:

- `(*MailtrapError).Is`: added
- `(*ResendError).Is`: added

### github.com/AlexGalhardo/vukem/social

Compatible:

- `ErrNotAllowed`: added
- `Social.Allow`: added

## github.com/AlexGalhardo/vukem/contrib/engine

Compared with `contrib/engine/v0.1.0`.

No API changes.

## github.com/AlexGalhardo/vukem/contrib/graphql

Compared with `contrib/graphql/v0.4.3`.

### github.com/AlexGalhardo/vukem/contrib/graphql

Compatible:

- `DefaultMaxPersistedBytes`: added
- `MaxMemoryStoreBytes`: added

### github.com/AlexGalhardo/vukem/contrib/graphql/PersistedQueries

Compatible:

- `MaxDocumentBytes`: added

## github.com/AlexGalhardo/vukem/contrib/grpc

Compared with `contrib/grpc/v0.4.3`.

No API changes.

## github.com/AlexGalhardo/vukem/contrib/imaging

Compared with `contrib/imaging/v0.1.3`.

No API changes.

## github.com/AlexGalhardo/vukem/contrib/stripe

Compared with `contrib/stripe/v0.5.1`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/mongo

Compared with `drivers/mongo/v0.2.3`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/mysql

Compared with `drivers/mysql/v0.2.1`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/otel

Compared with `drivers/otel/v0.4.3`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/postgres

Compared with `drivers/postgres/v0.6.1`.

### github.com/AlexGalhardo/vukem/drivers/postgres

Compatible:

- `DSN`: added
- `ErrInvalidURL`: added

## github.com/AlexGalhardo/vukem/drivers/redis

Compared with `drivers/redis/v0.5.0`.

### github.com/AlexGalhardo/vukem/drivers/redis

Incompatible:

- `Queue`: old is comparable, new is not

## github.com/AlexGalhardo/vukem/drivers/s3

Compared with `drivers/s3/v0.4.2`.

No API changes.

## github.com/AlexGalhardo/vukem/drivers/websocket

Compared with `drivers/websocket/v0.1.4`.

No API changes.
