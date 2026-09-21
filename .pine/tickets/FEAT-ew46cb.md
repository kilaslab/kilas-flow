---
id: FEAT-ew46cb
title: 'Agent debug primitives: workflow validate, run --revision, workflow duplicate, exec retry, debug eval'
status: todo
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
- [ ] `debug eval` is decided per design §9 question 1 and recorded on this ticket: a read-only expression evaluator over an execution's context (with its attack surface reviewed) or replaced by re-run-with-a-patched-node.
- [ ] Every new operation respects tenant scoping and the scoped-token refusals of the scoped-tokens ticket.
