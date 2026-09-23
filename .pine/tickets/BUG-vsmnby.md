---
id: BUG-vsmnby
title: 'Pack-trigger lifecycle hooks: values substituted without JSON escaping (injection), scalar-only params, no response capture'
status: done
priority: high
labels:
    - saas
    - packs
    - webhooks
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T04:57:38Z"
---

# Description

- Lifecycle templates receive only scalar parameters (`internal/webhook/request_lifecycle.go:119-130`), so multi-select events, collections and conditions cannot be registered with the remote service.
- Values are substituted without JSON escaping (`request_lifecycle.go:211-222`).
- Response data such as a subscription id or a generated secret cannot be captured for later `remove` or verification.

# Acceptance Criteria
- [x] Structured parameters are available JSON-encoded.
- [x] Substitution in a JSON context escapes values.
- [x] `set` may declare `capture: { key: jsonPath }`. The captured values persist on the binding and are usable by `check`, `remove` and the HMAC secret lookup.
- [x] Tests cover injection attempts.

# Implementation Plan

## Progress — part 1 of 2: escaping and structured parameters (2026-09-23)

Criteria 1, 2 and 4 pass. Capture (criterion 3) is part 2, and the ticket closes after it.

- **Context-aware substitution** (`internal/webhook/request_lifecycle.go`). One pass, `expand`, shared by every context:
  - **URL** (`substituteURL`): a *data* field — the `Parameter` and `ParameterJSON` families, listed in `dataFamilies` so that captured values can join them — is written with `safehttp.PathSegment` in the path and `url.QueryEscape` after a `?`. A value of `.` or `..` is refused, because escaping leaves a dot segment unchanged and a proxy resolves it. `PublicURL`, `Route` and the credential fields stay raw: they are the addresses the request is built on.
  - **JSON body** (`substituteBody`, for a template starting with `{` or `[`): a placeholder inside a string has its value JSON-escaped. A placeholder outside a string is written as it is, and must be exactly one JSON value. A placeholder right after a backslash is refused. The rendered body must pass `json.Valid`, or nothing is sent.
  - **Headers** (`substituteHeader`): a value containing CR or LF is refused before the request is built.
- **Structured parameters**: `lifecycleFields` keeps the scalar `Parameter.<key>` fields and adds `ParameterJSON.<key>` for every parameter, lists and objects included.
- **Live WAHA injection fixed**: `WebhookListLifecycle.open` now renders the session path through `substituteURL`. A session of `../../admin?key=` used to send the GET and the PUT, with the tenant's API key, to `/api/sessions/../../admin?key=`.
- **Shared helper**: `safehttp.PathSegment`, which `routing.substitutePath` now uses too. Its behaviour there is unchanged.
- **Review fix, the authority**: a data field that would stand before the URL's path begins (scheme, userinfo, host or port) is now refused, and the error names the field but not the value. `PathEscape` leaves `@` and `:` alone, so `{{ .baseUrl }}{{ .Parameter.x }}` or `https://host:{{ .Parameter.port }}/…` with `@evil.example` sent the credentialed request to `evil.example`. `WebhookListLifecycle.open` now adds a missing leading slash to the session template before rendering, so a relative session path still renders as a path.

Tests:
- `internal/webhook/request_lifecycle_test.go`:
  - `TestRequestLifecycleKeepsAJSONBodyParameterInsideItsString`;
  - `…KeepsAURLParameterInsideItsSegment`, which covers the path, the query, and the value right after the authority;
  - `…RendersStructuredParametersAsJSON`;
  - `…RefusesARequestItCannotRenderSafely`, which covers a CRLF header, `@evil.example` where the host ends, `@evil.example` in the port, a dot segment, a bare non-JSON value, an escaped placeholder and an invalid body;
  - `…ReadsASessionPathWrittenWithoutALeadingSlash`.
- `packs/waha/waha_test.go`: `TestASessionNameCannotMoveTheRegistrationToAnotherEndpoint`.
- `internal/safehttp/safehttp_test.go`: `TestPathSegmentKeepsAValueInsideOneSegment`.
- Every package that depends on `internal/routing`, `internal/webhook` or `internal/safehttp` passes: 31 packages, `go test`.

