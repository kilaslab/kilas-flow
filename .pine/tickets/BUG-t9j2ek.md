---
id: BUG-t9j2ek
title: Inbound webhooks default to no authentication, and legacy empty-route bindings match by a non-unique path
status: doing
priority: high
labels:
    - security
    - api
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T07:42:30Z"
---

## Problem

Two independent issues on the public webhook surface: a deployment cannot require inbound
authentication, and a legacy binding can be reached by a label that is not unique across
tenants.

## Evidence

- `internal/webhook/webhook.go:565-566`: `case "", "none": return 0, nil` — a binding whose
  `authentication` parameter is unset (the default) admits any caller. `basicAuth` /
  `headerAuth` / `jwtAuth` are opt-in, authored by the workflow owner, and there is no
  deployment-level switch that refuses unauthenticated deliveries.
- `internal/repository/webhooks.go:81` (`Resolve`) matches
  `method = ? AND (route = ? OR (route = '' AND path = ?))` and `:108` (`ResolveRoute`)
  matches `route = ? OR (route = '' AND path = ?)`, both **without a tenant predicate**.
- The `path` column is deliberately not unique: migration
  `000011_webhook_path_label_index` dropped `uidx_webhook_bindings_label (tenant_id, path)`
  and replaced it with a non-unique index, so two tenants may hold the same label.
- Rows minted today always carry an opaque route (`mintWebhookRoute`), so the fallback is
  reachable only on a database migrated from the pre-route schema — but there is no backfill
  migration, and the downstream execution runs with the matched row's `TenantID`, i.e. the
  other tenant's credentials and datastores.
- `/webhook/*` is mounted outside the `/api/v1` auth gate (`internal/api/routes.go`), so
  this surface is the only unauthenticated write path besides `/resume/{token}`.

## Acceptance criteria

- [x] A deployment setting (e.g. `webhook.require_auth`) refuses a delivery to a binding whose
      authentication mode is `none`, with a diagnostic naming the workflow and the fix, and
      the boot log states the posture.
- [x] Empty-route bindings are backfilled with a minted route (migration, both dialects), or
      the path fallback is removed once no such row can exist — with a test proving a
      delivery cannot reach another tenant's workflow by guessing a path label.
- [x] `Resolve`/`ResolveRoute` either take a tenant or are documented as the one deliberate
      unscoped lookup with a uniqueness argument that holds (a globally unique route).
- [x] A test covers: same path label in two tenants, delivery addressed by label, exactly one
      tenant's workflow runs (its own).

## Out of scope

Per-tenant webhook domains or rate limiting.

## Implementation notes

### Stage 1 — backfill empty routes, resolve by minted route only (criteria 2, 3, 4)

Criterion 1 (`webhook.require_auth`) is stage 2 and is deliberately not ticked here.

**What changed**

- Migration `webhook_route_backfill` for both dialects, up and down
  (`migrations/{sqlite,postgres}/000014_webhook_route_backfill.*`; the number lives only in
  those four file names and everything else, tests included, finds it by name, so the
  integrator can renumber on collision). Step one records a route in `webhook_routes` for every
  node that has an empty-route binding and no route row yet (`GROUP BY tenant, workflow, node`,
  not `DISTINCT`: one node bound on two methods must share one route); step two copies the
  node's route onto its bindings. A node that already has a `webhook_routes` row (an import
  mints at import time) keeps that route. Rows that already have a route are untouched. Both
  steps select on `route = ''`, so re-running is a no-op. Route format is 32 lowercase hex,
  the format `mintWebhookRoute` produces (SQLite `lower(hex(randomblob(16)))`; PostgreSQL
  `replace(gen_random_uuid()::text, '-', '')`, 122 random bits rather than 128 because a
  UUIDv4 fixes six, which the header comment states). The down file is a documented no-op
  (`UPDATE ... SET route = route WHERE 1 = 0`): the backfill is not reversible and should not
  be, and the runner refuses an empty down.
