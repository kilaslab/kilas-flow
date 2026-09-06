---
id: FEAT-4d0bje
title: Apply a network policy and domain scoping to database targets
status: done
priority: high
labels:
    - persistence
    - security
    - tier
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T08:28:30Z"
updated: "2026-09-06T03:37:32Z"
---

## Scope

`postgresDSN` at `internal/sqlnode/sqlnode.go:129` and `mysqlDSN` at line 150 assemble a DSN out of credential fields and return it. Neither takes the `Guard` argument `sqlitePath` takes — `dataSource` at line 112 hands the guard to the SQLite branch alone — and neither inspects the host; `Open` then calls `sql.Open` and `PingContext` against whatever address came out. A tenant who creates a `postgres` credential with `host: 127.0.0.1`, `port: 5432` and `database: kilasflow` therefore reads `credentials`, `workflows` and every execution payload in the installation, and the same credential reaches `169.254.169.254` or any neighbouring internal service. `internal/safehttp` exists to stop precisely this and governs HTTP only — nothing in `internal/sqlnode` imports it.

`Credential.AllowedDomains` is enforced in exactly one place in the product: `nodes/http.go:322-324`, where the HTTP executor builds a `credentials.Record` and calls `AllowsHost` before applying the credential. `nodes/database.go` never reads the field, although the resolver supplies it at `internal/engine/service.go:352` and `Execute` already holds it. The documentation at `internal/engine/runner.go:52-54` claims that keeping resolution behind `CredentialResolver` "lets the runtime enforce tenant ownership, type, and domain scope in one place" — tenant is enforced there, type is enforced per node, and domain scope is enforced nowhere at all for a database credential.

The gap is advertised to operators rather than hidden from them. The credentials editor renders an "Allowed hosts" input for every type, the database ones included, captioned "Comma separated. Leave empty to allow any host." (`web/src/routes/(dashboard)/credentials/+page.svelte:211-213`), and the API calls it "Hosts this credential may be sent to. Empty means unrestricted." (`internal/api/handlers/credentials.go:57`). An operator who types `db.partner.test` into a PostgreSQL credential has stored a string.

This is a different axis from V2-p6-5, which widens `sqlnode.Guard` so a credential resolving to KilasFlow's own database is refused on every driver. That answers whether one named target is forbidden; this ticket answers which addresses may be dialled at all, and where a credential may be sent. A guard that knows a single forbidden target still permits the metadata service, the host application's database and every neighbour — which is why the HTTP surface carries a policy and not a denylist.

## Acceptance criteria

- [x] A database credential resolving to a loopback, private, link-local, shared-address-space or reserved address is refused before any statement runs, with the reason `safehttp.Policy.CheckAddress` reports, proven by tests per driver.
- [x] The refusal is made against the resolved address at dial time rather than the credential's `host` text, so a hostname resolving to `127.0.0.1` is refused, proven by a test supplying its own resolver.
- [x] `AllowedDomains` is enforced for `postgres` and `mysql` credentials, so a credential scoped to `db.partner.test` cannot open a connection to any other host, proven by a node-level test.
- [x] A `sqlite` credential saved with a non-empty `AllowedDomains` is rejected at save time, because a file path has no host and a silently ignored scope is the defect this ticket exists to remove.
- [x] One configured egress policy governs HTTP and database targets alike, so `allow_private_networks: true` permits a private database and `false` refuses it, proven by a configuration test.
- [x] A MySQL connection failure still redacts the password after the dialler change, proven by a test exercising `sanitize` in `nodes/database.go` rather than only `internal/sqlnode`.
- [x] Every refusal reports `sqlnode.ErrForbiddenTarget` with the address and the reason and never the DSN, proven by tests across all three drivers.
- [x] `make smoke-postgres` is run by hand and its output recorded on this ticket: a workflow whose PostgreSQL credential names a loopback address fails with the policy's message, while the same workflow against the Compose service succeeds.

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

## Progress (PersistenceTier, 2026-09-06)

Status: doing. Mechanism implemented within Persistence ownership
(`internal/sqlnode`, no config-shape change); caller wiring below is for the
nodes/main owners.

Done in `internal/sqlnode/sqlnode.go`:
- `Guard` gains `Policy safehttp.Policy` (process egress, zero value denies
  private), `AllowedDomains []string` (per-credential scope, empty =
  unrestricted) and `LookupIPAddr` (resolver seam; nil = system).
