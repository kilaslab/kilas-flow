---
id: FEAT-4d0bje
title: Apply a network policy and domain scoping to database targets
status: todo
priority: high
labels:
    - persistence
    - security
    - tier
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

`postgresDSN` at `internal/sqlnode/sqlnode.go:129` and `mysqlDSN` at line 150 assemble a DSN out of credential fields and return it. Neither takes the `Guard` argument `sqlitePath` takes — `dataSource` at line 112 hands the guard to the SQLite branch alone — and neither inspects the host; `Open` then calls `sql.Open` and `PingContext` against whatever address came out. A tenant who creates a `postgres` credential with `host: 127.0.0.1`, `port: 5432` and `database: kilasflow` therefore reads `credentials`, `workflows` and every execution payload in the installation, and the same credential reaches `169.254.169.254` or any neighbouring internal service. `internal/safehttp` exists to stop precisely this and governs HTTP only — nothing in `internal/sqlnode` imports it.

`Credential.AllowedDomains` is enforced in exactly one place in the product: `nodes/http.go:322-324`, where the HTTP executor builds a `credentials.Record` and calls `AllowsHost` before applying the credential. `nodes/database.go` never reads the field, although the resolver supplies it at `internal/engine/service.go:352` and `Execute` already holds it. The documentation at `internal/engine/runner.go:52-54` claims that keeping resolution behind `CredentialResolver` "lets the runtime enforce tenant ownership, type, and domain scope in one place" — tenant is enforced there, type is enforced per node, and domain scope is enforced nowhere at all for a database credential.

The gap is advertised to operators rather than hidden from them. The credentials editor renders an "Allowed hosts" input for every type, the database ones included, captioned "Comma separated. Leave empty to allow any host." (`web/src/routes/(dashboard)/credentials/+page.svelte:211-213`), and the API calls it "Hosts this credential may be sent to. Empty means unrestricted." (`internal/api/handlers/credentials.go:57`). An operator who types `db.partner.test` into a PostgreSQL credential has stored a string.

This is a different axis from V2-p6-5, which widens `sqlnode.Guard` so a credential resolving to KilasFlow's own database is refused on every driver. That answers whether one named target is forbidden; this ticket answers which addresses may be dialled at all, and where a credential may be sent. A guard that knows a single forbidden target still permits the metadata service, the host application's database and every neighbour — which is why the HTTP surface carries a policy and not a denylist.

## Acceptance criteria

- [ ] A database credential resolving to a loopback, private, link-local, shared-address-space or reserved address is refused before any statement runs, with the reason `safehttp.Policy.CheckAddress` reports, proven by tests per driver.
- [ ] The refusal is made against the resolved address at dial time rather than the credential's `host` text, so a hostname resolving to `127.0.0.1` is refused, proven by a test supplying its own resolver.
- [ ] `AllowedDomains` is enforced for `postgres` and `mysql` credentials, so a credential scoped to `db.partner.test` cannot open a connection to any other host, proven by a node-level test.
- [ ] A `sqlite` credential saved with a non-empty `AllowedDomains` is rejected at save time, because a file path has no host and a silently ignored scope is the defect this ticket exists to remove.
- [ ] One configured egress policy governs HTTP and database targets alike, so `allow_private_networks: true` permits a private database and `false` refuses it, proven by a configuration test.
- [ ] A MySQL connection failure still redacts the password after the dialler change, proven by a test exercising `sanitize` in `nodes/database.go` rather than only `internal/sqlnode`.
- [ ] Every refusal reports `sqlnode.ErrForbiddenTarget` with the address and the reason and never the DSN, proven by tests across all three drivers.
- [ ] `make smoke-postgres` is run by hand and its output recorded on this ticket: a workflow whose PostgreSQL credential names a loopback address fails with the policy's message, while the same workflow against the Compose service succeeds.

## Implementation Plan

Start with the credential scope, because it needs no driver change and closes the operator-facing lie on its own. `Execute` already holds `resolved.AllowedDomains` at `nodes/database.go:180-186`, and `Record.AllowsHost` at `internal/credentials/credentials.go:48` already implements the matching rules the HTTP node uses, wildcards included. Check the configured host before calling `sqlnode.Open`, exactly as `nodes/http.go` does, and the two surfaces stop disagreeing about what the field means.

