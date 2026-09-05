---
id: FEAT-snxxny
title: Test a database credential before a workflow runs
status: done
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
updated: "2026-09-05T12:56:19Z"
---

## Scope

`handlers.Credentials.Register` mounts six operations — `list-credential-types`, `list-credentials`, `create-credential`, `get-credential`, `update-credential`, `delete-credential` (`internal/api/handlers/credentials.go:96-121`) — and not one of them opens a connection. `internal/credentials` has no such capability either: its exported contract is `Lookup`, `List`, `Validate`, `Redacted` and `Split`, which check the shape of a payload and say nothing about whether the target answers. A `postgres` credential with a wrong host, port, `sslMode` or a revoked password is indistinguishable from a correct one until a workflow runs and a node fails.

The machinery already exists and simply has no caller outside the executor. `sqlnode.Open` builds the DSN from credential fields, applies the SQLite guard, calls `db.PingContext` and closes on failure (`internal/sqlnode/sqlnode.go:89-109`) — a connection test in every respect. n8n ships `postgresConnectionTest` and `mysqlConnectionTest`; the credential types they cover are already defined here, alongside `sqlite`, which has no n8n counterpart (`internal/credentials/credentials.go:121-151`).

Two of the pieces a caller needs are deliberately restricted. `Resolve` is the only source of decrypted fields and its comment is explicit — "Resolve returns the decrypted payload for runtime use only" (`internal/repository/credentials.go:27-28`) — so a test endpoint is the first non-runtime caller and that boundary has to be moved on purpose. Error sanitisation lives in `sanitize` (`nodes/database.go:326`), unexported in package `nodes`, so a handler cannot reuse it, while both drivers echo the DSN — password included — on a connection failure.

The exposure has to be sized before the route is added. `NewServer` mounts `RequestID`, `Recover`, `Logger` and `EmbedAuth` and nothing else (`internal/api/server.go:77-83`), so nothing authenticates this API, and SQL targets get no host validation whatsoever — `safehttp` governs HTTP only. A test endpoint therefore hands anyone who can reach the API a connect-anywhere probe with a timing oracle. Embed sessions are covered by construction: `permits` denies everything it does not enumerate (`internal/api/middleware/embed.go:147-151`).

A credential validated only by running a workflow turns the first five minutes of the product into guesswork, and in a white-label embed — where the host application creates credentials on a user's behalf — there is no workflow to run yet. This endpoint is independently valuable, independently testable, and serves all three database nodes without waiting on the operation set.

## Acceptance criteria

- [x] A stored `postgres`, `mysql` or `sqlite` credential returns a reachable or unreachable verdict from one API call, with no workflow existing, proven by handler tests.
- [x] A failing test returns the driver's reason with no credential material in it, proven by a test that stores a known password and asserts its absence from the response body.
- [x] The endpoint applies the same `sqlnode.Guard` the executors receive, so a SQLite credential naming KilasFlow's own database is refused rather than probed, proven by a test.
- [x] An unsaved edit is testable, with placeholder secrets resolving to their stored values and the response naming which fields came from storage, proven by a host-only edit test.
- [x] An embed session is refused with 403, proven by an embed test so a later change to `permits` cannot open the route by accident.
- [x] A target that accepts a connection and never answers yields a verdict within the configured deadline rather than holding the request open, proven by a test.
- [x] A credential type with no connection test returns a documented "not testable" answer rather than a 500, proven by a handler test over `httpBearerAuth`.
- [x] The generated clients carry the new operation with no drift, verified by hand with `pnpm generate:api:check` and `pnpm generate:types:check` and recorded below.

## Outcome

### What Scope described had partly moved

`POST /credentials/{id}/test` already existed when this ticket opened — FEAT-2f68r8 shipped it with the HTTP probe, moved the `Resolve` boundary deliberately, and left `permits` default-denying it. So this is not a new endpoint; it is the half that was missing.

### The connection test

`sqlnode.Test` opens and closes. That is the whole of it, and deliberately: `Open` already builds the DSN from credential fields, applies the SQLite guard and pings, so a credential that gets through it is one a node could use. **Nothing is read and no statement is run** — a probe that could run SQL would be a query endpoint under another name.

`sqlnode.DriverForCredential` holds the credential-type-to-driver map, so the API package never assembles a DSN and never imports `nodes`, which would drag the AI runtime, the code compiler and the HTTP policy behind it.

### A latent hang the move exposed

