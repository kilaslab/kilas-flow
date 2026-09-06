---
id: FEAT-1c70nt
title: Open the Datastore to embedded hosts and the SDK
status: done
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-xeq6st
    - FEAT-t26rt7
    - FEAT-ddzk2k
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-06T06:23:11Z"
---

## Scope

`permits` at `internal/api/middleware/embed.go:89` decides what an embed session may reach, and its closing branch at lines 147-151 returns `false, "An embed session cannot use this endpoint."` for every path it does not enumerate. The enumerated set is `/node-types`, a `GET` of `/credentials`, `/workflows/...` for the session's own workflow, and `/executions`. Every datastore route V2-p9-7 registers is therefore a 403 for every embed session on the day it ships, with no datastore code wrong anywhere.

The second blocker is the session itself. `embed.Session` at `internal/embed/embed.go:94-103` carries one mandatory `WorkflowID string` serialised as `wid`; `Issuer.Issue` refuses a request without one — "an embed session needs a tenant and a workflow" at lines 213-215 — and `Verify` refuses a decoded token whose `WorkflowID` is empty as an "incomplete session" at lines 322-324. A session scoped to a datastore rather than a workflow cannot be minted, and could not be honoured if it were.

The third is the vocabulary. `normalizeScopes` at lines 252-274 switches over exactly `ScopeRead`, `ScopeWrite` and `ScopeRun` — `"workflow:read"`, `"workflow:write"`, `"workflow:run"` at lines 32-34 — and answers anything else with `scope %q is not supported`. There is no room in that switch for a datastore scope.

The SDK asymmetry is the fourth. `web/orval.config.ts` generates a full svelte-query client, so the SPA gains datastore calls for nothing, while `sdk/orval.config.ts` generates "Types only" into `sdk/src/generated/models.ts` because "The SDK writes its own request layer". Every call on `KilasFlowClient` in `sdk/src/server.ts` is hand-written: sixteen methods from `listWorkflows` at line 74 to `createEmbedSession` at line 159, plus a constructor and a `baseUrl` getter (the roadmap says seventeen, which counts one of those two).

The posture is what makes this the last ticket rather than an optional one. A host that cannot reach its own customers' rows with the token it already holds will reach for the privileged API key in the browser instead — the exact failure the SDK's two entry points exist to make impossible, per `sdk/src/index.ts`.

**Amendment for p10.** This ticket keeps the embed vocabulary: `DatastoreID` beside `WorkflowID`, the `permits` entry, the `datastore:read` and `datastore:write` scopes, the scope-implication rule, and the browser entry point's behaviour when handed a datastore-only session. Three things it previously implied are now owned elsewhere and must not be scoped twice. The **complete** management surface on `KilasFlowClient` — all three resource groups, the typed filter builder, the cursor iterator and the required-filter rule on row delete — belongs to V2-p10-7 (`FEAT-nc6z9r`), which depends on this ticket and completes what its fifth acceptance criterion starts. **Publishing** `@kilasflow/sdk` belongs to V2-p10-8 (`FEAT-3taswf`). And the **technical documentation** this ticket's title implies belongs to the documentation site: V2-p10-13 (`FEAT-frvez8`) for the host integration guide, V2-p10-10 (`FEAT-za118x`) for the generated API reference. What stays here is the datastore methods needed to prove the embed path works.

## Acceptance criteria