## Progress — part 2 of 2: capture from the `set` answer (2026-09-23)

Criterion 3 passes, so all four do.

- **Manifest** (`internal/webhook/request_lifecycle.go`, `internal/nodepack/trigger.go`):
  - `RequestDescriptor.Capture` (`capture: {key: "data.id"}`) keeps values from the JSON answer.
  - `RequestLifecycle.Validate`, run by `Trigger.validate`, refuses `capture` on `check` or `remove`, a key a template cannot name, and a path that is empty or has an empty segment.
  - `SuccessJSONPath` now walks the same dotted-path reader, `jsonPathValue`.
- **Flow**:
  - `LifecycleContext.State` (`LifecycleStateStore`: `Load`/`Save`/`Clear`) is one route's state. `Coordinator.WithState` supplies it, scoped by tenant and route.
  - `Create` saves what `set` answered with, all or nothing. A missing, null, empty, or object/list value fails activation, and so does an answer that is not JSON. Numbers keep their digits (`UseNumber`). A trigger that captures, with no state to keep it in, is refused before anything is sent.
  - `check` and `remove` read the values as `{{ .Captured.<key> }}`. `Captured` is in `dataFamilies`, so a captured value is escaped exactly like a `Parameter`.
  - A descriptor naming a value that was never kept is not sent: `check` then reports "not registered", and `remove` has nothing to remove.
  - A successful `remove` clears the state. A failed one keeps it.
- **Persistence**:
  - Migration `000022_webhook_route_lifecycle_state` adds a nullable `lifecycle_state` column to `webhook_routes`: `blob` in SQLite, `bytea` in Postgres.
  - `GORMWorkflowStore.WithLifecycleState(cipher)` seals the values with the credential cipher. `LifecycleState`, `SaveLifecycleState` and `ClearLifecycleState` scope by tenant and route. Nothing is kept without the key.
  - `cmd/kilasflow/main.go` reuses the credential cipher instance, and gives the coordinator the state only when the key is set.
- **HMAC**:
  - `TriggerHMAC.SecretCapture` is an alternative to `SecretParameter`. Validation refuses both at once, and refuses a `secretCapture` that the lifecycle's `set` does not capture.
  - `Resolve` opens the route's state into `WebhookBinding.Captured`, and `secretOf` reads it from there.
  - State that cannot be opened (no key, or the wrong key) fails the lookup rather than routing the delivery unverified.

Tests:
- `internal/webhook/request_lifecycle_capture_test.go`:
  - `TestRequestLifecycleKeepsWhatSetAnsweredForCheckAndRemove`;
  - `…EscapesACapturedValueLikeAParameter`, which covers `/`, `?` and `@` in the path and the query, a dot segment, and the host position;
  - `…FailsARegistrationWhoseAnswerLacksACapture`;
  - `…SendsNothingThatNeedsAValueItNeverCaptured`;
  - `…RefusesToCaptureWithNowhereToKeepIt`;
  - `…ReadsANestedSuccessPath`.
- `internal/webhook/lifecycle_test.go`: `TestCapturedValuesOutliveActivationUntilTheirRegistrationIsRemoved`, which drives the coordinator through activate, deactivate with no remove, reactivate (the check finds the kept id), deactivate with remove (state cleared), and reactivate (registers anew).
- `internal/repository/webhook_state_test.go`, run on SQLite and on Postgres:
  - `TestCapturedLifecycleStateIsSealedOnTheRouteAndOutlivesItsBindings`;
  - `TestCapturedLifecycleStateIsNeverKeptOrReadWithoutItsKey`.
- `internal/nodepack/trigger_capture_test.go`:
  - `TestAManifestCapturesOnlyFromSetAndOnlyWhatItCanName`;
  - `TestAnHMACSecretCanBeAValueTheRegistrationCaptured`.
