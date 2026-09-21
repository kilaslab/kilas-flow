---
id: FEAT-ew46cb
title: 'Agent debug primitives: workflow validate, run --revision, workflow duplicate, exec retry, debug eval'
status: done
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
updated: "2026-09-21T03:04:19Z"
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

- [x] `workflow validate` (validate a document without saving it), `run --revision` (run a pinned revision), `workflow duplicate`, `exec retry` — each an API operation first, then a CLI verb, then covered by the SDK operation gate.
      (Operations: `validate-workflow-document`, `duplicate-workflow`, `retry-execution`, plus `run-workflow`'s optional `workflowVersionId`; verbs: `workflow validate --file`, `workflow duplicate`, `exec retry`, `run --revision`, `debug eval`; the escape-hatch gate walks 51 contract rows against a real in-process server (`go test -run TestAPIEscapeHatchWalksTheContract ./internal/cli/`), which is the SDK operation gate. Live on a running server: validate answered `valid:false` naming the unregistered node type and its field; duplicate minted a new id; `run --revision` queued a pinned revision; `exec retry` queued a second execution of a finished one; `debug eval '$json'` answered `{}` and a malformed expression answered 422 with the reason.)
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

## CLI half (`DebugVerbs`) — one commit on `/tmp/wt-DebugVerbs`

Worktree `/tmp/wt-DebugVerbs`, detached from `main` at `b7b4478`. Files:
`internal/cli/verbs_workflow.go`, `internal/cli/verbs_exec.go`,
`internal/cli/verbs_debug.go` (new), `internal/cli/command.go` (registry),
`internal/cli/*_test.go`, `docs/src/content/docs/reference/cli.md`.

| Verb | Operation id in its metadata | Request it sends |
| --- | --- | --- |
| `workflow validate --file <path\|->` | `validate-workflow-document` | `POST /api/v1/workflows/validate`, the draft document unchanged |
| `workflow duplicate <id> [--name <n>]` | `duplicate-workflow` | `POST /api/v1/workflows/{id}/duplicate`, `{"name"}` only when `--name` was given |
| `exec retry <executionId>` | `retry-execution` | `POST /api/v1/executions/{id}/retry`, no body |
| `run <workflowId> --revision <versionId>` | `run-workflow` | the run body gains `workflowVersionId` |
| `debug eval <expression> --execution <id> [--node <nodeId>]` | `eval-expression` | `POST /api/v1/executions/{id}/eval`, `{expression, nodeId?}` |

All five are ordinary verbs: none carries `Guarded`, and none is a guardrail
case. `--quiet` prints the primary result of each (the new id; the validated
verdict; the evaluated value).

### Decisions taken on the CLI side

- **`workflow validate` exits 0 for a document that does not validate.** The
  operation answers `200` with `{valid, diagnostics}`, so the verdict travels in
  `data.valid` and in `--quiet`, and no exit code is invented for "the answer is
  no" — the exit vocabulary stays the one the design's §4.5 table fixes. The
  authority and absence codes are unchanged: a `403` exits 3 (`scope_denied`), a
  `404` exits 4.
- **`debug eval --execution` is required and is not defaulted to the newest
  run.** The route makes the execution the context source, so resolving "the
  newest execution" silently would let a caller evaluate against a run it did
  not mean — a guess dressed as a default. Omitted, it exits 2 with a message
  naming the flag and pointing at `exec list`. `--node` is the only optional
  narrowing within the execution the caller named; `--input-file` from design
  §4.2 is not part of this contract.
- **`debug eval` carries no guardrail refusal of its own.** It reads one
  execution the caller can already read, so its authority is exactly the scope
  the server asks for: a `403` exits 3 with `scope_denied` and another tenant's
  execution is a `404` that exits 4, both asserted in the tests.

### Proof

```text
$ gofmt -l internal/cli/            # silent
$ go build ./... && go vet ./...    # clean
$ go test -count=1 -run 'TestWorkflowValidate|TestWorkflowDuplicate|TestExecRetry|TestRunWithRevision|TestRunWithoutRevision|TestDebugEval' ./internal/cli/
ok  	github.com/kilaslab/kilas-flow/internal/cli	0.586s
```

The tests drive each verb through `Run` against the package's stub server and
assert the method, path and body that reached it and the exit code: `--file -`
reading stdin, the unchanged document, the duplicate's name, no body for a
retry, the `409` on a running execution, `workflowVersionId` in the run body,
the expression and the optional node id, the required `--execution`, a `404`
exiting 4 and a `403` exiting 3 with `scope_denied`.

A live smoke against a throwaway HTTP server (human output on a pty, envelopes
on a pipe) observed, for example:

```text
POST /api/v1/workflows/validate body='{"schemaVersion":1}'   -> exit 0, --quiet: false
POST /api/v1/executions/exec_1/eval body='{"expression":"$json.n + 1","nodeId":"HTTP Request"}'
POST /api/v1/executions/exec_1/retry body=''                 -> --quiet: exec_2
POST /api/v1/workflows/wf_1/run body='{"workflowVersionId":"wfv_2"}'  -> --quiet: exec_9
type   object / value  {"n":3}                               # debug eval, human mode
valid        false / diagnostics  1 / n1: blocking: unknown node type   # validate, human mode
```

`validate`'s diagnostics are the import path's own resource, carried unchanged:
`{severity, nodeId?, nodeName?, field?, type?, typeVersion?, reason}`, with
`severity: "blocking"` and the explanation under `reason` — no renamed field and
no invented `message` (corrected by the server half; the tests assert the real
keys). The verb is a passthrough of the resource; only the human printer picks
the node or field out for the line it writes.

### Known cross-half failure (expected)

`go test -count=1 ./internal/cli/... ./cmd/...` fails **only** in
`TestPhaseOneCommandTree` (`internal/cli/command_test.go`), which asserts that
every operation a verb names is served by the in-process server it builds from
`internal/api`:

```text
--- FAIL: TestPhaseOneCommandTree (0.01s)
    command_test.go:316: verb "workflow validate" drives "validate-workflow-document", which the running server does not serve
    command_test.go:316: verb "workflow duplicate" drives "duplicate-workflow", which the running server does not serve
    command_test.go:316: verb "debug eval" drives "eval-expression", which the running server does not serve
    command_test.go:316: verb "exec retry" drives "retry-execution", which the running server does not serve
    command_test.go:322: the phase-1 tree holds 57 verbs; the server serves 76 operations
```

`cmd/...` passes. The four ids are served once the server half lands (the
orchestrator confirmed the ids and paths and asked for the assertion to be left
strict), and this test then passes with no further CLI change.

### Not this half

- The API operations themselves, their tenant scoping and the scoped-token
  `permits` table (`internal/api/**`) — the server half, landing in parallel.
- `internal/cli/openapi_contract_test.go`'s walk and the SDK operation gate.
- The skills bundle still says "no server-side expression evaluation: debug eval
  is not shipped" (`skills/kilasflow-debugging`, `skills/kilasflow-expressions`)
  — that wording, and the embedded bundle, belong to the skills ticket. It is
  stale once this lands.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `b3c9f3f9` (last commit at or before ticket created 2026-09-20)
- Commits (4):
  - `12f615f9` — FEAT-ew46cb: the five verbs that drive the debug primitives
  - `2dedeb8c` — FEAT-ew46cb: serve workflow validate, duplicate, exec retry and debug eval
  - `8ac7c285` — chore(pine): record the debug-eval decision on FEAT-ew46cb
  - `aa307d46` — FEAT-mha6a0: cut the agent-surface implementation tickets from the design
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/capstone.yml                     |  127 +
 .github/workflows/ci.yml                           |   55 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/memory/docs.md                               |    8 +
 .pine/memory/tenancy.md                            |    8 +
 .pine/memory/testing.md                            |    9 +
 .pine/tickets/BUG-fng4m2.md                        |   88 +
 .pine/tickets/BUG-fvdz46.md                        |  368 +-
 .pine/tickets/BUG-p3t7yq.md                        |  212 +
 .pine/tickets/BUG-rpkjpy.md                        |  864 ++-
 .pine/tickets/BUG-t9j2ek.md                        |  410 +-
 .pine/tickets/BUG-vzzkg3.md                        |  558 +-
 .pine/tickets/BUG-w8h3km.md                        |  123 +
 .pine/tickets/BUG-xmr673.md                        |  707 ++-
 .pine/tickets/EPIC-bkj6yf.md                       |  680 ++-
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-15k49d.md                       | 1343 ++++-
 .pine/tickets/FEAT-1axhdn.md                       | 2017 ++++++-
 .pine/tickets/FEAT-3taswf.md                       | 1205 +++-
 .pine/tickets/FEAT-48hreg.md                       | 1441 ++++-
 .pine/tickets/FEAT-4jns31.md                       |  805 +++
 .pine/tickets/FEAT-5fhj6p.md                       |  292 +-
 .pine/tickets/FEAT-7cg0cd.md                       | 1339 ++++-
 .pine/tickets/FEAT-8mymac.md                       | 1115 +++-
 .pine/tickets/FEAT-bb4s6e.md                       |  837 +++
 .pine/tickets/FEAT-bp59m4.md                       |  420 ++
 .pine/tickets/FEAT-c72set.md                       |  811 +++
 .pine/tickets/FEAT-emf6k5.md                       |  536 +-
 .pine/tickets/FEAT-ew46cb.md                       |  231 +
 .pine/tickets/FEAT-fpqvwx.md                       |  893 ++-
 .pine/tickets/FEAT-hj8pyx.md                       |  714 ++-
 .pine/tickets/FEAT-m4d2y1.md                       |  763 +++
 .pine/tickets/FEAT-mha6a0.md                       |  123 +-
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++-
 .pine/tickets/FEAT-x5qqpm.md                       |  850 +++
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |  184 +-
 CONTRIBUTING.md                                    |    6 +
 Makefile                                           |  149 +-
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +
 cmd/kilasflow/fleet.go                             |   92 +
 cmd/kilasflow/fleet_test.go                        |  395 ++
 cmd/kilasflow/idempotency_test.go                  |  225 +
 cmd/kilasflow/main.go                              |  325 +-
 cmd/kilasflow/main_test.go                         |  115 +
 cmd/kilasflow/sidecar.go                           |  400 ++
 cmd/kilasflow/sidecar_test.go                      |  362 ++
 cmd/kilasflow/webhook_wiring_test.go               |  138 +
 config.example.yaml                                |  154 +-
 .../content/docs/concepts/datastore-concurrency.md |  179 +
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   42 +-
 .../src/content/docs/concepts/safety-boundaries.md |   41 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |   55 +
 docs/src/content/docs/guides/community-nodes.md    |  147 +-
 docs/src/content/docs/guides/embedding.md          |   41 +-
 docs/src/content/docs/guides/idempotency.md        |  197 +
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +
 .../content/docs/operate/acceptance-capstone.md    |  184 +
 docs/src/content/docs/operate/benchmark.md         |  229 +-
 .../docs/operate/configuration-reference.md        |  313 +-
 docs/src/content/docs/operate/deployment.md        |   31 +-
 .../src/content/docs/operate/javascript-sidecar.md |  193 +
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 +
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   39 +-
 docs/src/content/docs/reference/api.md             |   10 +-
 docs/src/content/docs/reference/api/datastores.md  |   33 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/executions.md  |   48 +-
 docs/src/content/docs/reference/api/interop.md     |    6 +
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |   53 +-
 docs/src/content/docs/reference/cli.md             |  838 +++
 docs/src/content/docs/reference/node-packs.md      |   80 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 e2e/.gitignore                                     |    1 +
 e2e/benchmark/README.md                            |  217 +-
 e2e/benchmark/SUMMARY.md                           |  109 +-
 e2e/benchmark/bench-2026-09-20T11-37-55.json       | 5978 ++++++++++++++++++++
 e2e/benchmark/bench-2026-09-20T12-01-21.json       | 5976 +++++++++++++++++++
 e2e/benchmark/lib.mjs                              |  405 +-
 e2e/benchmark/method.mjs                           |  393 ++
 e2e/benchmark/method.test.mjs                      |  366 ++
 e2e/benchmark/n8n-container.mjs                    |  458 ++
 e2e/benchmark/n8n-container.test.mjs               |  131 +
 e2e/benchmark/n8n.mjs                              |  416 +-
 e2e/benchmark/run.mjs                              |  877 ++-
 e2e/benchmark/scan-secrets.mjs                     |  196 +
 e2e/benchmark/scan-secrets.test.mjs                |   70 +
 e2e/benchmark/summarise.mjs                        |  393 +-
 e2e/benchmark/summarise.test.mjs                   |  303 +
 e2e/benchmark/workflows.mjs                        |  670 ++-
 e2e/benchmark/workflows.test.mjs                   |  223 +
 e2e/capstone/epic-capstone.spec.ts                 |  364 ++
 e2e/capstone/global-teardown.ts                    |   26 +
 e2e/fixtures/epic-config.ts                        |  172 +
 e2e/fixtures/epic-corpus.ts                        |  307 +
 e2e/fixtures/epic-external.ts                      |  293 +-
 e2e/fixtures/epic-hermetic.ts                      |  201 +
 e2e/fixtures/epic-image.ts                         |  456 ++
 e2e/fixtures/epic-proofs.ts                        |  854 +++
 e2e/fixtures/error-form-nodes.ts                   |  218 +
 e2e/fixtures/n8n-live.ts                           |    5 +
 e2e/helpers/stub.ts                                |   16 +-
 e2e/playwright.capstone.config.ts                  |   45 +
 e2e/scripts/capstone-lib.mjs                       |  519 ++
 e2e/scripts/capstone-report.mjs                    |  238 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 +
 e2e/tests/datastore.spec.ts                        |    2 +-
 e2e/tests/epic-acceptance.spec.ts                  |  694 +--
 e2e/tests/epic-machinery.spec.ts                   |  581 ++
 e2e/tests/i18n.spec.ts                             |   62 +
 e2e/tests/library-import.spec.ts                   |    6 +-
 e2e/tests/n8n-compare.spec.ts                      |   53 +-
 e2e/tests/node-coverage.spec.ts                    |   56 +-
 e2e/tests/pack-editor.spec.ts                      |   39 +-
 e2e/tests/waha-migration.spec.ts                   |   15 +-
 go.mod                                             |    1 +
 go.sum                                             |    2 +
 internal/api/cors_test.go                          |   19 +-
 internal/api/datastores_test.go                    |  418 ++
 internal/api/debug_ops_test.go                     |  476 ++
 internal/api/embed_datastore_test.go               |   28 +
 internal/api/embed_defaults_test.go                |  150 +
 internal/api/handlers/admin.go                     |  154 +
 internal/api/handlers/admin_admin_test.go          |  268 +-
 internal/api/handlers/api_key_scopes_test.go       |  138 +
 internal/api/handlers/auth.go                      |   83 +-
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/datastores.go                |  291 +-
 internal/api/handlers/execution_retry.go           |  206 +
 internal/api/handlers/executions.go                |   36 +-
 internal/api/handlers/idempotency.go               |  115 +
 internal/api/handlers/interop.go                   |   13 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 +-
 internal/api/handlers/workflow_duplicate.go        |   84 +
 internal/api/handlers/workflow_validate.go         |   98 +
 internal/api/handlers/workflows.go                 |  214 +-
 internal/api/idempotency_test.go                   | 1004 ++++
 internal/api/middleware/auth.go                    |    6 +
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |  140 +-
 internal/api/middleware/embed_sentences_test.go    |   35 +
 internal/api/middleware/embed_test.go              |   10 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/middleware/scope.go                   |  382 ++
 internal/api/middleware/scope_test.go              |  303 +
 internal/api/node_types_sidecar_test.go            |   82 +
 internal/api/node_visibility_test.go               |  544 ++
 internal/api/ready_fleet_test.go                   |  358 ++
 internal/api/routes.go                             |   19 +-
 internal/api/scoped_token_test.go                  |  154 +
 internal/api/server.go                             |   19 +-
 internal/api/tenant_delete_test.go                 |  386 ++
 internal/api/workflow_audit_test.go                |  209 +
 internal/auth/auth.go                              |   20 +
 internal/binary/binary.go                          |  117 +
 internal/binary/binary_test.go                     |  217 +
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  446 ++
 internal/cli/cli_test.go                           |  248 +
 internal/cli/client.go                             |  424 ++
 internal/cli/client_test.go                        |  320 ++
 internal/cli/command.go                            |  141 +
 internal/cli/command_test.go                       |  330 ++
 internal/cli/config.go                             |  258 +
 internal/cli/config_test.go                        |  749 +++
 internal/cli/context.go                            |  318 ++
 internal/cli/context_test.go                       |  236 +
 internal/cli/doc.go                                |   44 +
 internal/cli/exit.go                               |  125 +
 internal/cli/exit_test.go                          |  101 +
 internal/cli/flags.go                              |   88 +
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  543 ++
 internal/cli/openapi.go                            |  202 +
 internal/cli/openapi_contract_test.go              |  411 ++
 internal/cli/output.go                             |  116 +
 internal/cli/output_test.go                        |  246 +
 internal/cli/skills_used_test.go                   |   87 +
 internal/cli/sse.go                                |  151 +
 internal/cli/sse_test.go                           |  149 +
 internal/cli/verbs_api.go                          |  315 ++
 internal/cli/verbs_api_test.go                     |  820 +++
 internal/cli/verbs_auth.go                         |  314 +
 internal/cli/verbs_credential.go                   |  256 +
 internal/cli/verbs_credential_test.go              |  119 +
 internal/cli/verbs_datastore.go                    |  482 ++
 internal/cli/verbs_datastore_test.go               |  215 +
 internal/cli/verbs_debug.go                        |  156 +
 internal/cli/verbs_debug_test.go                   |  207 +
 internal/cli/verbs_exec.go                         |  450 ++
 internal/cli/verbs_exec_test.go                    |  501 ++
 internal/cli/verbs_node.go                         |  292 +
 internal/cli/verbs_node_test.go                    |  196 +
 internal/cli/verbs_pack.go                         |  146 +
 internal/cli/verbs_pack_test.go                    |  195 +
 internal/cli/verbs_run.go                          |  261 +
 internal/cli/verbs_run_test.go                     |  391 ++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_skills.go                       | 1068 ++++
 internal/cli/verbs_skills_test.go                  |  614 ++
 internal/cli/verbs_system.go                       |  282 +
 internal/cli/verbs_system_test.go                  |  182 +
 internal/cli/verbs_tenant.go                       |  169 +
 internal/cli/verbs_tenant_test.go                  |  116 +
 internal/cli/verbs_workflow.go                     |  929 +++
 internal/cli/verbs_workflow_test.go                |  590 ++
 internal/config/config.go                          |  387 +-
 internal/config/config_test.go                     |  365 ++
 internal/config/embed_branding_test.go             |  192 +
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 +
 internal/config/packs_visibility_test.go           |  168 +
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/registry.go                   |    5 +
 internal/database/migrate.go                       |   40 +-
 internal/database/migrate_test.go                  |   83 +-
 internal/database/tenant_columns_test.go           |  309 +
 internal/database/webhook_route_backfill_test.go   |  491 ++
 internal/database/workflow_actor_migration_test.go |  181 +
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +
 internal/datastore/concurrency.go                  |  224 +-
 internal/datastore/concurrency_test.go             |  579 +-
 internal/datastore/doc.go                          |   13 +-
 internal/datastore/engine.go                       |   27 +-
 internal/datastore/engine_test.go                  |   66 +-
 internal/datastore/fleet.go                        |  364 +-
 internal/datastore/fleet_engine_test.go            |  711 +++
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  245 +-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |  124 +-
 internal/datastore/upsert_id.go                    |  247 +
 internal/datastore/upsert_id_test.go               |  498 ++
 internal/embed/embed.go                            |  129 +-
 internal/embed/embed_branding_test.go              |  164 +
 internal/embed/embed_lifetime_test.go              |  146 +
 internal/engine/authenticate.go                    |   72 +-
 internal/engine/authenticate_test.go               |  130 +
 internal/engine/datastore_concurrency_test.go      |  393 ++
 internal/engine/eval.go                            |  299 +
 internal/engine/eval_test.go                       |  172 +
 internal/engine/export_test.go                     |   35 +
 internal/engine/service.go                         |   30 +-
 internal/engine/tenant_visibility_test.go          |  379 ++
 internal/engine/wait_service.go                    |   47 +-
 internal/engine/wait_service_test.go               |  219 +-
 internal/guardrails/compile_scope_test.go          |  440 ++
 internal/idempotency/hash.go                       |   64 +
 internal/idempotency/hash_test.go                  |  142 +
 internal/idempotency/idempotency.go                |  432 ++
 internal/idempotency/idempotency_test.go           | 1120 ++++
 internal/idempotency/sweeper.go                    |   94 +
 internal/idempotency/sweeper_test.go               |  146 +
 internal/interop/n8n/n8n_test.go                   |   55 +
 internal/interop/n8n/parameters.go                 |   11 +
 internal/node/registry.go                          |   31 +
 internal/node/registry_bench_test.go               |  112 +
 internal/node/visibility.go                        |  347 ++
 internal/node/visibility_test.go                   |  796 +++
 internal/nodepack/loaddir.go                       |   86 +
 internal/nodepack/loaddir_module_test.go           |  178 +
 internal/nodepack/module.go                        |  350 ++
 internal/nodepack/module_e2e_test.go               |   98 +
 internal/nodepack/module_test.go                   |  173 +
 internal/nodepack/nodepack.go                      |  116 +-
 internal/nodepack/trigger.go                       |    8 +
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   67 +-
 internal/nodepack/visibility_test.go               |  262 +
 internal/repository/api_key_scopes_test.go         |   78 +
 internal/repository/auth.go                        |   91 +-
 internal/repository/executions.go                  |   59 +-
 internal/repository/idempotency.go                 |  360 ++
 internal/repository/idempotency_test.go            |  615 ++
 internal/repository/models.go                      |   66 +-
 internal/repository/postgres_execution_test.go     |   16 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   48 +
 internal/repository/tenant_rows.go                 |  283 +
 internal/repository/tenant_rows_test.go            |  420 ++
 internal/repository/webhooks.go                    |  124 +-
 internal/repository/webhooks_delivery_test.go      |  147 +
 internal/repository/webhooks_test.go               |  184 +-
 internal/repository/workflow_history.go            |  119 +-
 internal/repository/workflow_history_test.go       |  102 +-
 internal/repository/workflows.go                   |   22 +-
 internal/runcode/diagnostic.go                     |   72 +
 internal/runcode/diagnostic_internal_test.go       |   91 +
 internal/runcode/diagnostic_test.go                |   78 +
 internal/runcode/doc.go                            |   73 +-
 internal/runcode/errors.go                         |   25 +
 internal/runcode/inspect.go                        |   90 +
 internal/runcode/inspect_test.go                   |  120 +
 internal/runcode/persist.go                        |  465 ++
 internal/runcode/persist_test.go                   |  696 +++
 internal/runcode/runcode.go                        |  227 +-
 internal/runcode/runcode_test.go                   |   23 +-
 internal/runcode/sandbox.go                        |  334 ++
 internal/runcode/sandbox_test.go                   |  279 +
 internal/sidecarnode/catalogue.go                  |  115 +
 internal/sidecarnode/convert.go                    |  854 +++
 internal/sidecarnode/convert_test.go               |  554 ++
 internal/sidecarnode/load.go                       |  217 +
 internal/sidecarnode/load_test.go                  |  360 ++
 internal/skills/bundle.go                          |   23 +
 internal/skills/bundle_test.go                     |  273 +
 internal/skills/check.go                           |  584 ++
 internal/skills/check_test.go                      |  130 +
 internal/skills/embed_test.go                      |  169 +
 internal/skills/index.go                           |   98 +
 internal/skills/skill.go                           |  416 ++
 internal/skills/skills_test.go                     |  794 +++
 .../clean/skills/kilasflow-fixture/SKILL.md        |   43 +
 .../skills/kilasflow-fixture/references/NOTES.md   |    3 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   42 +
 .../skills/kilasflow-fixture/SKILL.md              |   36 +
 .../skills/kilasflow-fixture/SKILL.md              |   40 +
 .../skills/kilasflow-fixture/references/PRESENT.md |    3 +
 .../missing-key/skills/kilasflow-fixture/SKILL.md  |   32 +
 .../skills/kilasflow-fixture/SKILL.md              |   41 +
 .../skills/kilasflow-fixture/references/PRESENT.md |    3 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   38 +
 .../skills/kilasflow-credentials/SKILL.md          |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   39 +
 .../skills/kilasflow-fixture/references/EXTRA.md   |    3 +
 .../unknown-key/skills/kilasflow-fixture/SKILL.md  |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   43 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 internal/tenantpurge/completeness_test.go          |  368 ++
 internal/tenantpurge/doc.go                        |  120 +
 internal/tenantpurge/docs_test.go                  |  115 +
 internal/tenantpurge/harness_test.go               |  614 ++
 internal/tenantpurge/purge.go                      |  412 ++
 internal/tenantpurge/purge_test.go                 |  507 ++
 internal/wasmpack/audit.go                         |  116 +
 internal/wasmpack/audit_test.go                    |  234 +
 internal/wasmpack/caps.go                          |  130 +
 internal/wasmpack/executor.go                      |  218 +
 internal/wasmpack/executor_test.go                 |  108 +
 internal/wasmpack/host.go                          |  480 ++
 internal/wasmpack/host_internal_test.go            |  161 +
 internal/wasmpack/hostcalls_binary.go              |  114 +
 internal/wasmpack/hostcalls_credentials.go         |   89 +
 internal/wasmpack/hostcalls_http.go                |  248 +
 internal/wasmpack/hostcalls_test.go                |  343 ++
 internal/wasmpack/legacy_test.go                   |   53 +
 internal/wasmpack/limits.go                        |   99 +
 internal/wasmpack/probe_test.go                    |  521 ++
 internal/wasmpack/registry.go                      |   89 +
 internal/wasmpack/registry_test.go                 |  116 +
 internal/wasmpack/spec.go                          |   90 +
 internal/wasmpack/support_test.go                  |  395 ++
 internal/wasmpack/testdata/probe/main.go           |  131 +
 internal/wasmtest/wasmtest.go                      |  359 ++
 internal/wasmtest/wasmtest_test.go                 |  191 +
 internal/webhook/require_auth.go                   |   74 +
 internal/webhook/require_auth_test.go              |  367 ++
 internal/webhook/route_label_test.go               |  172 +
 internal/webhook/shape.go                          |   10 +
 internal/webhook/webhook.go                        |   17 +-
 internal/webhook/webhook_test.go                   |    2 +
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_visibility_test.go      |  280 +
 internal/workflow/lifecycle.go                     |   21 +-
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 migrations/postgres/000018_api_key_scopes.down.sql |   10 +
 migrations/postgres/000018_api_key_scopes.up.sql   |   18 +
 migrations/postgres/000019_workflow_actor.down.sql |   21 +
 migrations/postgres/000019_workflow_actor.up.sql   |   55 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 migrations/sqlite/000018_api_key_scopes.down.sql   |   10 +
 migrations/sqlite/000018_api_key_scopes.up.sql     |   18 +
 migrations/sqlite/000019_workflow_actor.down.sql   |   21 +
 migrations/sqlite/000019_workflow_actor.up.sql     |   56 +
 nodes/code.go                                      |   26 +-
 nodes/code_test.go                                 |  131 +
 nodes/datastore.go                                 |   83 +-
 nodes/datastore_increment.go                       |   85 +
 nodes/datastore_test.go                            |  177 +
 nodes/executors.go                                 |   28 +-
 nodes/pgvector_test.go                             |   42 +-
 nodes/sidecar.go                                   |  383 ++
 nodes/sidecar_egress.go                            |  207 +
 nodes/sidecar_egress_internal_test.go              |  257 +
 nodes/sidecar_egress_test.go                       |  276 +
 nodes/sidecar_engine_test.go                       |  393 ++
 nodes/sidecar_test.go                              |  666 +++
 nodes/sql_options_live_test.go                     |   17 +-
 pkg/sdk/abi.go                                     |  298 +
 pkg/sdk/abi_test.go                                |  360 ++
 pkg/sdk/call.go                                    |  149 +
 pkg/sdk/capabilities.go                            |  169 +
 pkg/sdk/doc.go                                     |   83 +-
 pkg/sdk/example/fetch/main.go                      |   70 +
 pkg/sdk/guest_wasip1.go                            |   69 +
 pkg/sdk/host_other.go                              |  174 +
 pkg/sdk/host_wasip1.go                             |   41 +
 pkg/sdk/internal/abigen/abigen.go                  |   85 +
 pkg/sdk/internal/abigen/cmd/main.go                |   33 +
 pkg/sdk/sdk.go                                     |   55 +-
 pkg/sdk/sdk_test.go                                |   91 +
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |   23 +-
 scripts/skills-command-reference/main.go           |  375 ++
 scripts/skills-command-reference/main_test.go      |  124 +
 scripts/skills-index/main.go                       |  144 +
 scripts/smoke-cli.sh                               |  228 +
 scripts/smoke-postgres.sh                          |   22 +
 sdk/CHANGELOG.md                                   |   33 +-
 sdk/LICENSE                                        |  202 +
 sdk/README.md                                      |  101 +-
 sdk/RELEASING.md                                   |  188 +
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/examples/reference-host/server.mjs             |    8 +-
 sdk/examples/reference-host/tenant.html            |    3 +
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++
 sdk/scripts/check-package.mjs                      |  315 ++
 sdk/scripts/lib/pack.mjs                           |   77 +
 sdk/scripts/lib/release.mjs                        |  266 +
 sdk/scripts/release.mjs                            |  149 +
 sdk/src/browser.ts                                 |   13 +-
 sdk/src/generated/models.ts                        |  593 +-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  161 +-
 sdk/test/browser.test.ts                           |   24 +
 sdk/test/operation-coverage.test.mjs               |   23 +
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 +
 sdk/test/release.test.mjs                          |  390 ++
 sdk/test/server.test.ts                            |  131 +-
 sidecar/discover.go                                |   71 +
 sidecar/doc.go                                     |   68 +-
 sidecar/fixture/crash.js                           |   19 +
 sidecar/fixture/dial.js                            |   32 +
 sidecar/fixture/env.js                             |   48 +
 sidecar/fixture/forged.js                          |   52 +
 sidecar/fixture/hang.js                            |   27 +
 sidecar/fixture/heap.js                            |   27 +
 sidecar/fixture/perm.js                            |   57 +
 sidecar/fixture/rss.js                             |   39 +
 sidecar/fixture_test.go                            |    9 +-
 sidecar/hostcall_test.go                           |  346 ++
 sidecar/hostile_test.go                            |  199 +
 sidecar/isolation_test.go                          |  845 +++
 sidecar/logwriter.go                               |   77 +
 sidecar/logwriter_test.go                          |  111 +
 sidecar/memory.go                                  |   51 +
 sidecar/memory_darwin.go                           |   30 +
 sidecar/memory_linux.go                            |   30 +
 sidecar/memory_other.go                            |   12 +
 sidecar/procattr_other.go                          |   30 +
 sidecar/procattr_unix.go                           |   50 +
 sidecar/process.go                                 |  347 ++
 sidecar/process_test.go                            |  238 +
 sidecar/protocol.go                                |  159 +-
 sidecar/runner.go                                  |  182 +
 sidecar/runner/runner.cjs                          |  966 ++++
 sidecar/runner_test.go                             | 1094 ++++
 sidecar/sidecar.go                                 |  654 ++-
 sidecar/sidecar_test.go                            |    2 +-
 sidecar/sidecartest/sidecartest.go                 |   96 +
 .../kf-fixture-badload/dist/nodes/Bad/Bad.node.js  |   19 +
 .../packages/kf-fixture-badload/package.json       |   12 +
 .../dist/credentials/Escape.credentials.js         |    1 +
 .../dist/nodes/Ok/Ok.node.js                       |   16 +
 .../packages/kf-fixture-badmanifest/package.json   |   16 +
 .../dist/nodes/Hostile/Hostile.node.js             |  139 +
 .../dist/nodes/Noisy/Noisy.node.js                 |   36 +
 .../packages/kf-fixture-hostile/package.json       |   13 +
 .../kf-fixture-netload/dist/nodes/Net/Net.node.js  |   19 +
 .../packages/kf-fixture-netload/package.json       |   12 +
 .../dist/credentials/FixtureApi.credentials.js     |   36 +
 .../dist/nodes/Greet/Greet.node.js                 |  154 +
 .../dist/nodes/Relay/Relay.node.js                 |   60 +
 .../packages/kf-fixture-nodes/package.json         |   17 +
 .../dist/nodes/Distinct/Distinct.node.js           |   56 +
 .../dist/nodes/Trigger/Trigger.node.js             |   30 +
 .../packages/kf-fixture-unsupported/package.json   |   13 +
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   35 +
 .../dist/nodes/Trigger/Trigger.node.js             |   29 +
 .../dist/nodes/v2/FooV2.node.js                    |   33 +
 .../packages/kf-fixture-versions/package.json      |   14 +
 skills/embed.go                                    |   50 +
 skills/index.json                                  |  515 ++
 skills/kilasflow-credentials/SKILL.md              |  114 +
 .../references/CREDENTIAL_TYPES.md                 |  119 +
 skills/kilasflow-datastore/SKILL.md                |  127 +
 skills/kilasflow-datastore/references/FILTERS.md   |  140 +
 skills/kilasflow-debugging/SKILL.md                |  120 +
 .../references/TRACE_READING.md                    |  135 +
 skills/kilasflow-embedding/SKILL.md                |  122 +
 .../references/EMBED_HANDSHAKE.md                  |  134 +
 .../references/SESSION_AUTHORITY.md                |   67 +
 skills/kilasflow-error-handling/SKILL.md           |   97 +
 skills/kilasflow-expressions/SKILL.md              |  112 +
 .../references/EXPRESSION_ROOTS.md                 |  161 +
 skills/kilasflow-import-export/SKILL.md            |   96 +
 .../references/MAPPING_LIMITS.md                   |  143 +
 skills/kilasflow-node-configuration/SKILL.md       |  105 +
 .../references/LOAD_OPTIONS.md                     |  141 +
 .../references/PROPERTY_KINDS.md                   |  165 +
 skills/kilasflow-node-packs/SKILL.md               |   99 +
 .../references/MODULE_AND_SIDECAR.md               |   95 +
 .../kilasflow-node-packs/references/PACK_FORMAT.md |  114 +
 skills/kilasflow-operations/SKILL.md               |  109 +
 .../references/UPGRADE_ORDER.md                    |  210 +
 skills/kilasflow-triggers/SKILL.md                 |  132 +
 .../references/WEBHOOK_DELIVERY.md                 |  148 +
 skills/kilasflow-workflow-lifecycle/SKILL.md       |  115 +
 .../references/NAMING_AND_DESCRIPTIONS.md          |   81 +
 .../references/VALIDATION_CHECKLIST.md             |   95 +
 skills/using-kilasflow-skills/SKILL.md             |  223 +
 web/messages/en/auth.json                          |   36 +
 web/messages/en/canvas.json                        |   52 +
 web/messages/en/common.json                        |   27 +
 web/messages/en/credentials.json                   |   40 +
 web/messages/en/datastores.json                    |  176 +
 web/messages/en/editor.json                        |  134 +
 web/messages/en/embed.json                         |   16 +
 web/messages/en/executions.json                    |   94 +
 web/messages/en/home.json                          |   20 +
 web/messages/en/nav.json                           |   10 +
 web/messages/en/properties.json                    |  118 +
 web/messages/en/schedules.json                     |   33 +
 web/messages/en/settings.json                      |   46 +
 web/messages/en/versions.json                      |   89 +
 web/messages/en/workflows.json                     |  223 +
 web/messages/id/auth.json                          |   36 +
 web/messages/id/canvas.json                        |   52 +
 web/messages/id/common.json                        |   27 +
 web/messages/id/credentials.json                   |   40 +
 web/messages/id/datastores.json                    |  209 +
 web/messages/id/editor.json                        |  164 +
 web/messages/id/embed.json                         |   16 +
 web/messages/id/executions.json                    |   94 +
 web/messages/id/home.json                          |   20 +
 web/messages/id/nav.json                           |   10 +
 web/messages/id/properties.json                    |  123 +
 web/messages/id/schedules.json                     |   33 +
 web/messages/id/settings.json                      |   46 +
 web/messages/id/versions.json                      |   89 +
 web/messages/id/workflows.json                     |  222 +
 web/package.json                                   |    7 +-
 web/pnpm-lock.yaml                                 |  204 +
 web/project.inlang/settings.json                   |   25 +
 web/src/lib/api/generated/admin/admin.ts           |   94 +
 .../api/generated/datastore-rows/datastore-rows.ts |  110 +-
 web/src/lib/api/generated/executions/executions.ts |  222 +
 web/src/lib/api/generated/models/aPIKeyResource.ts |    8 +
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   10 +
 .../api/generated/models/createdAPIKeyResource.ts  |    7 +
 .../api/generated/models/deleteRowsInputBody.ts    |    2 +
 .../generated/models/duplicateWorkflowInputBody.ts |   17 +
 .../generated/models/evalExpressionInputBody.ts    |   20 +
 .../api/generated/models/evalExpressionResource.ts |   17 +
 .../generated/models/evalExpressionResourceType.ts |   22 +
 .../api/generated/models/incrementRowsInputBody.ts |   22 +
 .../generated/models/incrementRowsOutputBody.ts    |   16 +
 .../models/incrementRowsOutputBodyRowsItem.ts      |    9 +
 web/src/lib/api/generated/models/index.ts          |   16 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/principalResource.ts  |    9 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 .../api/generated/models/updateRowsInputBody.ts    |    2 +
 .../generated/models/validateWorkflowResource.ts   |   20 +
 .../models/workflowPublishEventResource.ts         |    7 +
 .../workflowPublishEventResourceActorKind.ts       |   18 +
 .../models/workflowVersionSummaryResource.ts       |   12 +
 .../workflowVersionSummaryResourceActorKind.ts     |   18 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  201 +
 web/src/lib/api/http.ts                            |    4 +-
 .../lib/components/dashboard/dashboard-nav.svelte  |   56 +-
 .../lib/components/dashboard/list-states.svelte    |   11 +-
 .../components/dashboard/locale-switcher.svelte    |   31 +
 .../lib/components/ui/dialog/dialog-content.svelte |    3 +-
 .../lib/components/ui/dialog/dialog-footer.svelte  |    3 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    3 +-
 .../workflow-editor/activation-notices.svelte      |   15 +-
 .../components/workflow-editor/canvas-edge.svelte  |    5 +-
 .../components/workflow-editor/canvas-node.svelte  |   27 +-
 .../workflow-editor/editor-controls.svelte         |    6 +-
 .../workflow-editor/execution-canvas-node.svelte   |    9 +-
 .../workflow-editor/execution-canvas.svelte        |   30 +-
 .../components/workflow-editor/node-picker.svelte  |   15 +-
 .../workflow-editor/properties-panel.svelte        |   47 +-
 .../workflow-editor/property-field.svelte          |  228 +-
 .../workflow-editor/property-field.test.ts         |   81 +
 .../workflow-editor/version-panel.svelte           |  107 +-
 .../workflow-editor/workflow-editor.svelte         |  157 +-
 web/src/lib/dashboard/cursor-page.test.ts          |  285 +-
 web/src/lib/dashboard/cursor-page.ts               |   96 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 +-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/nav-sections.test.ts         |   55 +-
 web/src/lib/dashboard/nav-sections.ts              |   59 +
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   24 +-
 web/src/lib/datastore/columns.test.ts              |   41 +
 web/src/lib/datastore/columns.ts                   |   39 +-
 web/src/lib/datastore/transfer.ts                  |   15 +-
 web/src/lib/embed/embed-editor.svelte              |   37 +-
 web/src/lib/embed/session.svelte.ts                |   50 +-
 web/src/lib/embed/session.test.ts                  |   20 +-
 web/src/lib/i18n/catalog.test.ts                   |   75 +
 web/src/lib/i18n/copy.test.ts                      |  399 ++
 web/src/lib/i18n/locale.svelte.ts                  |  107 +
 web/src/lib/i18n/locale.test.ts                    |   90 +
 web/src/lib/workflow-editor/activation.ts          |    3 +-
 web/src/lib/workflow-editor/document.ts            |    3 +-
 web/src/lib/workflow-editor/execution.test.ts      |   48 +-
 web/src/lib/workflow-editor/execution.ts           |   55 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   13 +-
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 10556 -> 10731 bytes
 web/src/lib/workflow-editor/import-diagnostics.ts  |    3 +-
 web/src/lib/workflow-editor/ports.ts               |    9 +-
 web/src/lib/workflow-editor/shortcuts.ts           |   53 +-
 .../lib/workflow-editor/version-history.test.ts    |   65 +-
 web/src/lib/workflow-editor/version-history.ts     |   82 +-
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  162 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   53 +-
 .../app/workflows/[id]/export-dialog.svelte        |   31 +-
 .../app/workflows/diagnostics-section.svelte       |   50 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   39 +-
 .../app/workflows/import-report-drawer.svelte      |    9 +-
 .../(dashboard)/app/workflows/import-report.svelte |   30 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   71 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   56 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  169 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  212 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  108 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   67 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  119 +-
 web/src/routes/+layout.svelte                      |   11 +
 web/src/routes/+page.svelte                        |   42 +-
 web/src/routes/approve/[token]/+page.svelte        |   51 +-
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/src/routes/login/+page.svelte                  |   21 +-
 web/vite.config.ts                                 |   21 +-
 692 files changed, 126017 insertions(+), 5299 deletions(-)
```