- [x] A session carrying a datastore scope and no workflow is minted and verified end to end, proven by unit tests in `internal/embed` covering `Issue`, `sign` and `Verify`.
- [x] `permits` admits the datastore routes only for a session holding the matching datastore scope and still refuses an unrecognised path by default, proven by a table-driven middleware test.
- [x] A session bound to one datastore reads no other datastore's rows, and the refusal is a 404 rather than a 403, proven by a handler test in the shape of `ownsExecution`.
- [x] A workflow-scoped session reaches no datastore route and a datastore-scoped session reaches no workflow route, proven by cases added to the existing embed middleware tests.
- [x] `KilasFlowClient` exposes typed datastore methods covered against a stubbed fetch, proven by `pnpm --dir sdk test` run by hand with its output recorded on the ticket.
- [x] `pnpm --dir sdk generate:types:check` passes against the committed `models.ts`, run by hand and recorded on the ticket, since this repository has no CI configuration of any kind.
- [x] `mountWorkflowEditor` given a datastore-only session fails with an error naming the cause rather than requesting `/embed/`, proven by a case in `sdk/test/browser.test.ts`.
- [x] A token minted before this ticket still verifies and still resolves to its original workflow scopes, proven by a fixture-token test that no format change may break.

## Implementation Plan

Settle the session's shape first, because the middleware, the mint endpoint and every SDK method sit downstream of it. **One subject field against a generic subject.** Recommend adding `DatastoreID string` serialised as `did,omitempty` beside `WorkflowID`, relaxing `Issue` and `Verify` to require exactly one subject rather than a workflow, and leaving the token version `kfe1` alone. Reject a generic `Subject{Kind, ID}`: it rewrites every existing comparison, including `session.WorkflowID != workflowID` inside `ownsExecution`, invalidates every token in flight, and buys nothing until a third subject kind exists.

The trap is `Allows` at `internal/embed/embed.go:106-117`. Its loop grants read whenever the session holds write or run, written as `if scope == ScopeRead && (held == ScopeWrite || held == ScopeRun)`. Extending that branch to a datastore write — or generalising it by string prefix — silently hands a datastore-only session the right to read the workflow, and a `workflow:write` session the right to read datastore rows. Nothing errors and every existing test passes, because both families answer the same method. Keep the implication strictly within a family and assert each cross-family pair is refused.

Add the `permits` cases above the default branch, keyed on `/datastores` and its prefix, holding to that function's own rule about what the request targets. Row-level identity belongs in the handler, as `ownsExecution` argues at `internal/api/handlers/executions.go:329-340`, because which datastore a row belongs to is knowable only by loading it. While in the file, correct the comment at line 144: it names `handlers.RequireEmbedWorkflow`, which does not exist — the function is `ownsExecution`.

Fix the mint response next. `EmbedSessions.Create` builds `EmbedURL` as `"/embed/" + session.WorkflowID` at `internal/api/handlers/embed.go:111`, and the browser SDK recovers the workflow from that string by regular expression in `workflowIdFrom` at `sdk/src/browser.ts:137-140`, throwing at line 69 when it comes back empty. A datastore session must omit `embedUrl` rather than return `/embed/`.

Hand-write the SDK methods beside the existing sixteen and in their style — a thin typed call onto a documented endpoint. Reject switching `sdk/orval.config.ts` to a full fetch client to dodge the typing: it would stand a second request layer next to `Transport`, contradicting that file's own comment, and would change every existing signature at once.

Documentation closes the ticket rather than opening it: a scope table in `sdk/README.md`, whose only table today is the two-entry-point one, the endpoint table in `README.md`, and a datastore path through `sdk/examples/host-page`. **Recommend** shipping `datastore:read` and `datastore:write` only, deferring an admin scope that could create or drop a datastore from inside an iframe, since DDL from an embedded page is the same authority `permits` already refuses for workflow import and activation. What reopens it: a host that provisions a datastore per end-customer entirely through the embedded surface, with no backend call of its own to make.

## References

