---
id: FEAT-ew46cb
title: 'Agent debug primitives: workflow validate, run --revision, workflow duplicate, exec retry, debug eval'
status: doing
priority: medium
labels:
    - agent
    - api
    - engine
deps:
    - FEAT-m4d2y1
parent: EPIC-r0yg5q
phase: p3
created: "2026-09-20T07:47:53Z"
updated: "2026-09-20T07:47:53Z"
---

## Scope

Design §4.2 and §9 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the primitives an agent needs for the create → validate → run → observe → patch loop that do not exist as API operations today.


## Decision: `debug eval` is kept, as a read-only evaluator (design §9 question 1)

Recorded here because the ticket asks for the decision, not for an implementation choice.

**Decision: keep it.** The create → validate → run → observe → patch loop's "observe" step is
where an agent fails without it, and the alternative — re-run with a patched node — answers a
different question by *changing* the workflow and spending a run. "What did this field hold at
this node" is not answerable by re-running, and the re-run costs a mutation plus a real
execution's side effects.

**Shape.** `POST /api/v1/executions/{id}/eval` (operation id `eval-expression`), body
`{expression, nodeId?}`, answered with the value and the type it had. `kilasflow debug eval
<expression> [--execution <id>] [--node <nodeId>]` is the verb. It loads the execution under the
caller's tenant (another tenant's id reads as 404, as every other execution read does),
rebuilds an `expression.Context` from the stored node outputs the trace view already shows, and
evaluates with `expression.Evaluate` — the evaluator the runtime already uses for tenant-authored
documents. No new interpreter, no new grammar.

**Attack surface, reviewed against what already exists.** The endpoint grants no new data
access: everything it reads is what `GET /executions/{id}` and the node-run trace already return
to the same caller, and the scoped-token gate puts it on `workflow:read` with the embed arm
refusing it outright. It grants no new code execution: the grammar is the one every workflow
document is already evaluated against. The three risks worth naming are bounded as follows.

- *Unbounded work.* Evaluation runs under the same deadline the runtime gives a node, so an
  expression that loops or explodes cannot hold a request open.
- *`$env`.* The context's `Env` is the runtime's allowlist — `KILASFLOW_WORKFLOW_ENV_*` only
  (`cmd/kilasflow/main.go`, `workflowEnvironment`), never `os.Environ`, so the database DSN and
  the credential master key are not reachable, and the exposed values are ones a workflow author
  can already read at runtime.
- *Enumeration.* Tenant scoping plus the 404-not-403 rule for a bound token, so the endpoint
  cannot be used to probe for ids.

The decision is recorded with the alternatives it beat: refusing the endpoint (the loop loses its
cheapest observation step), and a client-side evaluator (a second implementation of the grammar,
which would drift from the engine's — exactly the failure the design's §5.2 declarations exist to
prevent).

## Acceptance criteria

- [ ] `workflow validate` (validate a document without saving it), `run --revision` (run a pinned revision), `workflow duplicate`, `exec retry` — each an API operation first, then a CLI verb, then covered by the SDK operation gate.
- [x] `debug eval` is decided per design §9 question 1 and recorded on this ticket: a read-only expression evaluator over an execution's context (with its attack surface reviewed) or replaced by re-run-with-a-patched-node.
- [x] Every new operation respects tenant scoping and the scoped-token refusals of the scoped-tokens ticket.

## Server half (implemented)

Five operations, the seams they sit on, and what proves each one. Everything below
was run in the worktree and the commands are the ones in the evidence column.

- `validate-workflow-document` (`POST /workflows/validate`) compiles the supplied
  document with `workflow.Compile` under `workflow.CatalogFor(catalog, tenant)` and
  answers `{valid, diagnostics}` — nothing is saved. Diagnostics reuse
  `n8n.ImportIssue`, the import path's own type, with severity `blocking`, the
  compiler's message in `reason` and its path in `field` (the contract's
  `{nodeId?, message, severity}` names the same three facts under the import
  report's key names, because a second diagnostic type for one question is a
  second thing for the editor, the CLI and the import report to drift apart on).
- `duplicate-workflow` (`POST /workflows/{id}/duplicate`) copies the latest
  revision into a new workflow under the caller's tenant, named `<name> (copy)`
  unless the request names one, saved through the ordinary draft path so the copy
  is revision 1 of an ordinary workflow. It always gets a server-minted identity:
  carrying the source id would append a revision to the original. **No name
  collision is refused, and none can be**: `workflows.name` carries no unique
  index (models.go:214, and migrations/{sqlite,postgres}/000001_baseline.up.sql
  create none), `POST /workflows` and import both accept a repeated name, and
  refusing one here would make `duplicate` the only verb that does — while
  breaking the second copy of the same workflow. The one name rule the schema
  does impose is the 255-character column, refused as an invalid draft (422)
  through the existing `draftProblem` path.
- `retry-execution` (`POST /executions/{id}/retry`) queues the original's
  workflow, revision and input through the same body a manual run uses
  (`repository.QueueManualVersion`, which shares `QueueManualLatest`'s
  transaction, catalogue check and start-node check — one path, two ways to name
  the revision). 409 while the original is queued, running, cancelling or
  waiting; 404 for another tenant's execution.
- `run-workflow` gained the optional `workflowVersionId` body field, resolved by
  the same `QueueManualVersion`; a version that is not this workflow's reads as
  404 (`ErrNotFound`), and the idempotency fingerprint covers the field.
- `eval-expression` (`POST /executions/{id}/eval`) loads the execution under the
  caller's tenant, rebuilds an `expression.Context` from the stored node runs
  (keyed by the revision's node names, redacted on the way in exactly as the
  trace view redacts on the way out), and evaluates with `expression.Evaluate`
  under the budget the runtime gives that node (`Service.runBudget` refined by
  the node's `timeoutSeconds`). `$env` is `Service.environment` — the runtime's
  allowlist — and never `os.Environ`. No writes and no display of anything
  `GET /executions/{id}` does not already return.
- `internal/api/middleware/scope.go`: `/workflows/validate` is a read matched
  before the `/workflows/` prefix arm; `/executions/{id}/retry` needs
  `workflow:run` while `/executions/{id}/eval` stays on read; `duplicate` is
  refused to an embed session and to a bound key (a copy is a new workflow, the
  import case) and takes `workflow:write` otherwise. All four have refusal and
  allowed arms in `scope_test.go`, and the mounted gate is exercised against the
  real server in `internal/api/debug_ops_test.go`.

Evidence (worktree `/tmp/wt-debugops`, `main` at b7b4478):

- `go test -count=1 ./internal/api/... ./internal/workflow/... ./internal/engine/... ./internal/repository/... ./internal/cli/...` — all `ok`.
- `go build ./... && go vet ./...` — clean; `gofmt -l <files>` — silent.
- `make generate-api generate-types generate-api-reference` then
  `generate-api-reference-check`, `web/scripts/check-api-client.mjs`,
  `sdk/scripts/check-types.mjs` — `14 pages fresh (KilasFlow 0.1.0-dev, 80 operations)`.
- The escape-hatch gate's page gained the four rows and its count moved 47 → 51
  (`internal/cli/openapi_contract_test.go`); `scripts/generate-api-reference.mjs`
  maps the four ids into the workflows and executions groups.