- `dataSource`/`hostPort` removed; `openDatabase` dispatches to
  `openPostgres`/`openMySQL`/sqlite. Both network drivers run `checkTarget`
  pre-flight (credential scope via `credentials.Record.AllowsHost`, process
  `allowed_hosts` via `Policy.CheckURL` with identical semantics, then every
  resolved IP via `CheckEndpointAddress`) and dial through a guarded dialler.
- Dial-time is the control: pgx via `ParseConfig` + `LookupFunc` (guard
  resolver — pgconn resolves before dialling, found by test) + `DialFunc`
  checking the exact address; mysql via `ParseDSN` + per-connection
  `Config.DialFunc` + `NewConnector` + `sql.OpenDB` — no global
  `RegisterDialContext`, DSN keeps `@tcp(host:port)` so `Sanitize` still
  redacts. Rebinding proven by test (public at check, loopback at dial).
- `hostPort` correction applied: `splitHostPort` refuses an unparsable or
  out-of-range port instead of substituting the default.
- sqlite + non-empty `AllowedDomains` refused in `sqlitePath` (a file has no
  host). Save-time rejection belongs to the credential endpoint (not owned).
- Every refusal is `ErrForbiddenTarget` with host/address + reason, never
  the DSN.

Tests (`internal/sqlnode/policy_test.go`, all passing): loopback refused per
driver; metadata IP refused (link-local); resolved-address via own resolver;
rebind at dial time; `AllowedPrivateEndpoints` admits one loopback dial;
`allow_private_networks: true` dials (and still redacts the password —
restores the driver-echo coverage default-deny bypasses); process
`allowed_hosts` scopes targets; credential `AllowedDomains` (+ wildcard)
scopes targets; sqlite scope refused; bad port refused.

NOT done here — needs owners (do not expand, per batch non-goals):
- `nodes/database.go` (+ `postgres_v2.go`): pass `resolved.AllowedDomains`
  into `Guard` before `sqlnode.Open`; node-level AllowedDomains test. Note
  `TestDatabaseNodeReportsAFailingStatementWithoutLeakingTheCredential`
  still passes (127.0.0.1:1 now refused pre-dial; assertions hold).
- `cmd/kilasflow/main.go` `databaseGuard`: fill `Guard.Policy` from
  `outboundPolicy(cfg.Outbound)` (one policy for HTTP + SQL) and thread to
  the credential test endpoint; sqlite save-time scope rejection in
  `internal/api`/credentials validation.
- `nodes/database.go` `sanitize` coverage for the MySQL dialler change.
- `make smoke-postgres` by hand: not run — no local PG/compose here, and
  reaching loopback services would mean punching the very policy holes this
  ticket closes. Manual step: workflow with loopback PG credential must fail
  with the policy message; Compose-service workflow must succeed.

## Progress (PolicyWiring, 2026-09-06)

Caller wiring done. No mechanism or policy-semantics changes; `internal/sqlnode`
untouched. `go test ./nodes/ ./internal/api/... ./cmd/...` green; gofmt clean
on all touched files.

- `nodes/database.go` `Execute`: copies `executor.guard`, fills
  `AllowedDomains` from `resolved`, and pre-flights `resolved.AllowsHost(host)`
  (same message shape as the AI/HTTP surfaces, wildcards included; sqlite has
  no host and falls through to `sqlnode`, which refuses it). Refusals wrap
  `sqlnode.ErrForbiddenTarget`.
- Same gate in `nodes/postgres_v2.go` `Execute` (before statement building) and
  the scoped guard passed to its `sqlnode.Open`.