- Roadmap plan, p9 section, entry V2-p9-15: `.pine/roadmap.md`.
- `internal/api/middleware/embed.go` — `permits`, the enumerated cases, the default-deny branch at 147-151, and the stale comment at line 144.
- `internal/embed/embed.go` — the `Scope` constants at 32-34, `Session` at 94-103, `Allows` at 106-117, `Issue` at 212-215, `normalizeScopes` at 252-274, and `Verify` at 322-324.
- `internal/api/handlers/embed.go` — `EmbedSessions.Create` and the `"/embed/" + session.WorkflowID` response at line 111.
- `internal/api/handlers/executions.go` — `ownsExecution` at 329-340, the handler-side identity check the datastore routes must copy.
- `sdk/src/server.ts` — `KilasFlowClient` and its sixteen hand-written methods.
- `sdk/orval.config.ts` and `web/orval.config.ts` — types-only generation against a full svelte-query client, the source of the asymmetry.
- `sdk/src/browser.ts` — `mountWorkflowEditor` at line 62 and `workflowIdFrom` at 137-140.
- `sdk/scripts/check-types.mjs` — the drift check behind `pnpm generate:types:check`, which is run by hand.
- `sdk/README.md` and `README.md` — the two documentation surfaces this ticket must leave accurate.
- `.pine/tickets/FEAT-nc6z9r.md` — V2-p10-7, which owns the complete Datastore management surface on the SDK and depends on this ticket.
- `.pine/tickets/FEAT-3taswf.md` — V2-p10-8, which owns publishing the package.
- `.pine/tickets/FEAT-frvez8.md`, `.pine/tickets/FEAT-za118x.md` — V2-p10-13 and V2-p10-10, which own the documentation.

## Work notes (EmbedDatastore, 2026-09-06)

Scope kept to the ticket's vocabulary brief: `DatastoreID` beside
`WorkflowID`, the `permits` entry, `datastore:read`/`datastore:write`, the
browser clean-fail, and the minimal client subset proving the path. No
`internal/datastore` edits, no SQL touched, no new dependencies, no web
frame change (the SPA serves the workflow editor only; a datastore token
there is refused on workflow routes by design, and the SDK refuses to
mount before any request).

Design, per box:

- Session: `DatastoreID string \`json:"did,omitempty"\`` beside
  `WorkflowID`; `Issue`/`Verify` require exactly one subject; token
  version `kfe1` untouched. `Allows` implication stays inside one family
  (`datastore:write` implies `datastore:read` only); every cross-family
  pair is asserted refused. Scope/subject mismatch is refused at mint
  (a token that reaches nothing is never issued).
- `permits`: datastore arm keyed on `/datastores` + prefix. Reads (GET
  definition/rows/row/export) need `datastore:read`; row mutations
  (POST/PUT/DELETE incl. upsert/import) need `datastore:write`. List,
  create, rename, drop, clear, and all column routes are refused outright
  (schema work, like import/activation). Identity is NOT checked in
  `permits` — the handler answers 404. Stale
  `handlers.RequireEmbedWorkflow` comment corrected to `ownsExecution`.
- Handler: `ownsDatastore` in `internal/api/handlers/datastores.go`,
  shaped like `ownsExecution`, returning the same `404 datastore not
  found` an unknown id produces. Guarded: `Get`, `ListRows`, `GetRow`,
  `InsertRow`, `UpdateRows`, `DeleteRows`, `UpsertRow`, plus `ExportRows`
  and `ImportRows` in `datastores_csv.go` (two lines, agreed with
  DatastoreCSV, their suite re-verified green).
- Mint: `POST /embed-sessions` takes exactly one of `workflowId` /
  `datastoreId`; datastore existence is checked through a `WithDatastores`
  engine (wired in `routes.go`, skipped when nil like the workflow repo).
  A datastore session answers `embedUrl: ""` plus `datastoreId`, never a
  fabricated `/embed/`.
- SDK: `EmbedScope` gains the two `datastore:*` scopes;
  `EmbedSessionRequest` takes exactly one subject (client-checked);
  `KilasFlowClient` gains `getDatastore`, `listDatastoreRows`,
  `getDatastoreRow`, `insertDatastoreRow` — thin wrappers on generated
  types, null triples sent as absent. Full surface stays with FEAT-nc6z9r.
