---
id: FEAT-snxxny
title: Test a database credential before a workflow runs
status: todo
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

`handlers.Credentials.Register` mounts six operations — `list-credential-types`, `list-credentials`, `create-credential`, `get-credential`, `update-credential`, `delete-credential` (`internal/api/handlers/credentials.go:96-121`) — and not one of them opens a connection. `internal/credentials` has no such capability either: its exported contract is `Lookup`, `List`, `Validate`, `Redacted` and `Split`, which check the shape of a payload and say nothing about whether the target answers. A `postgres` credential with a wrong host, port, `sslMode` or a revoked password is indistinguishable from a correct one until a workflow runs and a node fails.

The machinery already exists and simply has no caller outside the executor. `sqlnode.Open` builds the DSN from credential fields, applies the SQLite guard, calls `db.PingContext` and closes on failure (`internal/sqlnode/sqlnode.go:89-109`) — a connection test in every respect. n8n ships `postgresConnectionTest` and `mysqlConnectionTest`; the credential types they cover are already defined here, alongside `sqlite`, which has no n8n counterpart (`internal/credentials/credentials.go:121-151`).

Two of the pieces a caller needs are deliberately restricted. `Resolve` is the only source of decrypted fields and its comment is explicit — "Resolve returns the decrypted payload for runtime use only" (`internal/repository/credentials.go:27-28`) — so a test endpoint is the first non-runtime caller and that boundary has to be moved on purpose. Error sanitisation lives in `sanitize` (`nodes/database.go:326`), unexported in package `nodes`, so a handler cannot reuse it, while both drivers echo the DSN — password included — on a connection failure.

The exposure has to be sized before the route is added. `NewServer` mounts `RequestID`, `Recover`, `Logger` and `EmbedAuth` and nothing else (`internal/api/server.go:77-83`), so nothing authenticates this API, and SQL targets get no host validation whatsoever — `safehttp` governs HTTP only. A test endpoint therefore hands anyone who can reach the API a connect-anywhere probe with a timing oracle. Embed sessions are covered by construction: `permits` denies everything it does not enumerate (`internal/api/middleware/embed.go:147-151`).

A credential validated only by running a workflow turns the first five minutes of the product into guesswork, and in a white-label embed — where the host application creates credentials on a user's behalf — there is no workflow to run yet. This endpoint is independently valuable, independently testable, and serves all three database nodes without waiting on the operation set.

## Acceptance criteria

- [ ] A stored `postgres`, `mysql` or `sqlite` credential returns a reachable or unreachable verdict from one API call, with no workflow existing, proven by handler tests.
- [ ] A failing test returns the driver's reason with no credential material in it, proven by a test that stores a known password and asserts its absence from the response body.
- [ ] The endpoint applies the same `sqlnode.Guard` the executors receive, so a SQLite credential naming KilasFlow's own database is refused rather than probed, proven by a test.
- [ ] An unsaved edit is testable, with placeholder secrets resolving to their stored values and the response naming which fields came from storage, proven by a host-only edit test.
- [ ] An embed session is refused with 403, proven by an embed test so a later change to `permits` cannot open the route by accident.
- [ ] A target that accepts a connection and never answers yields a verdict within the configured deadline rather than holding the request open, proven by a test.
- [ ] A credential type with no connection test returns a documented "not testable" answer rather than a 500, proven by a handler test over `httpBearerAuth`.
- [ ] The generated clients carry the new operation with no drift, verified by hand with `pnpm generate:api:check` and `pnpm generate:types:check` and recorded on this ticket.

## Implementation Plan

Decide first where the test itself lives, because everything else follows. Put it in `internal/sqlnode` as an exported function over a driver, the field map and the guard, not in `internal/api/handlers`: the handler package must never learn to assemble a DSN, and the endpoint and the executor must not be able to disagree about what "reachable" means. Deciding it first also forces the sanitiser question before a handler is built around a copy of it.

Promote `sanitize` out of package `nodes` into `sqlnode` and have `DatabaseExecutor` call the promoted version. Reject copying it into the handler: the redaction has already been tuned once for the schemeless MySQL `user:password@tcp(…)` form (`nodes/database.go:334-341`), and a second copy guarantees the next tuning lands in one of them only. The coverage at `nodes/database_test.go:238` moves with it.

For the route, recommend a stored-credential test at `POST /credentials/{id}/test` and an unsaved-payload test at `POST /credential-types/{type}/test`, split because they take different inputs and different authority. Reject `GET /credentials/{id}/test`: a GET that opens an outbound connection is prefetchable and cacheable, and it is not a read of anything.

The trap is the redaction placeholder. The editor sends `••••••••` back for every stored secret (`internal/credentials/credentials.go:25`) and `Validate` accepts it as a non-empty string, so a handler passing the submitted body straight to `sqlnode.Open` authenticates with eight bullet characters and reports a password failure for a credential that is perfectly good. The mirror-image mistake is worse: falling back to the stored record whenever a field is redacted reports success for an edit never actually tested. Merge field by field as `GORMCredentialStore.Update` already does (`internal/repository/credentials.go:102-110`), and name the fields that came from storage.

Bound the endpoint before shipping it: its own short deadline in configuration rather than the server's, and one in-flight test per credential so a loop cannot turn the instance into a port scanner. Reject building a host allowlist here — that is V2-p6-5's work, it has to cover the executors as well, and half of it landing in the API package is how it ends up applied in one place only.

Answer an unreachable target with 200 and a structured negative verdict rather than an error status: the request succeeded and the target did not, while a 502 says the API failed. Reserve 422 for a payload the catalogue rejects and 404 for a missing credential, as `handler.problem` already does (`internal/api/handlers/credentials.go:208-215`).

**Where the driver mapping lives.** `DatabaseCredentialType` maps node type to credential type (`nodes/database.go:98-109`) and nothing maps credential type to `sqlnode.Driver`. Recommend adding that map in `internal/sqlnode` beside `Driver`, so the API package never imports `nodes` — which would drag `ai`, `runcode` and `safehttp` behind it (`nodes/executors.go:9-14`). This reopens if V2-p4-10 forks `kilasflow.sqlite` off the shared factory, since the credential-type list would then stop being a property of the driver alone.

## References

- Roadmap plan, p4 section, entry V2-p4-7: `.pine/roadmap.md`.
- `internal/api/handlers/credentials.go` — the six registered operations, none of which opens a connection, and `problem`'s status mapping.
- `internal/credentials/credentials.go` — the `postgres`, `mysql` and `sqlite` definitions, `Validate`, and `RedactedValue`.
- `internal/sqlnode/sqlnode.go` — `Open`, `dataSource` and `sqlitePath`: the connection test that exists already and lacks only a caller.
- `internal/repository/credentials.go` — `Resolve`, restricted to runtime use, and `Update`'s field-by-field placeholder merge.
- `nodes/database.go` — `sanitize` and `DatabaseCredentialType`.
- `internal/api/middleware/embed.go` — `permits`, whose default-deny branch already refuses the new route.
- `internal/api/server.go` — the middleware stack, which carries no authentication in front of this API.
- `.pine/tickets/FEAT-a94c8y.md` — V2-p6-5, the guard and host-allowlist work this endpoint must not pre-empt.
- `internal/interop/n8n/corpus/corpus.go` — `SyncCommand`, for reading n8n's own `postgresConnectionTest` from a reference checkout.
