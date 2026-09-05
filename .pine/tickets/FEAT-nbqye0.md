---
id: FEAT-nbqye0
title: Report every field the n8n importer drops
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:06:18Z"
updated: "2026-09-05T05:06:18Z"
---

## Scope

`FEAT-chxkvq` shipped on the principle that an unsupported element is named rather than silently applied. The node-level mapping honours it; the document and node metadata do not. `n8n.Document` declares `Settings`, `PinData` and `Meta` (internal/interop/n8n/n8n.go:34-41) and `Import` reads none of them — the canonical document is built with `Settings: map[string]any{}` (n8n.go:292) and `pinData` and `meta` are discarded without a word. `staticData` and node-level `webhookId` are not even fields on the structs, so they are dropped before anything could report them. `Node.Notes` is parsed (n8n.go:53) and never used. Every n8n error-handling field — `continueOnFail`, `retryOnFail`, `maxTries`, `waitBetweenTries`, `alwaysOutputData`, `executeOnce`, `onError` — is absent from `n8n.Node` entirely.

The user-visible consequence: a workflow whose author set "Continue on Fail" on the node that calls a flaky API imports as a workflow that aborts on the first failure, and the import response says nothing. `Node.Disabled` is the one field that is handled correctly (n8n.go:270-275) and shows exactly what the rest should look like.

There is also an export bug on the webhook mapping. `webhookToKilas` writes KilasFlow's own `responseMode` value `"immediate"` as the default (parameters.go:440-447; the constant is `ResponseModeImmediate` in nodes/webhook.go:39). `webhookToN8N` then passes it straight back out: `defaultString(stringParameter(node.Parameters, "responseMode"), "onReceived")` (parameters.go:475) only substitutes the fallback when the value is *empty*, and `"immediate"` is not empty. n8n's webhook `responseMode` enum is `onReceived | lastNode | responseNode`, so every exported webhook carries a value n8n does not accept.

Finally, the diagnostic channel itself is too narrow. `Unsupported` (n8n.go:67-77) has `NodeName`, `NodeID`, `Type`, `TypeVersion` and `Reason` — no severity and no field name, so "this node cannot run" and "this annotation was not carried" arrive looking identical. The type is generated into both clients (`web/src/lib/api/generated/models/unsupported.ts`) and there is no UI consuming it yet, so widening it now costs nothing and later costs a client migration.

## Acceptance criteria

- [x] Importing a workflow that carries `settings`, `pinData`, `meta` or `staticData` produces one diagnostic per dropped element, naming it.
- [x] Importing a node that carries `notes`, `webhookId`, or any of n8n's error-handling fields produces a diagnostic naming the node and the field.
- [x] A diagnostic distinguishes severity — a node that cannot run, versus a field that was carried lossily, versus one that was dropped harmlessly — and names the field where there is one.
- [x] An exported webhook never carries `responseMode: "immediate"`; the KilasFlow immediate mode maps to n8n's `onReceived`, and the reverse mapping is its exact inverse.
- [x] A round-trip fixture asserts that the exported document validates against n8n's documented enums for every value the exporter writes.
- [x] The import API response carries the widened diagnostics and the generated clients are regenerated from it.
- [x] Nothing the importer chooses not to carry can reach the canonical document without a corresponding diagnostic, proven by a fixture that feeds a maximally annotated n8n workflow through import and asserts the diagnostic count.

## Implementation Plan

Widen the diagnostic type first, because every other change reports through it. Add a severity and a `Field` to `Unsupported` — or rename it, since it will no longer only describe unsupported things; `ImportIssue` reads better and the rename is free while no UI consumes it. Severity should be a small closed set: `blocking` (the workflow cannot run), `lossy` (carried differently), `dropped` (not carried). `Lossy` on the export side should get the same treatment so the two directions stay symmetrical.

Then add the missing fields to the structs so they can be seen at all: `StaticData` on `Document`, and `WebhookID` plus the error-handling set on `Node`. Reading a field and reporting it is the whole change for most of them; only the error-handling fields have a canonical destination, and only once the runner honours them — until then they are `dropped`, not `lossy`, and the diagnostic must say so rather than implying they were applied.

`pinData` deserves a decision rather than a blanket drop. It is n8n's pinned test data for a node, and KilasFlow has no equivalent concept. Two options: drop it with a diagnostic, or carry it into node settings under a reserved key so a later feature can use it. Recommendation: drop with a diagnostic. Carrying data nothing reads creates a second silent-drop problem one release later, and the diagnostic is what the user actually needs.

