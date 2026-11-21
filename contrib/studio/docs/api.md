# Studio JSON API

> Experimental. This API belongs to Studio's own interface (`/ui/`) and may change
> in any release. It is not a public contract: do not build on it.

The API is versioned as `/api/v1`. It runs inside Studio, behind the same
guards as every other route: a loopback peer and `Host`, no proxy header,
cross-origin protection and the session cookie (`vukem_studio`) that the
one-time start token is traded for. Without the cookie every route answers
`401`; from a non-loopback peer, `Host` or with a proxy header, `403`.

## Rules

- JSON only. A request that is not a `GET` must send `Content-Type: application/json`, or it
  gets `415`.
- A request body is at most 1 MiB (`413` above it), has no unknown field and holds one JSON value
  (`422` otherwise).
- Names of tables, columns and sort keys come from the request but are checked against the
  introspected catalog before any SQL is built. An unknown name is `404` or `422` and no SQL runs.
- Every response has `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
  `Cross-Origin-Resource-Policy: same-origin` and the strict `Content-Security-Policy` of Studio.
  No CORS header is ever set, so a browser page from another origin cannot read a response.
- Errors have one shape. The message is short and never carries what the database said:

  ```json
  { "error": { "code": "invalid_input", "message": "unknown sort column" } }
  ```

  | Status | `code` | Meaning |
  | --- | --- | --- |
  | 401 | (HTML body) | No valid session cookie |
  | 403 | `write_disabled` | `write: true` asked of a read-only Studio |
  | 404 | `not_found` | Unknown route, source, table, collection, document or key |
  | 413 | `body_too_large` | Body over 1 MiB |
  | 415 | `unsupported_media_type` | Not `application/json` |
  | 409 | `conflict` | A guarded update found the row changed; `current` has its values |
  | 422 | `invalid_input` | The request is refused; the message says why |
  | 500 | `query_failed`, `internal` | The database or Studio failed; Studio's log has its class, never the database's text |
  | 504 | `timeout` | A console statement ran past its 30 seconds and was stopped |
  | 499 | `canceled` | The client closed the connection while the statement ran |

## Bootstrap

`GET /api/v1/bootstrap` returns the mode and the sources:

```json
{
  "version": "v0.1.1",
  "write": false,
  "refreshSeconds": 2,
  "sessionKey": "9f2c41d07a3b6e58",
  "sources": [{ "name": "db", "kind": "sql", "label": "sqlite  storage/app.db" }]
}
```

`kind` is `sql`, `mongo` or `redis`. `refreshSeconds` is `0` when live refresh is off. `sessionKey` names this
Studio run for the interface's browser storage: it is a one-way hash of the session id (it cannot be traded for
access) and changes at every start, so history kept under it does not outlive the run.

## SQL sources

| Route | Returns |
| --- | --- |
| `GET /sources/{source}/tables` | `{ tables: [{ name, rowEstimate, primaryKey }] }`; `rowEstimate` is `-1` when unknown |
| `GET /sources/{source}/schema` | `{ tables: [{ name, columns, primaryKey, foreignKeys, indexes }], canShowCreate }` from the database catalog alone: `indexes` is `[{ name, columns, unique, primary, expression? }]` (the primary key first, columns in index order, `name` empty for the index a database makes for a `UNIQUE` column), `foreignKeys` is `[{ columns, refTable, refColumns }]`; `canShowCreate` is true on SQLite and MySQL |
| `GET /sources/{source}/tables/{table}/create` | `{ statement }`, the `CREATE` statement the database reports through a read-only query (SQLite `sqlite_master`, MySQL `SHOW CREATE TABLE`); `404` for an unknown table and where the database offers none |
| `GET /sources/{source}/tables/{table}/export` | the rows of the view as a download: JSON Lines, or CSV with `format=csv` (`format=json` is JSON Lines), for the same `filter`, `sort` and `desc` parameters; at most 100,000 rows, and the trailer `X-Studio-Truncated` says whether it was cut; a CSV cell that starts with `=`, `+`, `-` or `@` gets a leading quote |
| `GET /sources/{source}/tables/{table}` | `{ name, columns: [{ name, type, nullable, default? }], primaryKey, foreignKeys }` |
| `GET /sources/{source}/tables/{table}/rows` | `{ columns, rows, next?, hasMore }` |
| `GET /sources/{source}/tables/{table}/rows/{key}` | `{ columns, cells }`, values in full |
| `GET /sources/{source}/tables/{table}/count` | `{ count }` for the same `filter` parameters, or `{ timedOut: true }` when the bounded `COUNT(*)` (10 seconds) did not finish |
| `GET /sources/{source}/tables/{table}/references` | `{ references: [{ table, columns, refColumns }] }` |
| `POST /sources/{source}/query` | `{ columns, rows, truncated, elapsedMs, kind, affected? }` for `{ sql, write? }` |
| `POST /sources/{source}/explain` | the same shape, for `{ sql }` |

Rows query parameters: `filter=<column>:<op>:<value>` (repeatable; operators `eq`, `ne`, `lt`, `lte`,
`gt`, `gte`, `contains`, `starts`, `null`, `notnull`), `sort`, `desc=1`, `after` (the `next` of the
previous page), `page` (when sorted), `perPage` (1 to 200).

A row cell is `{ "t": text, "n"?: true, "x"?: true, "b"?: true, "l"?: true }`: `n` is `NULL`, `x` is
cut for display, `b` is binary and `l` says the text is a JSON number or boolean.

`{key}` is the primary key. A composite key joins its parts with a comma, each part escaped as a
query value (`1,2`). `next` uses the same packing.

The console runs one statement of at most 64 KiB. `kind` is its verb in upper case (`SELECT`, `UPDATE`; for a
`WITH` that carries a write, that write's verb). A result holds at most 1000 rows and about 8 MiB of cell text;
`truncated` says it was cut at either limit. A statement runs for at most 30 seconds (`504`, code `timeout`). It
stops when the client closes the connection (the request's context is cancelled, the statement is stopped in the
database, and the answer, which nobody reads, is `499`, code `canceled`). Studio's log holds the size of a
statement and the class of a failure, never the text or a value. It is read-only (`SELECT`, `WITH`, `SHOW`,
`EXPLAIN`, `DESCRIBE`, `TABLE`, `VALUES`) unless Studio runs with `--write` and the body has
`"write": true`.

Read-only is enforced here, whatever the text looks like: a second statement, a comment that hides one, a `WITH`
that carries an `INSERT`, `UPDATE` or `DELETE`, `SELECT ... INTO`, `EXPLAIN ANALYZE` of a write (a plain `EXPLAIN` only shows the
plan), `ATTACH`, `VACUUM`, and a `PRAGMA` that sets a value are all refused with `422` before they reach the database.

## MongoDB sources

| Route | Returns |
| --- | --- |
| `GET /sources/{source}/collections` | `{ collections: [{ name, estimate }] }` |
| `GET /sources/{source}/collections/{collection}/documents` | `{ docs: [{ id, json }], next?, hasMore }` |
| `GET /sources/{source}/collections/{collection}/documents/{id}` | `{ id, json }` |
| `GET /sources/{source}/collections/{collection}/export` | the documents of the view as JSON Lines (`format=json`), for the same `filter` and `desc` parameters, with the same cap and trailer as the table export |
| `POST /sources/{source}/find` | the documents shape, for `{ collection, filter }` (Extended JSON) |
| `POST /sources/{source}/aggregate` | the documents shape, for `{ collection, pipeline }` |

The documents route takes the same `filter`, `sort`, `desc`, `after`, `page` and `perPage` parameters.
`id` is the canonical Extended JSON of `_id`.

## Redis sources

| Route | Returns |
| --- | --- |
| `GET /sources/{source}/keys` | `{ keys: [{ key, type, ttlSeconds }], cursor? }` for `match`, `cursor`, `count` |
| `GET /sources/{source}/keys/{key...}` | `{ key, type, ttlSeconds, size, fields, more, binary? }` for `page`; `fields` is pairs (field and value, index and value, member and score, id and body) and `binary` is true for a string that is not valid UTF-8, whose value then shows each byte that is not printable ASCII as `\xNN` |
| `POST /sources/{source}/command` | `{ result }` for `{ line }`, an allowed command |

Keys are listed with `SCAN`, never `KEYS`. `cursor` is a string, absent when the scan is complete.
`ttlSeconds` is `-1` when the key does not expire.

## Write routes

They exist only when Studio runs with `--write`. Without it they are not registered and answer `404`.
Every write is audited without values. A delete needs `confirm` to equal what a person would type:
the primary key (composite parts joined by a comma), the `_id` (its hex for an ObjectID) or the key name.
A missing or different `confirm` is `422` and nothing changes.

| Route | Body |
| --- | --- |
| `POST /sources/{source}/tables/{table}/rows` | `{ fields: { <column>: { text, null } } }` |
| `PATCH /sources/{source}/tables/{table}/rows/{key}` | `{ fields, expect? }`: `expect` has the same shape as `fields` and lists values the row must still hold |
| `DELETE /sources/{source}/tables/{table}/rows/{key}` | `{ confirm }` |
| `POST /sources/{source}/collections/{collection}/documents` | `{ doc }`, Extended JSON |
| `PUT /sources/{source}/collections/{collection}/documents/{id}` | `{ doc }` |
| `DELETE /sources/{source}/collections/{collection}/documents/{id}` | `{ confirm }` |
| `PATCH /sources/{source}/keys/{key...}` | `{ action: "set" \| "hset" \| "ttl", value?, field?, seconds? }` |
| `DELETE /sources/{source}/keys/{key...}` | `{ confirm }` |

A `PATCH` whose `expect` differs from the stored row (compared as the grid shows the value, `NULL` included) changes nothing and answers `409` with `code: "conflict"` and `current`, the values of the guarded columns now. Tables without a primary key refuse writes. The console routes (`query`, `command`, `aggregate`) also
write when `--write` is on and the statement or command is a write.