**Put the address check in a dialler, not a pre-flight.** Recommend the dialler and reject a pre-flight-only check: `sql.Open` is lazy, the pool reconnects on its own, and a name that resolves to a public address when checked can resolve to a loopback address when dialled — the rebinding case `safehttp.NewClient` already handles at `internal/safehttp/safehttp.go:162-188` by checking every resolved IP immediately before the socket opens. A pre-flight is still worth keeping for its clearer message, but it is not the control.

Both hooks exist in the pinned versions. `github.com/jackc/pgx/v5@v5.10.0` carries `pgconn.Config.DialFunc` (`pgconn/config.go:42`), reachable by replacing `sql.Open("pgx", dsn)` with `pgx.ParseConfig` plus `stdlib.OpenDB` (`stdlib/sql.go:228`). `github.com/go-sql-driver/mysql@v1.9.3` exposes `RegisterDialContext` (`driver.go:49`), a package-global map keyed by network name. SQLite dials nothing, which is why its scope check belongs at credential-save time instead.

The trap is the MySQL network name. `mysqlDSN` emits `@tcp(host:port)`, and `sanitize` at `nodes/database.go:335` finds the password to redact by searching the error text for the literal `@tcp(`. Registering the checked dialler under a fresh network name turns every MySQL connection error into a disclosure of the password the credential store has just decrypted, and nothing fails, because `TestPostgresAndMySQLFailToConnectWithoutLeakingTheirPassword` at `internal/sqlnode/sqlnode_test.go:295` exercises `sqlnode` and never `sanitize`. Keep the network named `tcp` — defensible, since the policy is a process policy and per-credential scope is checked before the dial — or change `sanitize` and its coverage in the same commit.

One correction to record while in this file: `hostPort` at `internal/sqlnode/sqlnode.go:160` silently substitutes the default port when the credential's `port` field does not parse as an integer, so `5432x` connects to 5432. A policy reading credential fields is told the wrong port; one reading the address handed to the dialler is told the truth.

Settle the configuration shape rather than leaving it to whichever ticket lands first. Recommend one policy value for both surfaces: `outbound` becomes the process's egress policy for HTTP and SQL alike, because two switches is how an install ends up with request forgery closed on one surface and open on the other, and that section name is already the single word `envKeyToPath` at `internal/config/config.go:241` requires. What would reopen it is a real deployment that must call a private HTTP service while being denied a private database — or V2-p6-5 landing its own `sqlnode` section first, in which case this ticket adopts that section and defaults it from `outbound`.

## References

- Roadmap plan, p6 section, entry V2-p6-8: `.pine/roadmap.md`.
- `internal/sqlnode/sqlnode.go` — `Open` at line 89, `dataSource` at line 112, `postgresDSN` at line 129, `mysqlDSN` at line 150 and `hostPort` at line 160, none of which take the guard or check a host.
- `internal/safehttp/safehttp.go` — `Policy.CheckAddress` at line 96, `blockedReason` at line 109, and the dial-time check inside `NewClient` at lines 162-188, the control to reuse.
- `nodes/http.go` — lines 322-324, the only place in the product where `AllowedDomains` is enforced.
- `nodes/database.go` — `Execute` at line 172, which holds `resolved.AllowedDomains` and ignores it, and `sanitize` at line 326 with its `@tcp(` match at line 335.
- `internal/credentials/credentials.go` — `Record.AllowsHost` at line 48 and the `postgres`, `mysql` and `sqlite` type definitions at lines 121-151.
- `internal/engine/runner.go` — the `CredentialResolver` documentation at lines 52-54 claiming domain scope is enforced in one place, and `Credential` at line 60.
- `web/src/routes/(dashboard)/credentials/+page.svelte` — lines 211-213, the "Allowed hosts" input rendered for every credential type including the database ones.
- `internal/config/config.go` — `OutboundHTTP` at line 78 and `envKeyToPath` at line 241, whose first-underscore cut forces a one-word section name.
- `internal/sqlnode/sqlnode_test.go` — line 295, the password-redaction test the dialler change must not quietly invalidate.
