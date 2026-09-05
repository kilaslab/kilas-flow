---
id: FEAT-a6yg3n
title: Honour continueOnFail, retryOnFail and maxTries in the runner
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:01:54Z"
updated: "2026-09-05T05:01:54Z"
---

## Scope

`sharedSettings()` in `nodes/core.go:121-131` declares four settings on every registered node: `continueOnFail`, `retryOnFail`, `timeoutSeconds` and `maxTries` (the last gated on `retryOnFail` being true). The editor renders all four, the API serves all four in the node-type metadata, and a user can set all four. Only `timeoutSeconds` is read by any Go code: `nodeContext` in `internal/engine/runner.go:266-281` builds a per-node deadline from it. A grep for the other three across the Go tree returns their declaration in `nodes/core.go` and nothing else.

So a workflow author sets "Continue on Fail" on an HTTP node, saves it, and the first 500 from the upstream API still aborts the whole execution — `Runner.Run` returns at runner.go:236-238 the moment an executor errors. "Retry on Fail" with "Maximum Attempts 3" produces exactly one attempt. This is not a missing feature; it is a control that visibly exists and does nothing, which is worse.

The persistence layer is already shaped for retries and unused: `executionNodeRunModel` carries an `Attempt` column with a unique index on `(execution_id, node_id, attempt)` (internal/repository/models.go:133-149), `execution.NodeRun` exposes it (internal/execution/records.go:58), the API serializes it (internal/api/handlers/workflows.go:491), and `internal/engine/service.go:162` hardcodes `Attempt: 1`. Every attempt should have been a row from the start.

The importer must not map n8n's equivalents until this lands. n8n nodes carry `continueOnFail`, `retryOnFail`, `maxTries`, `waitBetweenTries`, `alwaysOutputData`, `executeOnce` and `onError` at node top level; the importer's `Node` struct (internal/interop/n8n/n8n.go:44-54) reads none of them. Mapping them onto settings the runner ignores would turn a silent drop into a documented lie.

## Acceptance criteria

- [x] A node whose executor returns an error and whose `continueOnFail` is true does not abort the execution; the run continues with that node emitting an error item on its `main` output, and the execution finishes with a terminal status that reflects what happened.
- [x] A node with `retryOnFail` true is attempted up to `maxTries` times, and stops on the first success.
- [x] Every attempt is persisted as its own `execution_node_runs` row with an incrementing `Attempt`, and the API returns them in attempt order.
- [x] A retried node that exhausts `maxTries` and has `continueOnFail` false fails the execution exactly as it does today, with the error from the last attempt.
- [x] `maxTries` is bounded and validated: a non-numeric, negative, or absurdly large value is refused at compile time, not at run time.
- [x] The delay between attempts is configurable per node and defaults to a sane non-zero value; a retry storm against a failing upstream is not possible with default settings.
- [x] A workflow imported from n8n carries the source node's error-handling settings, and any n8n error-handling field with no KilasFlow equivalent produces an import diagnostic naming it.

## Implementation Plan

All three settings belong in `Runner.Run`, around the single `executor.Execute` call at runner.go:227. Read them from `node.Settings` with the same defensive numeric coercion `timeoutSeconds` already uses (`timeoutSeconds`, runner.go:283-307) — settings arrive from JSON, so an `int` is a `float64` and a `json.Number` is possible; write one shared coercion helper rather than a second copy of that switch.

The attempt loop wraps the executor call and rebuilds the per-node context each time, because `nodeContext` returns a cancel func that must fire per attempt, not once for all of them. Record every attempt into `Result.NodeRuns`, not only the last: `engine.NodeRun` gains an `Attempt int`, and `internal/engine/service.go:162` stops hardcoding `1`. The `Sequence` field stays a monotonic counter over all rows.

`continueOnFail` is the part with a real design decision, because the item shape after a tolerated failure determines what every downstream expression sees. n8n emits one item per input item carrying `{error: "…"}` and, with `alwaysOutputData`, an empty item when there is nothing. Two options: emit a single `{$error: {code, message}}` item on the first `main` output, or emit one error item per input item so downstream item counts and paired-item lineage survive. Recommendation: one error item per input item, falling back to a single item when the node had no input, because it is the only variant that keeps lineage intact and it matches n8n's observable behaviour. Do not pass the input items through unchanged — a downstream node cannot then tell success from tolerated failure.