- Browser: `mountWorkflowEditor` with a datastore-only session throws
  `mountWorkflowEditor cannot open the workflow editor with a
  datastore-scoped session for datastore <id>: mint a workflow session
  instead` before creating any iframe (no hang, no `/embed/` request).
- Docs: `docs/src/content/docs/guides/embedding.md` — credential table,
  one-subject bullet, and a datastore-session mint example. The stale
  "default-denied on `/datastores/*`" row is gone.

Proof, per box (all run by hand 2026-09-06):

- [x] Box 1 — `go test ./internal/embed/` ok:
  `TestADatastoreSessionIsMintedAndVerifiedEndToEnd`,
  `TestASessionNeedsExactlyOneSubject`,
  `TestScopesMustBelongToTheSessionSubject`,
  `TestScopeImplicationStaysInsideOneFamily`.
- [x] Box 2 — `go test ./internal/api/middleware/` ok:
  `TestPermitsAdmitsDatastoreRoutesOnlyWithTheMatchingScope` (28 cases),
  `TestPermitsKeepsTheTwoFamiliesApart` (10 cases).
- [x] Box 3 — `go test ./internal/api/` ok:
  `TestDatastoreSessionReadsNoOtherDatastore` (same-tenant 404s),
  `TestCrossTenantDatastoreIsRefusedThroughEmbed` (two servers, one
  engine, victim id named exactly, 404 on definition and rows),
  `TestDatastoreSessionReadsItsOwnRows`.
- [x] Box 4 — `TestWorkflowAndDatastoreSessionsStayApart` (live server)
  plus the middleware family cases; the xeq6st pin
  `TestEmbedSessionsAreDeniedOnDatastorePaths` still passes with its
  comment refreshed to the explicit arm.
- [x] Box 5 — stubbed-fetch coverage in `sdk/test/server.test.ts`
  (`datastore methods`, `embed sessions`) plus live-HTTP proof in
  `sdk/test/datastore-live.test.mjs` (real `node:http` server, real
  transport, no injected fetch). Focused run
  `pnpm --dir sdk exec vitest run test/server.test.ts test/browser.test.ts
  test/datastore-live.test.mjs test/operations.test.ts test/version.test.mjs`
  → 5 files, 59 tests, all pass. Full `pnpm --dir sdk test` → 60/61: the
  single failure is the operation-coverage gate listing 13 datastore ops
  (`list/create/rename/delete/clear-datastore`, 3 column ops, filtered
  row writes, transfer pair) with no client method — pre-existing
  (xeq6st+t26rt7 registered 17 ops; this ticket adds none) and owned by
  FEAT-nc6z9r, whose ticket names that gate as its start signal. This
  ticket's 4 ops (`get-datastore`, `list/get-datastore-row(s)`,
  `insert-datastore-row`) are registered in `OPERATION_COVERAGE` and
  verified by the gate's method-existence test.
- [x] Box 6 — `pnpm --dir sdk generate:types` regenerated, then
  `pnpm --dir sdk generate:types:check` passed (exit 0). Regen diff is
  exactly the embed vocabulary (`datastoreId?` on body and resource,
  optional `workflowId`, widened scope docs, retitled operation).
- [x] Box 7 — `sdk/test/browser.test.ts`: `fails cleanly on a
  datastore-only session instead of mounting a dead iframe` — asserts the
  named throw and that no iframe was appended.
- [x] Box 8 — `TestATokenMintedBeforeDatastoresStillVerifies`: fixture
  token minted by the pre-change issuer at a fixed clock
  (`es_fixture00000000000001`, 2026-09-06T12:00Z) verifies under the new
  code and resolves to `wf-1` / `tenant-a` with exactly
  `workflow:read,workflow:write`.

Also green: `go build ./...`, `go vet` on all touched packages,
`gofmt` clean, `pnpm --dir sdk check` (tsc) clean, full
`go test ./internal/embed/ ./internal/api/...` ok.