- `internal/database/webhook_route_lifecycle_state_migration_test.go`: `TestTheRouteLifecycleStateColumnComesAndGoesWithItsMigrationOn{SQLite,Postgres}`. `workflow_actor_migration_test.go` now rolls back by migration name (`rollBackBelow`).
- `go test ./...` passes. The Postgres-gated suites for `database`, `repository`, `tenantpurge` and `datastore` pass against pgvector/pg17.
- **Review fix, round 1:**
  - A node verified by a captured secret refuses every delivery while its registration is on and nothing is captured yet. Only a node with registration off counts as "not configured". This is `webhook.LifecycleEnabled`, which the gate uses too.
  - A route whose state cannot be opened still answers 404, and the server now logs an error naming the route: `resolveBinding` stops falling through on errors other than not-found.
  - When the service accepted a registration but its answer could not be captured or kept, the error now says so, and a Warn is logged. The registration is removed again when what was captured is enough to address the `remove`.
  - `Validate` refuses a `check` or `remove` that reads a `Captured` key `set` does not capture, and a `set` that reads any.
  - Tests: `TestACapturedSecretNotYetKeptRefusesDeliveriesOnlyWhileRegistrationIsOn`, `TestADeliveryWhoseRouteStateCannotBeOpenedIsRefusedAndLogged`, `TestRequestLifecycleSaysARegistrationItCouldNotKeepWasMade`, the new validation cases, and a check that the error never quotes the answer.

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (4):
  - `6196357e` — BUG-vsmnby: a pack trigger keeps what its registration answered with, for its check, its remove and its HMAC secret
  - `fce90932` — BUG-vsmnby: a node parameter cannot stand in a lifecycle URL before its path begins
  - `fcaf7dd6` — BUG-vsmnby: a lifecycle template escapes each value for where it lands, and can send a parameter as JSON
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .pine/memory/code-node.md                          |   3 +-
 .pine/memory/licensing.md                          |   2 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 +++
 .pine/tickets/BUG-15st2k.md                        | 162 +++++++
 .pine/tickets/BUG-2eryxn.md                        |  49 ++
 .pine/tickets/BUG-2mes2k.md                        |  54 +++
 .pine/tickets/BUG-2n4rfz.md                        |  51 +++
 .pine/tickets/BUG-2z8geh.md                        |  52 +++
 .pine/tickets/BUG-3k12ky.md                        | 163 +++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 +++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 ++++++
 .pine/tickets/BUG-66fhea.md                        |  97 ++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 ++++
 .pine/tickets/BUG-6gkd12.md                        | 107 +++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 +++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 +++
 .pine/tickets/BUG-bcahaj.md                        | 100 +++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 +++
 .pine/tickets/BUG-dstsg9.md                        |  54 +++
 .pine/tickets/BUG-e7dwpk.md                        |  51 +++
 .pine/tickets/BUG-ecbq28.md                        | 111 +++++
 .pine/tickets/BUG-epy2se.md                        | 122 +++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 +++
 .pine/tickets/BUG-hmp85t.md                        |  99 +++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 +++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 +++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 +++
 .pine/tickets/BUG-namghh.md                        |  48 ++
 .pine/tickets/BUG-nbymq4.md                        |  52 +++
 .pine/tickets/BUG-ngt25j.md                        |  52 +++
 .pine/tickets/BUG-nn74ph.md                        |  51 +++
 .pine/tickets/BUG-nzy3pa.md                        | 186 ++++++++
 .pine/tickets/BUG-p334yw.md                        |  53 +++
 .pine/tickets/BUG-p3j233.md                        |  53 +++
 .pine/tickets/BUG-phv0r9.md                        |  56 +++
 .pine/tickets/BUG-ppvyzr.md                        |  58 +++
 .pine/tickets/BUG-pzkpfr.md                        |  53 +++
 .pine/tickets/BUG-q6b75c.md                        |  52 +++
 .pine/tickets/BUG-r1m83f.md                        |  52 +++
 .pine/tickets/BUG-rbask0.md                        | 140 ++++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 +++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 +++
 .pine/tickets/BUG-sgrxhh.md                        |  53 +++
 .pine/tickets/BUG-t12ffz.md                        |  57 +++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 +++
 .pine/tickets/BUG-v8ksv8.md                        |  49 ++
 .pine/tickets/BUG-vsmnby.md                        | 101 +++++
 .pine/tickets/BUG-x28fsx.md                        | 142 ++++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 +++
 .pine/tickets/BUG-xam6t8.md                        | 179 ++++++++
 .pine/tickets/BUG-y38bss.md                        |  54 +++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 +++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 +++
 .pine/tickets/EPIC-3en6xr.md                       |  88 ++++
 .pine/tickets/EPIC-62zt4j.md                       | 110 +++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 ++++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 ++++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 +++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 +++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 +++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 +++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 +++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 +++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 +++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 +++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 +++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 +++
 .pine/tickets/FEAT-5fx926.md                       |  57 +++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 +++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 +++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 +++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  40 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 +++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 +++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 +++
 .pine/tickets/FEAT-c81kp3.md                       |  59 +++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 +++
 .pine/tickets/FEAT-edzr73.md                       | 121 +++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 ++++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 +++
 .pine/tickets/FEAT-f045nj.md                       | 131 ++++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 +++
 .pine/tickets/FEAT-fqmh01.md                       |  97 ++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 +++++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 ++++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 +++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 +++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 +++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 +++
 .pine/tickets/FEAT-m7aw75.md                       |  54 +++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 ++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 ++++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 +++
 .pine/tickets/FEAT-p01rcw.md                       |  98 ++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 ++++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 +++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 +++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 +++
 .pine/tickets/FEAT-rkj8ry.md                       |  37 ++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 +++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 +++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 +++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 +++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 +++
 .pine/tickets/FEAT-tjcr13.md                       |  52 +++
 .pine/tickets/FEAT-v2nenc.md                       |  58 +++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 ++++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 +++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 +++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 +++
 .pine/tickets/FEAT-xzdn35.md                       |  56 +++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  38 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 +++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 +++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 ++
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++--------------
 cmd/kilasflow/main.go                              |  13 +
 config.example.yaml                                |   8 +-
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |   8 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 ...webhook_route_lifecycle_state_migration_test.go | 108 +++++
 internal/database/workflow_actor_migration_test.go |   7 +-
 internal/engine/approval.go                        |   2 +-
 internal/nodepack/trigger.go                       |  48 +-
 internal/nodepack/trigger_capture_test.go          | 167 +++++++
 internal/repository/models.go                      |   5 +
 internal/repository/webhook_state.go               | 108 +++++
 internal/repository/webhook_state_test.go          | 148 +++++++
 internal/repository/webhooks.go                    |  18 +-
 internal/repository/workflows.go                   |   4 +
 internal/routing/executor.go                       |   2 +-
 internal/safehttp/path.go                          |  15 +
 internal/safehttp/safehttp_test.go                 |  20 +
 internal/webhook/lifecycle.go                      |  54 +++
 internal/webhook/lifecycle_test.go                 | 114 +++++
 internal/webhook/request_lifecycle.go              | 491 +++++++++++++++++++--
 internal/webhook/request_lifecycle_capture_test.go | 293 ++++++++++++
 internal/webhook/request_lifecycle_test.go         | 275 ++++++++++++
 .../000022_webhook_route_lifecycle_state.down.sql  |   7 +
 .../000022_webhook_route_lifecycle_state.up.sql    |  20 +
 .../000022_webhook_route_lifecycle_state.down.sql  |   7 +
 .../000022_webhook_route_lifecycle_state.up.sql    |  20 +
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 packs/waha/waha_test.go                            |  55 ++-
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 +
 .../api/generated/models/aIModelStartedEvent.ts    |  24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 +
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 +
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 +
 .../workflow-editor/canvas-chat-panel.svelte       | 381 +++++++++++++---
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 ++-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 +++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 +++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 +++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 ++-
 web/src/lib/workflow-editor/chat.ts                |  78 +++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 +++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 +++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 275 files changed, 16820 insertions(+), 588 deletions(-)
```