- Chain fix found while testing: `sqlnode.Sanitize` flattens errors to a
  string, dropping `ErrForbiddenTarget` at the node boundary. Both executors
  now pass refusals through unwrapped (refusals never carry the DSN, proven by
  the mechanism's `assertForbidden`) and sanitize everything else.
- `cmd/kilasflow/main.go`: `sqlGuard.Policy = outboundPolicy(cfg.Outbound)`
  right after `databaseGuard` — one process egress policy for HTTP + SQL.
  Nothing else in `main.go` touched. The credential test endpoint already
  receives this guard via `Deps.DatabaseGuard`.
- `internal/api/handlers/credentials.go`: `Create`/`Update` reject a sqlite
  credential with a non-empty scope (422; `Update` resolves the stored type
  when the body omits it, since the type is immutable). `probe` narrows a copy
  of the guard to `record.AllowedDomains`, so a test verdict can no longer
  report reachable for a target a node refuses.
- Tests: `TestDatabaseExecutorsEnforceTheCredentialDomainScope` (v1,
  postgres+mysql, out-of-scope refused with host named + `ErrForbiddenTarget`
  + no password, in-scope falls through to the policy refusal),
  `TestV2DatabaseExecutorsEnforceTheCredentialDomainScope`,
  `TestDatabaseNodeRedactsAMySQLPasswordOnConnectionFailure` (permissive guard
  so the driver actually dials and echoes; node error carries no password and
  is not a policy refusal), `TestASQLiteCredentialCannotBeSavedWithAllowedDomains`
  (create 422, typeless update 422, postgres-with-scope 201),
  `TestOneEgressPolicyGovernsDatabaseTargets` (allow_private_networks true
  dials / false refuses with `ErrForbiddenTarget`, built exactly as `run()`
  builds it).
- Incidental: `TestOnlyOneTestOfACredentialRunsAtATime` was already broken by
  the mechanism change (default-deny refuses the loopback pre-dial, so the
  first probe never hangs and no 409 follows). Fixed by giving that test a
  guard admitting `127.0.0.1:<listener-port>` — test-only change.
- `hostPort`: verified, no fix needed — `splitHostPort` refuses unparsable
  ports (`TestAnUnparsablePortIsRefused` passes).

## Smoke (PolicyWiring, 2026-09-06) — criterion recorded

PG was reachable (docker daemon up, `postgres:17-alpine` present), so this ran
for real: PG container with `127.0.0.1:5433:5432`, stock binary from this tree
on sqlite storage, `KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS=127.0.0.1:5433`.
Two manual workflows, each manual -> postgres-v1 `SELECT 1 AS one`:

- Loopback credential (`127.0.0.1:5432`): execution **failed** with the
  policy's message —
  `node "Postgres": postgres connection failed: database target is not
  allowed: postgres host "127.0.0.1" resolved to 127.0.0.1: request target is
  not allowed: loopback address 127.0.0.1`.
- Compose-PG credential (`127.0.0.1:5433`, the admitted endpoint): execution
  **succeeded**, output `{"one": 1}`, `error: null`.

Topology note: the app ran on the host rather than inside the Compose network
(stock `make smoke-postgres` proves the image, not the policy), so the "Compose
service" leg is the published PG port admitted as the single private endpoint —
the exact `AllowedPrivateEndpoints` shape the ticket recommends over
`allow_private_networks: true`. First attempt used a nonexistent `ada` user and
proved the dial was admitted (SASL failure, no policy refusal); reran with the
`kilasflow` superuser for the success leg.

Every acceptance criterion above now has a proof cited here. Status stays
`doing` for the main agent to flip after project-wide validation.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .env.example                                       |  186 +
 .github/actions/js-toolchain/action.yml            |   49 +
 .github/workflows/ci.yml                           |  422 +++
 .github/workflows/release.yml                      |   92 +
 .gitignore                                         |    3 +
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |    6 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
 .pine/memory/live-databases.md                     |   40 +
 .pine/memory/persistence.md                        |   14 +
 .pine/memory/web-editor.md                         |   10 +
 .pine/roadmap.md                                   |  273 +-
 .pine/tickets/BUG-9s3htg.md                        |  170 +
 .pine/tickets/BUG-br7ggc.md                        |  189 +
 .pine/tickets/BUG-v6tdjr.md                        |  231 ++
 .pine/tickets/BUG-xmcm8x.md                        |  152 +
 .pine/tickets/EPIC-m42s3g.md                       |   12 +-
 .pine/tickets/FEAT-0556ck.md                       |  135 +
 .pine/tickets/FEAT-096vs9.md                       |  784 +++-
 .pine/tickets/FEAT-0f87fn.md                       |  302 +-
 .pine/tickets/FEAT-12s0e5.md                       |  539 +++
 .pine/tickets/FEAT-1500sp.md                       |  168 +-
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |  127 +
 .pine/tickets/FEAT-2f68r8.md                       |   83 +-
 .pine/tickets/FEAT-2phs15.md                       |  461 +++
 .pine/tickets/FEAT-347egc.md                       |    3 +-
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |  438 +++
 .pine/tickets/FEAT-48hreg.md                       |   10 +-
 .pine/tickets/FEAT-4d0bje.md                       |  183 +
 .pine/tickets/FEAT-53fht8.md                       |  120 +
 .pine/tickets/FEAT-55v09k.md                       |   84 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |  190 +-
 .pine/tickets/FEAT-5kfctc.md                       |  208 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   84 +-
 .pine/tickets/FEAT-5mvech.md                       |  332 ++
 .pine/tickets/FEAT-5rvtzc.md                       |   93 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   85 +-
 .pine/tickets/FEAT-5z37xh.md                       |   72 +
 .pine/tickets/FEAT-68zzqs.md                       |  438 +++
 .pine/tickets/FEAT-6vfn3s.md                       |  356 +-
 .pine/tickets/FEAT-7cg0cd.md                       |    3 +-
 .pine/tickets/FEAT-7tgasa.md                       |  252 ++
 .pine/tickets/FEAT-8qyfh1.md                       |  455 ++-
 .pine/tickets/FEAT-8r9n21.md                       |  306 +-
 .pine/tickets/FEAT-91as16.md                       |   96 +-
 .pine/tickets/FEAT-9555xz.md                       |    3 +-
 .pine/tickets/FEAT-96p7m3.md                       |  784 +++-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 +++
 .pine/tickets/FEAT-9knk67.md                       |   86 +-
 .pine/tickets/FEAT-a6yg3n.md                       |    2 +-
 .pine/tickets/FEAT-a7p1b2.md                       |  136 +
 .pine/tickets/FEAT-a94c8y.md                       |    2 +-
 .pine/tickets/FEAT-adzn0a.md                       |   76 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |  340 +-
 .pine/tickets/FEAT-bscygc.md                       |  102 +
 .pine/tickets/FEAT-c2a081.md                       |    2 +-
 .pine/tickets/FEAT-cgm1y3.md                       |  786 +++-
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |  118 +
 .pine/tickets/FEAT-czbzs6.md                       |   65 +
 .pine/tickets/FEAT-ddzk2k.md                       |   77 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-ej0468.md                       |    3 +-
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |  196 +
 .pine/tickets/FEAT-gg85se.md                       |   69 +
 .pine/tickets/FEAT-gjzgkd.md                       |    2 +-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-je4f4t.md                       |    3 +-
 .pine/tickets/FEAT-jq84xk.md                       |   67 +
 .pine/tickets/FEAT-jwhdsy.md                       |  414 ++-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |    2 +-
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-knpfqf.md                       |    2 +-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m94hhx.md                       |  265 ++
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |  458 +++
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |  197 +
 .pine/tickets/FEAT-nxxbs5.md                       |  213 ++
 .pine/tickets/FEAT-pd3p6x.md                       |   87 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |  110 +
 .pine/tickets/FEAT-q81bq4.md                       |  447 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |   76 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  344 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-r6xhnp.md                       |  808 +++-
 .pine/tickets/FEAT-rj17xj.md                       |    2 +-
 .pine/tickets/FEAT-sar60r.md                       |   90 +-
 .pine/tickets/FEAT-sbnejr.md                       |    3 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   82 +
 .pine/tickets/FEAT-sfy1tq.md                       |  139 +
 .pine/tickets/FEAT-snxxny.md                       |  409 +++
 .pine/tickets/FEAT-sp8cfm.md                       |  361 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v8k1tc.md                       |   90 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-whn5vb.md                       |  103 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   79 +
 .pine/tickets/FEAT-xx6p22.md                       |  117 +
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   71 +
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-za118x.md                       |   61 +
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |  316 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  347 +-
 Dockerfile                                         |   54 +-
 Makefile                                           |  229 +-
 README.md                                          |  276 +-
 cmd/kilasflow/main.go                              |  458 ++-
 cmd/kilasflow/main_test.go                         |   54 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   73 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  284 +-
 docker-compose.yml                                 |   41 -
 docs/.gitignore                                    |    6 +
 docs/astro.config.mjs                              |  130 +
 docs/package.json                                  |   20 +
 docs/plugins/base-links.mjs                        |   57 +
 docs/pnpm-lock.yaml                                | 3807 +++++++++++++++++++
 docs/src/components/ThemeProvider.astro            |   59 +
 docs/src/components/ThemeSelect.astro              |   79 +
 docs/src/content.config.ts                         |   11 +
 docs/src/content/docs/404.md                       |   21 +
 docs/src/content/docs/concepts/architecture.md     |  163 +
 docs/src/content/docs/concepts/credentials.md      |  223 ++
 docs/src/content/docs/concepts/execution-model.md  |  335 ++
 docs/src/content/docs/concepts/expressions.md      |  210 ++
 .../src/content/docs/concepts/items-and-lineage.md |  174 +
 docs/src/content/docs/concepts/node-registry.md    |  319 ++
 .../src/content/docs/concepts/safety-boundaries.md |  283 ++
 .../content/docs/concepts/tenancy-and-embedding.md |  305 ++
 docs/src/content/docs/concepts/webhooks.md         |  203 ++
 docs/src/content/docs/contributing.md              |   89 +
 docs/src/content/docs/guides/embedding.md          |   49 +
 docs/src/content/docs/guides/n8n-migration.md      |  674 ++++
 docs/src/content/docs/guides/node-authoring.md     |   41 +
 docs/src/content/docs/index.mdx                    |   59 +
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  101 +
 docs/src/content/docs/operate/security.md          |   88 +
 docs/src/content/docs/operate/upgrades.md          |   69 +
 docs/src/content/docs/reference/api-contract.md    |  304 ++
 docs/src/content/docs/reference/api.md             |   48 +
 .../content/docs/reference/expression-grammar.md   |  183 +
 docs/src/content/docs/reference/node-packs.md      |   36 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  271 ++
 docs/src/content/docs/start/what-kilasflow-is.md   |   84 +
 docs/src/styles/kilasflow.css                      |  138 +
 docs/tsconfig.json                                 |    5 +
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 internal/ai/agent.go                               |   34 +-
 internal/ai/ai.go                                  |   41 +-
 internal/ai/ai_test.go                             |  166 +
 internal/ai/memory.go                              |  164 +-
 internal/ai/openai.go                              |  100 +-
 internal/ai/openai_test.go                         |  156 +
 internal/api/auth_test.go                          |  668 ++++
 internal/api/credentials_test.go                   |  401 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/auth.go                      |  380 ++
 internal/api/handlers/credentials.go               |  339 ++
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  468 ++-
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  299 +-
 internal/api/middleware/auth.go                    |  173 +
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   58 +-
 internal/api/server.go                             |   59 +-
 internal/api/workflow_history_test.go              |  188 +
 internal/api/workflows_test.go                     |  142 +-
 internal/auth/auth.go                              |   73 +
 internal/auth/auth_test.go                         |  311 ++
 internal/auth/keys.go                              |  197 +
 internal/auth/session.go                           |  274 ++
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  508 ++-
 internal/config/config_test.go                     |  325 ++
 internal/credentials/builtin.go                    |  189 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  244 ++
 internal/credentials/registry.go                   |  346 ++
 internal/database/database.go                      |   71 +-
 internal/database/database_test.go                 |   91 +-
 internal/database/migrate.go                       |  648 ++++
 internal/database/migrate_test.go                  |  879 +++++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/embed.go                            |    2 +
 internal/engine/authenticate.go                    |  104 +
 internal/engine/runner.go                          |  467 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |  344 +-
 internal/engine/service_test.go                    |   41 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   41 +-
 internal/expression/doc.go                         |   91 +-
 internal/expression/expression.go                  |  350 +-
 internal/expression/expression_test.go             |  374 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/interop/n8n/corpus/BASELINE.md            |   43 +-
 internal/interop/n8n/corpus/baseline.json          |  113 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  107 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/n8n.go                        |  705 +++-
 internal/interop/n8n/n8n_test.go                   | 2063 ++++++++++-
 internal/interop/n8n/parameters.go                 | 2403 +++++++++++-
 internal/interop/n8n/sqlfidelity_test.go           |  442 +++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   93 +
 internal/loadoptions/loadoptions.go                |  412 +++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  716 +++-
 internal/node/registry_test.go                     |  816 ++++-
 internal/nodepack/nodepack.go                      |  432 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  459 +++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/auth.go                        |  330 ++
 internal/repository/auth_test.go                   |  322 ++
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  396 ++
 internal/repository/executions.go                  |  208 +-
 internal/repository/models.go                      |  253 +-
 internal/repository/models_test.go                 |   12 +-
 internal/repository/postgres_execution_test.go     |  213 ++
 internal/repository/schedules.go                   |  140 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/repository/workflow_history.go            |  446 +++
 internal/repository/workflow_history_test.go       |  613 ++++
 internal/repository/workflows.go                   |  182 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/runcode/doc.go                            |   37 +-
 internal/runcode/runcode.go                        |   96 +-
 internal/runcode/runcode_test.go                   |  184 +-
 internal/safehttp/safehttp.go                      |  139 +-
 internal/safehttp/safehttp_test.go                 |  193 +
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  155 +-
 internal/sqlbuild/dialect.go                       |  256 ++
 internal/sqlbuild/sqlbuild.go                      |  392 ++
 internal/sqlbuild/sqlbuild_test.go                 |  650 ++++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |    2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |    2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |    2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |    2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |    2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |    2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |    2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |    1 +
 .../testdata/mysql/delete_drop_cascade.sql         |    1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |    2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |    1 +
 .../testdata/mysql/delete_truncate_restart.sql     |    1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |    2 +
 .../testdata/mysql/insert_skip_conflict.sql        |    2 +
 internal/sqlbuild/testdata/mysql/select.sql        |    3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |    2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |    3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |    3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |    2 +
 internal/sqlbuild/testdata/mysql/update.sql        |    2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |    2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../testdata/postgres/delete_drop_cascade.sql      |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 .../testdata/postgres/delete_truncate_restart.sql  |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 .../testdata/postgres/insert_skip_conflict.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |    3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |    2 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |    3 +
 internal/sqlguard/admit.go                         |  245 ++
 internal/sqlguard/attack_test.go                   |  344 ++
 internal/sqlguard/dialect.go                       |  260 ++
 internal/sqlguard/doc.go                           |   53 +
 internal/sqlguard/sqlguard.go                      |  443 +++
 internal/sqlguard/sqlguard_test.go                 |  338 ++
 internal/sqlnode/export_test.go                    |   11 +
 internal/sqlnode/guard_test.go                     |  126 +
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/sqlnode.go                        |  745 +++-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/web/dist/index.html                       |   38 +-
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  261 +-
 internal/webhook/webhook_test.go                   |  227 +-
 internal/workflow/compiler.go                      |  384 +-
 internal/workflow/compiler_test.go                 |  215 ++
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/lifecycle.go                     |   51 +
 internal/workflow/typeversion.go                   |   10 +
 migrations/.gitkeep                                |    0
 migrations/embed.go                                |   27 +
 migrations/postgres/000001_baseline.down.sql       |   23 +
 migrations/postgres/000001_baseline.up.sql         |  192 +
 .../postgres/000002_workflow_history.down.sql      |   11 +
 migrations/postgres/000002_workflow_history.up.sql |   30 +
 migrations/postgres/000003_identity.down.sql       |   15 +
 migrations/postgres/000003_identity.up.sql         |   75 +
 .../postgres/000004_execution_indexes.down.sql     |    5 +
 .../postgres/000004_execution_indexes.up.sql       |   26 +
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 nodes/ai.go                                        | 1290 ++++++-
 nodes/ai_ollama_test.go                            |  413 +++
 nodes/ai_test.go                                   | 1435 +++++++-
 nodes/annotation.go                                |    3 +
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |  104 +-
 nodes/code_test.go                                 |  147 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  192 +-
 nodes/database.go                                  |  329 +-
 nodes/database_test.go                             |  751 +++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  623 +++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/loop.go                                      |  245 ++
 nodes/mysql_v2.go                                  |  199 +
 nodes/mysql_v2_test.go                             |  181 +
 nodes/postgres_v2.go                               |  633 ++++
 nodes/postgres_v2_test.go                          |  231 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/sql_options.go                               |  569 +++
 nodes/sql_options_live_test.go                     |  594 +++
 nodes/sql_options_test.go                          |  257 ++
 nodes/sqlite_attach_test.go                        |  161 +
 nodes/subworkflow.go                               |  275 ++
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/testdata/n8n_chat_model_options.json         |   38 +
 nodes/testdata/n8n_sql_options.json                |   17 +
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/wait.go                                      |  230 ++
 nodes/webhook.go                                   |  290 +-
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 +++
 packs/waha/README.md                               |   32 +
 packs/waha/REPORT-202409.md                        |  100 +
 packs/waha/REPORT-202502.md                        |  128 +
 packs/waha/manifest-202409.json                    |  124 +
 packs/waha/manifest-202502.json                    |  124 +
 packs/waha/pack-202409.json                        | 2794 ++++++++++++++
 packs/waha/pack-202502.json                        | 3844 ++++++++++++++++++++
 packs/waha/pack-trigger-202409.json                |  124 +
 packs/waha/pack-trigger-202502.json                |  130 +
 packs/waha/waha.go                                 |  107 +
 packs/waha/waha_test.go                            | 1196 ++++++
 scripts/docker-tags.sh                             |   84 +
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   43 +-
 sdk/README.md                                      |   25 +-
 sdk/package.json                                   |    2 +-
 sdk/src/generated/models.ts                        | 1608 +++++++-
 sdk/src/version.ts                                 |   15 +-
 web/src/lib/api/generated/auth/auth.ts             |  752 ++++
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   41 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
 .../generated/models/listWorkflowVersionsParams.ts |   20 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/loginInputBody.ts |   24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/principalResource.ts  |   24 +
 .../lib/api/generated/models/propertyDefinition.ts |   19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/publishVersionInputBody.ts    |   17 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 .../models/workflowPublishEventResource.ts         |   19 +
 .../models/workflowPublishEventResourceAction.ts   |   16 +
 .../models/workflowVersionListResource.ts          |   17 +
 .../models/workflowVersionSummaryResource.ts       |   23 +
 web/src/lib/api/generated/nodes/nodes.ts           |  443 ++-
 .../workflow-lifecycle/workflow-lifecycle.ts       |  317 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  113 +-
 web/src/lib/api/http.test.ts                       |   79 +-
 web/src/lib/api/http.ts                            |   23 +
 .../lib/components/dashboard/list-states.svelte    |  136 +
 web/src/lib/components/ui/table/index.ts           |   28 +
 web/src/lib/components/ui/table/table-body.svelte  |   15 +
 .../lib/components/ui/table/table-caption.svelte   |   20 +
 web/src/lib/components/ui/table/table-cell.svelte  |   15 +
 .../lib/components/ui/table/table-footer.svelte    |   20 +
 web/src/lib/components/ui/table/table-head.svelte  |   15 +
 .../lib/components/ui/table/table-header.svelte    |   20 +
 web/src/lib/components/ui/table/table-row.svelte   |   15 +
 web/src/lib/components/ui/table/table.svelte       |   17 +
 .../workflow-editor/activation-notices.svelte      |  120 +
 .../components/workflow-editor/canvas-node.svelte  |   45 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   61 +-
 .../workflow-editor/property-field.svelte          |  586 ++-
 .../workflow-editor/version-panel.svelte           |  509 +++
 .../workflow-editor/workflow-editor.svelte         |  290 +-
 web/src/lib/dashboard/cursor-page.test.ts          |   71 +
 web/src/lib/dashboard/cursor-page.ts               |   64 +
 web/src/lib/dashboard/list-state.test.ts           |   65 +
 web/src/lib/dashboard/list-state.ts                |   69 +
 web/src/lib/dashboard/request-guard.test.ts        |   55 +
 web/src/lib/dashboard/request-guard.ts             |   44 +
 web/src/lib/embed/embed-editor.svelte              |   50 +-
 web/src/lib/embed/session.svelte.ts                |   22 +-
 web/src/lib/embed/session.test.ts                  |   35 +-
 web/src/lib/workflow-editor/activation.test.ts     |  130 +
 web/src/lib/workflow-editor/activation.ts          |  108 +
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/collection.test.ts     |  131 +
 web/src/lib/workflow-editor/collection.ts          |  148 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 ++
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  222 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 .../lib/workflow-editor/version-history.test.ts    |  191 +
 web/src/lib/workflow-editor/version-history.ts     |  156 +
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |  141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  130 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   54 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  190 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   33 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   54 +-
 web/src/routes/+page.svelte                        |    8 +-
 708 files changed, 107952 insertions(+), 2709 deletions(-)
```