`sanitize` moved from `nodes` into `sqlnode` as `Sanitize`, as planned, and writing its first direct test found **an infinite loop that had been there all along**. `redactURLCredentials` rewrote the message in place and looped from the start; the replacement contains no space and is still followed by the same `@`, so the next pass matched it again and produced the identical string forever.

It never fired in the node package because pgx does not echo a URL-form DSN — its error names `user=` and `database=` instead — so the scheme was never found and the loop returned on its first pass. Moving the function onto a path where an API endpoint reports a driver's message to a browser is exactly where a spinning core and a hung request would have shown up for the first time. It is now a forward walk that never rescans what it has redacted, with a table covering every DSN form the two drivers emit, including a bare `postgres://` in prose that must not eat the rest of the sentence.

The node-level test that proved the whole path still redacts stays where it is; the unit table is additional rather than a move, because the two prove different things.

### The redaction placeholder

Merged field by field, the way `Update` already merges. **A redacted field with no `credentialId` is refused**, rather than sent as eight bullet characters and blamed on the password — and rather than the mirror-image mistake of falling back to the whole stored record, which reports success for an edit nobody tested. A `credentialId` naming a credential of a different type is refused too.

The verdict carries `resolvedFromStorage`. Without it "it works" is ambiguous in the one case that matters: an edit that changed the host and left the password alone was tested against a value the user cannot see and did not send.

### Untestable is not failed

`untestable` is its own field beside `ok`. "We cannot check this" and "we checked and it is broken" are different answers, and a client drawing a red cross for both would be lying about one of them. It covers a type with no probe (`httpBearerAuth`) and a stored record whose type this server no longer registers.

An unreachable target answers **200 with a negative verdict**: the request succeeded and the target did not. 422 is kept for a payload the catalogue rejects, 404 for a missing credential, 409 for a concurrent test.

### Bounding it

Its own deadline in a one-word `credential` config section, defaulting to ten seconds — short because a person is watching this one, and a probe that takes half a minute to say "unreachable" has already been given up on. The test drives it with a listener that completes the handshake and then says nothing, which is the shape that holds a request open: nothing fails, and the driver waits for a greeting that never comes. It returns in 0.31s against a 300ms deadline.

One in-flight test per credential, 409 otherwise. Not a rate limit — sequential tests are how a credential gets fixed — but without it a few hundred concurrent requests turn one stored credential into a port scanner with the verdict as its oracle. A brand-new payload has no identity yet so it is claimed by type, which means two people in one tenant creating two PostgreSQL credentials at the same moment will make each other wait. That is the deliberate trade: the unsaved route is the one where the caller supplies the host.

**No host allowlist here.** That is V2-p6-5's work, it has to cover the executors too, and half of it landing in the API package is how it ends up applied in one place only.

### A gap the wiring had

`routes.go` handed the credential probe `safehttp.DefaultPolicy()` rather than the instance's own, so a self-hosted install that had opted into private networks still had its probe refuse them — a credential that works would test as unreachable. The policy and the guard now come through `Deps` from the composition root, so testing a credential and using it reach the same set of hosts.

### Clients

```
$ cd web && pnpm generate:api:check   # clean
$ cd sdk && pnpm generate:types:check # clean
```

Both regenerated and re-checked: `test-credential-payload` and the two new models, `resolvedFromStorage` and `untestable` added to the existing verdict as optional fields, so no client breaks. `pnpm check` 1310 files 0 errors, web vitest 113 passing, sdk vitest 23 passing.