- `internal/repository/webhooks.go`: the `route = '' OR path` fallback is gone from `Resolve`,
  from `ResolveRoute` and from `bindingFromModel` (which used to hand callers `Route = Path` for
  an empty row). `Resolve` refuses an empty route. `ResolveRoute` matches the route alone and
  returns `ErrNotFound` when its rows disagree on tenant, workflow or node (defence in depth for
  hand-edited data; it is the only lookup that can return several tenants' rows). The
  `ClaimDelivery` / `RecordDeliveryExecution` code is untouched.
- The doc comments of `Resolve` and `ResolveRoute` state the uniqueness argument: no session and
  no tenant in the URL, so the lookup is deliberately unscoped and returns the tenant it derives;
  the route comes from `mintWebhookRoute` (and, for old rows, from the backfill, which writes the
  same table); `webhook_routes` is unique on `route` and on `(tenant, workflow, node)`;
  `uidx_webhook_bindings_route` is unique on `(method, route)`; the path label is never a key;
  and what the schema does *not* enforce (bindings have no foreign key to `webhook_routes` and
  cannot be unique on the route alone, since one node binds several methods on one route) is why
  `ResolveRoute` checks its rows agree. The comment calls it "the deliberate unscoped binding
  lookup" and does not claim exclusivity: `ClaimDelivery`/`RecordDeliveryExecution` and the
  activation-time `bindingOwner` are also route-keyed and unscoped. A comment-only correction in
  `models.go` says the index is on `(method, route)`.
- Docs: `concepts/webhooks.md` (new section "The path label is never an address"),
  `operate/upgrades.md`, README "The webhook route", CHANGELOG (Changed and Security). The two
  pages BUG-vzzkg3 owns were not edited.

**Red first (unfixed code, then the same tests after the change)**

- `TestADeliveryAddressedByPathLabelNeverRunsAnyTenantsWorkflow`: the route-addressed legs pass
  (each tenant's own route queues exactly one execution of its own workflow, unreadable by the
  other tenant). After blanking tenant A's route to `''`, on the unfixed code:
  `POST /webhook/shared-label: status = 200`, `OPTIONS /webhook/shared-label: status = 204`, and
  `a delivery addressed by label queued 1 execution(s), running the first tenant's workflow
  wf_01a0be06-...; want none`. After the change it is 404 / 404 and nothing is queued, and tenant
  B's own route still runs only tenant B's workflow.
- `TestAHostedPageAddressedByPathLabelIsNotServed` (its own harness, because blanking the form's
  POST row in the same database collides on `uidx_webhook_bindings_route` with the first test's
  `(POST, '')` row): on the unfixed code `GET /webhook/label-page` returned 200 with the hosted
  page, `POST` returned 200, and one execution was queued. After the change both are 404.
- `TestResolveNeverMatchesByPathLabel` (`eachDriver`): on the unfixed code
  `Resolve(POST, label) = tenant "drv-t9j-a", error <nil>`, and so were `ResolveRoute(label)` and
  `Resolve(POST, "")` (an empty route equals the column of every unrouted row). The same failures
  on the PostgreSQL half. After the change all five lookups are `ErrNotFound` and
  `Resolve(GET, minted)` returns tenant B's row.
- `TestResolveRouteRefusesARouteBoundToTwoTenants` (`eachDriver`, both dialects): on the unfixed
  code `ResolveRoute` returned bindings of two tenants for one route.
- `TestBackfill...` (`internal/database`): before the migration existed every empty-route
  binding stayed `''` ("binding 1 (tenant-a/wf-a/n1) still has no route", ...) and the mid-schema
  helper could not find a migration named `webhook_route_backfill`.

**Tests added or changed**

- New `internal/webhook/route_label_test.go` (the harness in `webhook_test.go` gained a `db`
  field, 2 lines).
- `internal/repository/webhooks_test.go`: `TestResolveRouteReturnsEveryMethodOnOneRoute` is
  **REPLACED**, because it pinned the removed fallback (it seeded rows with `route = ''` and
  asserted they answered on their path, with `Route` reported as the path). Kept: two bindings on
  one non-empty route, GET then POST in order, `Resolve` per method, unknown route and empty
  route are `ErrNotFound`. Changed to keep the guard from firing: both rows are now the same node
  `n1` (they used `node_id = method`). Dropped: the clause "a row with no minted route answers on
  its path". Added in its place: a third row with `route = ''` whose path spells the route
  (method PUT) must not be returned by `ResolveRoute` and not by `Resolve(PUT, route)`.
  Plus new `TestResolveNeverMatchesByPathLabel` and `TestResolveRouteRefusesARouteBoundToTwoTenants`
  on both dialects (tenants `drv-t9j-*`, fixtures cleared at start and in cleanup because the
  `(method, route)` index is global, no `t.Parallel`, `-p 1`).
- New `internal/database/webhook_route_backfill_test.go`: baseline-adoption test on the frozen
  legacy schema (the literal "pre-route schema row" proof: rows with empty routes adopted by
  `Migrate` like an AutoMigrate-built database; asserts 32-hex distinct routes, existing
  `webhook_routes` route reused, already-routed row byte-identical, only the route column
  changed, the label `shared` still held by both tenants, `webhook_routes` one row per node and
  equal to each binding's route, second `Migrate` and a re-run of the migration's own SQL are
  no-ops, then the real store: `Resolve` on the migrated route finds the tenant's binding, the
  label is `ErrNotFound`, and `EnsureWebhookRoutes` returns the same route so the address
  survives a reactivation); mid-schema test (migrate to just below the backfill, seed one node
  on two methods, migrate on: one shared route, one `webhook_routes` row; needed because the
  frozen baseline still has `UNIQUE (tenant_id, path)` that a later migration drops); and a
  `kf_` table-prefix test. Each on SQLite, the first two also on PostgreSQL.
- Mutation check: replacing `GROUP BY` with `SELECT DISTINCT` in the SQLite migration makes the
  mid-schema and prefix tests fail with `duplicated key not allowed`; restored afterwards.

**Commands and outcomes**

- `go test -race ./internal/repository/... ./internal/database/... ./internal/webhook/...`: ok
  (SQLite).
- Scratch `pgvector/pgvector:pg17` container `kf-pg-bug-t9j2ek` (readiness confirmed by two
  "ready to accept connections" lines), `KILASFLOW_TEST_POSTGRES_DSN` set:
  `go test -race -count=1 -p 1 -v -run 'Backfill|Rolling|Resolve|PathLabel|TestEveryMigrationShips'
  ./internal/database/... ./internal/repository/...` passed with these EXECUTED, not skipped:
  `TestBackfillGivesEveryEmptyRouteBindingAMintedRouteOnPostgres`,
  `TestBackfillSharesOneRouteAcrossANodesMethodsOnPostgres`,
  `TestRollingBackEveryPostgresMigrationLeavesNoKilasFlowTables`,
  `TestResolveNeverMatchesByPathLabel/postgres`,
  `TestResolveRouteRefusesARouteBoundToTwoTenants/postgres`. The two eachDriver tests were also
  run red on PostgreSQL against the original `webhooks.go` and failed the same way as on SQLite.
  Then the full `go test -race -count=1 -p 1 ./internal/database/... ./internal/repository/...`
  against the same container: ok. Container removed afterwards.
- `go test -race ./internal/api/... ./nodes/... ./internal/nodepack/... ./internal/interop/...
  ./cmd/... ./packs/...`: ok. Nothing else depended on the fallback.
- `gofmt -l internal cmd nodes migrations packs pkg scripts`: empty. `go vet ./...`,
  `go build ./...`, `go test ./internal/guardrails/...`, `make coordinates-check`: clean.
  `(cd docs && pnpm install --frozen-lockfile && pnpm build)`: all internal links valid.
- Not applicable, nothing changed there: `make generate-api*`, `generate-types`, SDK checks (no
  OpenAPI operation, header, schema or SDK surface changed; `/webhook` is not in the OpenAPI
  document), `web/` (untouched), config reference (no config key in this stage).

**Address change for backfilled rows.** A row that answered on `/webhook/<label>` now answers on
`/webhook/<route>` (read it with `GET /workflows/{id}/webhooks`). Triggers that register their
own address with the sender at activation (Telegram's `setWebhook`, whose secret is derived from
the route in `nodes/telegram_lifecycle.go`, and WAHA via `LifecycleContext.PublicURL`) need to be
activated again. Rows that already have a route keep the public URL exactly as it was.

**Caller audit.** Only `internal/webhook/webhook.go` calls `Resolve`/`ResolveRoute`:
`hostedPage` (`ResolveRoute`), `resolveBinding` (`Resolve` twice: whole path, then first
segment), `servePreflight` (`ResolveRoute`). No fake `WebhookRepository` exists in the tree.

**How reachable the empty-route state is.** Hard to reach in the field. On SQLite
`ALTER TABLE ... ADD COLUMN ... NOT NULL` without a default succeeds on an empty table and fails
on a populated one (checked with sqlite3 3.51.0: `Cannot add a NOT NULL column with default value
NULL`), and CHANGELOG shows nothing has ever been released. So this closes a real code-level hole
for a database an older build populated; it is hardening, not an incident fix.

**Criteria wording.** Criterion 3 is met by documentation and an added guard, not by taking a
tenant: the URL carries none and its format must not change. The uniqueness argument that holds
is `webhook_routes` plus `(method, route)` plus the write path, not the bindings table alone,
and the comment says so. Criterion 4 is met in the combined sense the orchestrator brief chose:
a delivery addressed by the label runs no tenant's workflow (404, nothing queued), while the
deliveries addressed by each tenant's own route run exactly that tenant's own workflow.

**Dependencies and licence.** No dependency added. No n8n code, types or bytes.

### Stage 2 — webhook.require_auth (criterion 1)

**What changed**

- `internal/config/config.go`: `Webhook.RequireAuth bool` (`koanf:"require_auth"`, env
  `KILASFLOW_WEBHOOK_REQUIRE_AUTH`, default `false` in `Default()`), with the field doc the
  generator turns into `config.example.yaml` and the configuration reference (both
  regenerated, never hand-edited).
- `internal/webhook/require_auth.go` (new): `Handler.RequireAuthentication(bool)` (builder,
  like `WithTriggers`), `Handler.LogPosture()` (one Info line in both directions, the open
  one carrying `enable_with=KILASFLOW_WEBHOOK_REQUIRE_AUTH=true`), unexported
  `authenticates(r, binding)` and `refuseUnauthenticated(w, binding)` (the Warn log naming
  route/tenant/workflow/node, then the 403 problem body). `internal/webhook/webhook.go` gained
  one `requireAuth` field and a three-line branch in `admit`, placed after the address
  allow-list and before `handler.authenticate`, so it covers the delivery and the hosted form
  page (both go through `admit`) and leaves a CORS preflight ungated (it cannot carry a
  credential).
- The refusal is `403` (`application/problem+json`, no `WWW-Authenticate`), detail: "This
  deployment requires webhook authentication (webhook.require_auth) and workflow `<id>` does
  not authenticate this trigger. Set the trigger node's Authentication to Basic auth, Header
  auth or JWT auth and attach a credential, then activate the workflow again."
- Kind-aware definition: a binding counts as authenticated when its
  `parameters["authentication"]` is present and not `""`/`"none"` — extracted with the same
  type assertion `authenticate()` uses, so a non-string reads as no mode in both places and an
  unknown mode still falls through to `authenticate()`'s `500` rather than becoming a `403` —
  **or** when its trigger kind verifies its own senders. `TriggerKind` gained an optional
  `Verifies func(Delivery) bool` (`shape.go`) because a verifier can be conditional on the
  binding: a pack trigger declares an HMAC verifier but skips it when the node holds no secret,
  so "has a Verify" is not "authenticates its callers". `nodepack.Trigger.TriggerKind()` sets
  `Verifies` to `trigger.secretOf(delivery) != ""`. Telegram's kind sets `Verify` with no
  `Verifies`, i.e. it always authenticates. An IP allow-list alone deliberately does not count.
- `cmd/kilasflow/main.go`: `.RequireAuthentication(cfg.Webhook.RequireAuth)` appended to the
  existing handler chain, and `webhookHandler.LogPosture()` called after the
  `!role.runsAPI()` early return so a worker-only process claims no posture for a surface it
  does not serve.
- Docs: a "Requiring authentication on every trigger" section in
  `concepts/webhooks.md`; two sentences in `operate/security.md`; one sentence in the README's
  "The webhook route"; an `Added` bullet in `CHANGELOG.md` [Unreleased]. The two pages
  BUG-vzzkg3 owns were not touched.

**Red first**

- The new tests do not compile against the unfixed tree: `Default().Webhook.RequireAuth
  undefined`, `cfg.Webhook.RequireAuth undefined`, `TriggerKind has no field Verifies`,
  `Handler has no method RequireAuthentication` (config, nodepack and webhook test packages).
  Behavioural red was then shown by mutation: with `authenticates()` forced to `return true`,
  `TestRequireAuthRefusesADeliveryToATriggerWithNoAuthentication` sees `200`, the form GET/POST
  are served, and `…/plain_kind` and `…/optional_kind` see `200 … "Workflow was started"` where
  they want `403`; reverted, the package is green again.

**Tests added**

- `internal/config/webhook_require_auth_test.go`: default off, absent file off,
  `KILASFLOW_WEBHOOK_REQUIRE_AUTH=true` on, `webhook.require_auth: true` in a file on
  (subtests, so `t.Setenv` does not leak between them).
- `internal/webhook/require_auth_test.go` (package `webhook_test`, reusing the existing
  harness; the harness gained nothing new this stage): refusal of an unset and an explicit
  `none` mode with the exact body/headers and nothing queued; the default and explicitly-off
  postures unchanged; basic auth still `401` with a challenge and not `403`; a hosted form page
  and its submission both gated while a credentialed form still serves; OPTIONS still `204`;
  the three kind shapes (plain `403`, unconditional verifier `401`/`200`, conditional verifier
  `403` without a secret and admitted with one), the unsupported mode keeping its `500`, and
  the allow-list-before-the-flag ordering (address refusal, no workflow id disclosed); and the
  posture line in both states. The fakes for the no-database cases embed
  `repository.WebhookRepository` / `webhook.Runner`, because FEAT-fpqvwx changes
  `ClaimDelivery`/`RecordDeliveryExecution` in parallel.
- `internal/nodepack/trigger_require_auth_test.go`: an HMAC trigger reports `Verifies` true
  only for a non-blank secret and a trigger with no HMAC reports neither `Verify` nor
  `Verifies`.

**Commands and outcomes**

- Red: `go test ./internal/config/... ./internal/nodepack/... ./internal/webhook/...` →
  build failures above. Green: `go test -race -count=1 ./internal/webhook/...
  ./internal/nodepack/... ./internal/config/... ./cmd/kilasflow/... ./internal/guardrails/...
  ./scripts/...` → all `ok` (webhook 25.3s, nodepack 1.7s, config 3.1s, cmd 3.9s, guardrails
  2.5s, scripts 1.7s). `-v -run 'RequireAuth|PostureLine|WebhookRequireAuth|APackTriggerCounts'`
  shows every new test and subtest EXECUTED (none skipped).
- `gofmt -l internal/webhook internal/nodepack internal/config cmd/kilasflow internal/guardrails`
  → empty. `go vet ./...` → clean. `go build ./...` → clean.
- `make generate-config-reference` regenerated `config.example.yaml` and
  `docs/src/content/docs/operate/configuration-reference.md`; `make
  generate-config-reference-check` → clean; `go test ./scripts/...` → ok.
- Docs: `cd docs && pnpm install --frozen-lockfile && pnpm build` → 43 pages built, "All
  internal links are valid."
- Boot smoke through the real binary on a fresh SQLite database (`-config ''`,
  `KILASFLOW_SERVER_HOST=127.0.0.1`, a free port, `/api/v1/ready` polled), once per posture:
  `msg="inbound webhooks require authentication" require_auth=true` and
  `msg="inbound webhooks accept unauthenticated deliveries unless the trigger sets its own
  authentication" require_auth=false enable_with=KILASFLOW_WEBHOOK_REQUIRE_AUTH=true`.
- Not applicable this stage: no migration (so no PostgreSQL container and no
  `internal/database` dialect run), no OpenAPI operation/header/schema/SDK change (`/webhook`
  is not in the OpenAPI document), `web/` untouched.

**Dependencies and licence.** No dependency added, so no licence to record.

### Stage 2 fix — pin the composition-root wiring (review round 1)

The acceptance review found one medium finding: the switch and the posture line existed only as
two appended lines in `run()`, and nothing exercised them. Deleting both kept
`go build ./...` and every relevant package green, so a merge on this contention file could drop
the gate while `KILASFLOW_WEBHOOK_REQUIRE_AUTH=true` still loaded into `cfg.Webhook.RequireAuth`.

**What changed**

- `cmd/kilasflow/main.go`: the handler construction moved into one named function,
  `startWebhookHandler(role, cfg, workflows, runtime, credentialStore, eventBroker, webhookTriggers, log)`.
  It builds the handler (`Limits`, `WithTriggers`, `WithLogger`, `RequireAuthentication(cfg.Webhook.RequireAuth)`)
  and, only when `role.runsAPI()`, calls `handler.LogPosture()`. `run()` calls it once, where the
  chain used to be; the old separate `webhookHandler.LogPosture()` call after the `!role.runsAPI()`
  early return is gone (the helper keeps the API-only condition, so this supersedes the two-call-site
  placement described in Stage 2 above). Behaviour is unchanged: a worker-only process still claims
  no posture, and the only difference is that for an API process the posture line now precedes
  `runtime.Start`/the scheduler/retention — log ordering, not a contract.

**Test added (red first, mutation-proven)**

- New `cmd/kilasflow/webhook_wiring_test.go`, `TestWebhookRequireAuthSwitchIsWiredAtBoot`. Two
  subtests drive `startWebhookHandler` — the one function `run()` builds the handler through —
  each with only `require_auth` set; the fake binding resolves to one open trigger on one route,
  so a `403` can only come from the deployment switch and never from queueing.
  - `required`: `POST /webhook/wiring-route` → `403`, and the boot log carries
    `"… require authentication"`.
  - `default`: the same POST is admitted (`200`), and the boot log carries
    `"accept unauthenticated deliveries"`.
- Mutation proof: deleting `RequireAuthentication(cfg.Webhook.RequireAuth)` (and the now-dangling
  chain dot) fails `required` with `status with require_auth on = 200, want 403; the switch is not
  wired to the handler chain`. Deleting the `handler.LogPosture()` call fails both subtests on the
  log assertion (`boot log = "…a delivery was refused…", want the requiring posture stated` and
  `boot log = "", want the open posture stated`). `shasum` confirmed the file restored to
  `7fc70d9e412b54064940eb69c7cf2b664dadd663` after each mutation; the test is green again.

**Commands and outcomes**

- `gofmt -l cmd/kilasflow/main.go cmd/kilasflow/webhook_wiring_test.go` → empty. `go vet ./...` →
  clean. `go build ./...` → clean.
- `go test -race -count=1 ./cmd/kilasflow/... ./internal/webhook/... ./internal/config/...
  ./internal/nodepack/... ./internal/database/... ./internal/repository/... ./internal/guardrails/...
  ./scripts/...` → all `ok` (cmd 7.7s, webhook 58.2s, config 3.1s, nodepack 2.1s, database 27.7s,
  repository 160.0s, guardrails 3.8s, scripts 2.8s).
- Not applicable: no config key added or changed (`config.example.yaml` and the configuration
  reference are untouched), no migration or repository behaviour changed (no PostgreSQL run needed),
  no OpenAPI/SDK surface, `web/` untouched, no dependency added.

**Dependencies and licence.** No dependency added.
