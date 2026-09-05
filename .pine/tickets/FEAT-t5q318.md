---
id: FEAT-t5q318
title: Add the Sticky Note node and a lossless unsupported capsule
status: todo
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:04:21Z"
updated: "2026-09-05T05:04:21Z"
---

## Scope

Sticky Note is the most widely deployed node in n8n. It draws a coloured rectangle on the canvas, has no ports and never executes. KilasFlow has no equivalent, so `byN8NType` misses it (internal/interop/n8n/n8n.go:160-167) and it becomes `kilasflow.unsupported`, whose `validateUnsupportedConfiguration` always returns an error (nodes/unsupported.go:48-59). That one substitution makes every real imported workflow permanently unactivatable, because almost every real workflow is annotated.

It fails three ways at once, not one. The placeholder declares `Inputs: mainInput()` and `Outputs: mainOutput()` (nodes/unsupported.go:32-33), so a sticky note with no connections also trips `workflow node %q requires an incoming "main" connection` and `workflow node %q is disconnected from the trigger` in `validateExecutableTopology` (internal/workflow/compiler.go:326-332, 378-386). A user who removes the sticky note to get past the first error still meets the other two on the next unsupported node.

The capsule the placeholder preserves is also lossy. `Import` stores only `{type, typeVersion, parameters}` (n8n.go:241-243) plus the same type and version as loose parameters. Everything else on the source node is dropped: `credentials`, `disabled`, `notes`, `webhookId`, and every error-handling field. So a node imported as unsupported and exported back to n8n returns stripped of its credential bindings and its settings — which defeats the stated purpose of the placeholder, that a node "came from n8n and belongs there".

Export additionally collapses multi-output nodes. `Export` populates `portIndex[node.ID]` at n8n.go:487, but the unsupported branch `continue`s at n8n.go:453 before reaching it. Every outgoing connection from a placeholder therefore falls to `index := 0` at n8n.go:512. Import had faithfully recorded the branch as `output1`, `output2` and so on through `outputPortName` (n8n.go:368-374); export silently rewires all of them onto n8n output 0. A Switch node round-trips as a Switch whose every branch now leaves the first output.

## Acceptance criteria

- [ ] A registered annotation node exists with zero input and zero output ports, never executes, and never fails validation.
- [ ] A workflow whose only unmapped node is a sticky note imports, compiles and activates.
- [ ] The compiler accepts a zero-port node that is connected to nothing, without a disconnected-from-trigger or missing-input error.
- [ ] The unsupported capsule preserves `credentials`, `disabled`, `notes`, `webhookId` and every error-handling field from the source node, and an import-then-export round trip returns them all.
- [ ] The placeholder declares port arity matching the original node's connections, so an imported multi-output node keeps its branches distinguishable.
- [ ] Exporting a placeholder that had three outputs writes three n8n output slots with the right targets in each, not three targets on slot zero.
- [ ] The unsupported node still refuses to compile, and its message still names the original n8n type.

## Implementation Plan

Two independent halves; do the sticky note first because it unblocks the corpus immediately.

Register an annotation node in `nodes/core.go`'s `RegisterAll` with empty `Inputs` and empty `Outputs`, a content parameter, and dimension and colour parameters so the canvas can render it. Zero ports needs three checks relaxed in `internal/workflow/compiler.go`: `producesItems` already returns false for an empty port list, so it is correctly not counted as a trigger root; the disconnected-from-trigger sweep at the bottom of `validateExecutableTopology` must exempt a node with no ports at all; and the executor registry test that every `ExecutorID` resolves must tolerate a definition that declares none, or the node needs a no-op executor. Prefer a no-op executor over widening the registry contract — one function that returns an empty output is cheaper than a special case in the compiler and in the runner. Add the `n8n-nodes-base.stickyNote` mapping in both directions, and carry the content and geometry through so a round trip does not lose the annotation.

Then the capsule. Widen the `original` payload in `n8n.go:241-243` to the whole source `Node` rather than three fields, and add the fields the `n8n.Node` struct does not yet read — `webhookId` and the error-handling set — so the capsule can carry them. Store it as a structured object rather than the current marshalled-then-stringified `string`; the double encoding exists for no reason and makes the value unreadable in the editor.

Port arity is the interesting part. The placeholder needs its declared ports to match the edges the import created, or the compiler rejects the connection with `ErrorUnknownPort` before the always-fails validator is even reached. Since the registry is keyed by `{type, version}` and immutable per process, a single placeholder definition cannot have a variable port count. Two options: register a small family of placeholder versions with one, two, four and eight ports and pick by observed arity, or make port arity a compiler-visible property computed from the node's own parameters. Recommendation: the family. It is a contained change, it needs no new compiler capability, and computed ports are already scoped as a decision for the node-metadata phase — deciding it here would pre-empt that work.

Finally, move `portIndex[node.ID] = outputIndexesFor(node.Type)` above the unsupported `continue` in `Export`, and make `outputPortsFor` return the placeholder's real port names rather than `["main"]` so the inverse mapping holds. The round-trip fixture that already asserts `If → [[Gold], [Notify]]` survives export is the template for the regression test: assert the same for a three-output placeholder.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-9.
- `nodes/unsupported.go` — the placeholder definition, its ports, and `validateUnsupportedConfiguration`.
- `internal/interop/n8n/n8n.go` — the capsule construction in `Import`, the unsupported branch of `Export`, `portIndex`, `outputPortName`, `outputPortsFor`, `outputIndexesFor`.
- `internal/workflow/compiler.go` — `validateExecutableTopology`, `producesItems`, the port and kind checks in `Compile`.
- `nodes/core.go` — `RegisterAll`; `nodes/executors.go` — `RegisterExecutors`.
- `.pine/tickets/FEAT-chxkvq.md` — the original unsupported-placeholder contract this ticket extends without weakening.