**No editor UI consumes this yet** — FEAT-2f68r8 left it that way and this ticket does not change it. The credential form's Test button belongs to whichever ticket owns that form; the endpoint and its clients are ready for it.

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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/live-databases.md                     |   30 +
 .pine/roadmap.md                                   |  252 ++
 .pine/tickets/EPIC-m42s3g.md                       |   10 +-
 .pine/tickets/FEAT-0556ck.md                       |   66 +
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |   65 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |   71 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |   68 +
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |   68 +
 .pine/tickets/FEAT-48hreg.md                       |    6 +
 .pine/tickets/FEAT-4d0bje.md                       |   62 +
 .pine/tickets/FEAT-53fht8.md                       |   60 +
 .pine/tickets/FEAT-55v09k.md                       |   82 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |    3 +
 .pine/tickets/FEAT-5kfctc.md                       |   66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   81 +-
 .pine/tickets/FEAT-5mvech.md                       |   72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   91 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   83 +-
 .pine/tickets/FEAT-5z37xh.md                       |   72 +
 .pine/tickets/FEAT-68zzqs.md                       |   65 +
 .pine/tickets/FEAT-6vfn3s.md                       |  354 +-
 .pine/tickets/FEAT-7tgasa.md                       |   61 +
 .pine/tickets/FEAT-8r9n21.md                       |  304 +-
 .pine/tickets/FEAT-91as16.md                       |   93 +-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 +++
 .pine/tickets/FEAT-9knk67.md                       |   84 +-
 .pine/tickets/FEAT-adzn0a.md                       |   74 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-bp0ytb.md                       |  338 +-
 .pine/tickets/FEAT-bscygc.md                       |   62 +
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |   71 +
 .pine/tickets/FEAT-czbzs6.md                       |   65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    6 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-g6wrxm.md                       |   64 +
 .pine/tickets/FEAT-gg85se.md                       |   69 +
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-jq84xk.md                       |   67 +
 .pine/tickets/FEAT-jwhdsy.md                       |  411 ++-
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-kwxxd0.md                       |   64 +
 .pine/tickets/FEAT-m94hhx.md                       |   60 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |   69 +
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |   64 +
 .pine/tickets/FEAT-nxxbs5.md                       |   77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   85 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +
 .pine/tickets/FEAT-qcm5ec.md                       |   74 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  342 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-sar60r.md                       |   87 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   33 +
 .pine/tickets/FEAT-sfy1tq.md                       |   63 +
 .pine/tickets/FEAT-snxxny.md                       |  123 +
 .pine/tickets/FEAT-sp8cfm.md                       |  359 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-v8k1tc.md                       |   88 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  395 +-
 .pine/tickets/FEAT-whn5vb.md                       |  101 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   76 +
 .pine/tickets/FEAT-xx6p22.md                       |   62 +
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   71 +
 .pine/tickets/FEAT-za118x.md                       |   61 +
 .pine/tickets/FEAT-zmfsjd.md                       |   71 +
 .pine/tickets/FEAT-znm60y.md                       |  314 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +-
 Makefile                                           |   11 +
 README.md                                          |   33 +
 cmd/kilasflow/main.go                              |  123 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 config.example.yaml                                |   17 +
 internal/api/handlers/credentials.go               |  298 ++
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  262 +-
 internal/api/handlers/workflows.go                 |  113 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/routes.go                             |   26 +-
 internal/api/server.go                             |   26 +-
 internal/api/workflows_test.go                     |    4 +-
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  101 +-
 internal/config/config_test.go                     |   35 +
 internal/credentials/builtin.go                    |  153 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  242 ++
 internal/credentials/registry.go                   |  340 ++
 internal/engine/authenticate.go                    |   95 +
 internal/engine/runner.go                          |  418 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |   45 +-
 internal/engine/service_test.go                    |   33 +-
 internal/execution/records.go                      |   27 +-
 internal/expression/doc.go                         |   82 +-
 internal/expression/expression.go                  |  328 +-
 internal/expression/expression_test.go             |  309 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/interop/n8n/corpus/BASELINE.md            |   42 +-
 internal/interop/n8n/corpus/baseline.json          |  114 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   64 +-
 internal/interop/n8n/n8n.go                        |  378 +-
 internal/interop/n8n/n8n_test.go                   |  798 +++-
 internal/interop/n8n/parameters.go                 |  891 ++++-
 internal/loadoptions/loadoptions.go                |  353 ++
 internal/loadoptions/loadoptions_test.go           |  354 ++
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  661 +++-
 internal/node/registry_test.go                     |  647 +++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/property.go                      |  299 ++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/executions.go                  |    2 +
 internal/repository/models.go                      |   37 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/sqlnode/sqlnode.go                        |  305 +-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  187 +-
 internal/webhook/webhook_test.go                   |   48 +-
 internal/workflow/compiler.go                      |  275 +-
 internal/workflow/compiler_test.go                 |   97 +
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/ai.go                                        |   54 +-
 nodes/annotation.go                                |    3 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |   30 +-
 nodes/code_test.go                                 |   66 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  177 +-
 nodes/database.go                                  |  255 +-
 nodes/database_test.go                             |  534 ++-
 nodes/executors.go                                 |  597 ++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/loop.go                                      |  245 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/webhook.go                                   |   35 +-
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
 sdk/src/generated/models.ts                        |  612 +++-
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   16 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   23 +
 .../api/generated/models/loadOptionsInputBody.ts   |   20 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   14 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  342 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   48 +-
 .../workflow-editor/property-field.svelte          |  203 +-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |    4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  214 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 273 files changed, 47248 insertions(+), 1421 deletions(-)
```