The webhook export bug is a two-line fix but do it as its own change with its own fixture: map KilasFlow's `immediate` to `onReceived` explicitly in `webhookToN8N` rather than relying on `defaultString`, and add an assertion covering every enum value the exporter writes. The same class of bug is likely elsewhere — `respondToN8N` writes `respondWith: "text"` unconditionally (parameters.go:497) regardless of what the KilasFlow node holds — so sweep the exporters for values invented rather than derived while the fixture is being written.

The trap is that this is an OpenAPI change: `ImportedWorkflowResource` and `ExportedWorkflowResource` (internal/api/handlers/interop.go:20-30) carry these types directly, so `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/` must run or the drift checks fail. There is no import screen yet — nothing in `web/src` outside the generated client references interop — so this ticket delivers the API surface, and the screen that renders it is separate work.

## References

- Roadmap plan, p1 section, entry V2-p1-12: `.pine/roadmap.md`.
- `internal/interop/n8n/n8n.go` — `Document`, `Node`, `Unsupported`, `Lossy`, `Import`, the disabled-node diagnostic that shows the pattern.
- `internal/interop/n8n/parameters.go` — `webhookToKilas`, `webhookToN8N`, `respondToN8N`, `defaultString`.
- `nodes/webhook.go` — `ResponseModeImmediate` and the webhook node's `responseMode` options.
- `internal/api/handlers/interop.go` — `Import`, `Export`, `ImportedWorkflowResource`, `ExportedWorkflowResource`.
- `.pine/tickets/FEAT-chxkvq.md` — the "named, not dropped quietly" contract this ticket completes.

## Outcome

### The diagnostic type

`Unsupported` became `ImportIssue` and `Lossy` became `ExportIssue`, with
`Severity` and `Field` on both. The rename was free exactly as the ticket
predicted — nothing outside the generated client referenced either name — and
it was worth doing: a `notes` field is not "unsupported", it is simply not
carried, and the old name would have made every new diagnostic read wrongly.
The old names survive as type aliases so the thirty-odd converter signatures in
the mapping table did not all have to change in the same commit.

Severity is a closed three-value set. Rather than restate it at each of the ~30
converter sites, `withDefaultSeverity` fills in `lossy` — which is what a
converter issue always is, since the node still imported — and the two
severities that are *not* lossy are set explicitly where raised: `blocking` for
a node type with no equivalent, `dropped` for a field nothing reads.

### What is now reported

Workflow level: `settings`, `pinData`, `meta` and `staticData`. The last was not
even a field on the struct, so it was dropped before anything could report it.

Node level: `notes` (parsed since V1 and never used), `webhookId`, `disabled`,
and all seven error-handling fields.

The error-handling set is `dropped`, not `lossy`, and that distinction is the
point: the runner does not honour `continueOnFail` or `retryOnFail` yet, so
calling them lossy would imply a retry policy was applied in some reduced form
when it was ignored entirely. When FEAT-a6yg3n lands, they become carried and
these diagnostics disappear — which is the signal that ticket wants.

`pinData` is dropped with a diagnostic rather than parked under a reserved key,
as recommended: carrying data nothing reads would create a second silent-drop
problem one release later.

### One thing the ticket did not anticipate

Node-level reporting had to run **only for mapped nodes**. An unsupported node
keeps the whole source node in its capsule and hands it back on export, so
reporting its `notes` as dropped would claim something was lost that was
preserved. `TestAnUnsupportedNodeDoesNotReportItsFieldsAsDropped` pins it.

### The export enum bug

`webhookToN8N` passed KilasFlow's `responseMode` straight through, so a
round-tripped webhook carried `immediate` — not one of n8n's
`onReceived`/`lastNode`/`responseNode`. `defaultString` hid it, because it only
substitutes when the value is empty and "immediate" is not empty. It now
translates explicitly and `TestWebhookResponseModeRoundTripsExactly` checks all
three values survive a round trip unchanged.

The sweep for other invented values found one more: `respondToN8N` wrote
`respondWith: "text"` unconditionally, so a node configured to answer with JSON
came back as text. It is now derived from the body. `sqlToN8N`'s `executeQuery`
turned out to be legitimately derived — it is n8n's only raw-statement
operation, and a KilasFlow operation that does not map already reports lossy.

`TestExportWritesOnlyValidN8NEnumValues` checks every value the exporter writes
against n8n's documented enums, which is the assertion the webhook bug escaped.

### API

`ImportIssue` and `ExportIssue` are published with `severity` as a real OpenAPI
enum, so the generated clients get a union type rather than a bare string. Both
were regenerated; the drift checks pass.

### Also fixed

The licence guardrail hard-failed on a tracked file deleted but not yet staged,
which is exactly what a rename looks like mid-edit. It now skips a tracked path
that is gone from the working tree instead of turning an ordinary state into a
confusing licence error.