Add `waitBetweenTries` as a declared setting alongside `maxTries` with the same `VisibleWhen` gate, defaulting to a non-zero delay. Without it the first user who ticks Retry on Fail against a rate-limited API sends three requests inside a millisecond.

Validation belongs in the compiler path, not the runner: an out-of-range `maxTries` should fail the same way an invalid `timeoutSeconds` does today (`config.invalid`, runner.go:222-226), except earlier — settings are static, so checking them at compile time gives the editor an error before activation.

Finally the importer. Add the n8n error-handling fields to `n8n.Node` and map `continueOnFail`, `retryOnFail`, `maxTries` and `waitBetweenTries` onto the canonical settings; report `alwaysOutputData`, `executeOnce` and `onError` as diagnostics until they have equivalents. Adding an attempt column to the node-run API response is an OpenAPI change — regenerate with `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`.

## References

- Roadmap plan, p1 section, entry V2-p1-5: `.pine/roadmap.md`.
- `nodes/core.go` — `sharedSettings`, the four declared settings.
- `internal/engine/runner.go` — `Run`, `nodeContext`, `timeoutSeconds`, the error return path.
- `internal/engine/service.go` — the node-run persistence loop and its hardcoded `Attempt: 1`.
- `internal/repository/models.go`, `internal/execution/records.go`, `internal/api/handlers/workflows.go` — the attempt column that already exists end to end.
- `internal/interop/n8n/n8n.go` — the `Node` struct that reads none of n8n's error-handling fields.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 04 — the node Settings tab: Always Output Data, Execute Once, Retry On Fail and the On Error dropdown, which is the surface these engine settings must actually drive. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Outcome

Three settings that visibly existed and did nothing now work. `continueOnFail`,
`retryOnFail` and `maxTries` were rendered by the editor, served by the API and
settable by a user, and no Go code read any of them — so ticking "Retry on Fail,
Maximum Attempts 3" produced exactly one attempt.

### The attempt loop

It wraps the single `executor.Execute` call, and rebuilds the per-node context
each time round: `nodeContext` returns a cancel that must fire per attempt, and
sharing one deadline would make the second attempt inherit whatever the first
had left.

Every attempt is its own `NodeRuns` entry with an incrementing `Attempt`, and
`internal/engine/service.go` stops hardcoding `1`. That column has carried a
unique index on `(execution_id, node_id, attempt)` since V1, so a retried node
would have collided with itself the moment retries existed.

`waitBetweenTries` is declared alongside `maxTries` and defaults to one second.
Without it the first user to tick Retry on Fail against a rate-limited API sends
every attempt inside a millisecond. The wait is cancellable — a retry loop must
not outlive the run it belongs to.

### continueOnFail

One error item per input item, falling back to a single item when the node had
no input, as recommended: it is the only variant that keeps item counts and
paired-item lineage intact.

The input is deliberately **not** passed through. A downstream node has to be
able to tell a tolerated failure from a success, and identical items would make
that impossible — so each item carries an `$error` descriptor and nothing else.

### Validation

In the compiler, not the runner. Settings are static, so an out-of-range
`maxTries` is refused before activation rather than part-way through a run.
`MaxRetryAttempts` is 8 and `MaxRetryWaitMilliseconds` is 300,000; the runner
clamps rather than errors, because a document that reached it has already been
checked and a second error path would only be reachable if that stopped being
true.

### The importer

`continueOnFail`, `retryOnFail`, `maxTries` and `waitBetweenTries` are now
carried onto the canonical settings, and their "dropped" diagnostics from
FEAT-nbqye0 are gone. `alwaysOutputData`, `executeOnce` and `onError` keep
theirs, each with a reason specific to it rather than a shared sentence.

An out-of-range n8n budget is clamped rather than refused, so a workflow with
`maxTries: 9999` still imports — and `TestImportClampsAnOutOfRangeRetryBudget`
checks the clamped document still compiles, since a clamp that produced an
invalid document would achieve nothing.

### Two things the ticket did not anticipate

1. **No OpenAPI change was needed.** The ticket expected adding an attempt
   column to the node-run response; `Attempt` was already on `execution.NodeRun`
   and already serialized — it was simply always 1. Both generators produce no
   diff.

2. **A test from the previous ticket had to change, and that was the point.**
   `TestImportReportsEveryDroppedElement` asserted all seven error-handling
   fields were reported as dropped. Four of them are now carried, so it failed —
   exactly the signal FEAT-nbqye0 said to expect when this landed. It now asserts
   the three that remain, and a new test asserts the four that are carried.
