---
id: BUG-4ch186
title: alwaysOutputData runs a node that got no input, which loops a Loop Over Items forever
status: testing
priority: high
labels:
    - engine
    - n8n
    - parity
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Found by the 2026-09-25 Code-node self-test (importing real n8n templates). A node with `alwaysOutputData: true` runs even when its parent sent it nothing, and emits an empty `{}` item. In n8n a node whose parent emitted no items is not executed at all; `alwaysOutputData` only applies to a node that did run and produced no output. On a Loop Over Items (`splitInBatches`) back edge the stray `{}` item restarts the loop, so the execution runs ~6,000 node runs until the 2-minute execution timeout.

# Steps to Reproduce

1. Manual Trigger → Code (`return []`) → Code with `alwaysOutputData: true` (`return [{json:{ran:true}}]`): the second Code node runs and outputs `{ran:true}`. In n8n it is skipped.
2. Template 2896 (reduced): a Loop whose body contains a node with `alwaysOutputData` never finishes; the same workflow without the flag does.

Repros (scratchpad of the self-test): wf/12b-empty-alwaysOutputData.json, wf/12-loop-alwaysOutputData.json, wf/09-loop.json, wf/11-loop-2896-reduced.json.

# Expected

A node that receives no items is not run, whatever its alwaysOutputData; the loop ends.

# Actual

The node runs and emits `{}`; the loop never ends.

# Acceptance Criteria
- [x] A node that gets no input items is not run even with alwaysOutputData (match n8n's exact rule, including nodes with several inputs and the Merge node).
- [x] alwaysOutputData still emits one empty item when the node ran and returned nothing.
- [x] The template-2896 shape finishes.

# Notes

## n8n's rule (read from n8n's workflow-execute.ts on master, 2026-09-25; written in our own words)

- **Scheduling a child.** When a node finishes, n8n walks each of its outputs and, for every edge on that output, queues the node at the other end only if that output carries at least one item (v1 execution order; the legacy v0 order also queued a child on an empty output when it hung off a second input). An output with no items queues nothing, so the node below it never runs — its own settings are not consulted.
- **Always Output Data.** Applied after a node ran successfully: if the node's first output has no first item, n8n replaces the first output with a single empty item (paired with every input item) and leaves the other outputs as returned. It never makes a node run; it only changes what a node that ran hands on.
- **Several inputs / Merge.** A node with more than one item input collects what each input received in a waiting entry. It goes on the stack at once when every input has received items. Otherwise it is looked at once the stack is empty: it runs if at least one input received items, unless its type declares required inputs. Merge v3 declares both inputs required in `chooseBranch` mode and one in every other mode. A node none of whose inputs received items is dropped without running.
- Also seen: a node whose input is empty at run time returns no data without running (a guard for the v0 order), and `alwaysOutputData` is then not what brings it back, since in v1 such a node is never queued in the first place.

## Plan

1. RED tests in `internal/engine/always_output_test.go`: linear case, loop case (must terminate), node that ran and returned nothing, two-input node nothing reached, and a resumed run with an empty sibling branch.
2. In `runState.push`, stop exempting an `alwaysOutputData` target from the empty-delivery skip.
3. In `Resume`, recognise a pending entry that carries no items for a fed node as a skipped delivery.
4. CHANGELOG, migration guide sentence, report.

## Decisions

- The scheduler rule is now "a node reached by an empty port is recorded as skipped" with no exception; `withEmptyItem` after a real run is unchanged. The existing `TestAlwaysOutputDataKeepsTheBranchAliveWithOneEmptyItem` encodes the right behaviour (its flagged node ran and returned nothing) and still passes unchanged.
- Multi-input nodes were already gated correctly by `delivered` (at least one input with items); the new test pins that `alwaysOutputData` does not change it.
- Resume path: a checkpoint's pending stack does not record the skipped flag, so a branch that received nothing was run on no input after a Wait resumed — same rule, different door. The flag is derived from the pending input (no items on any item port of a node that has an incoming item edge) rather than added to the checkpoint, so checkpoints already written are covered and the format does not change.
- Out of scope, filed: BUG-hnvn3r (n8n looks only at the first output's first item and pairs the empty item with the inputs; KilasFlow looks at every port), BUG-e8ytyq (Merge `chooseBranch` requires both inputs in n8n; KilasFlow has no required-inputs notion).

## Progress (2026-09-25)

- Fixed in `internal/engine/runner.go` (`push`, `Resume`, new `deliveredNothing`). New tests green; full engine/nodes/interop suites green under `-race`.
- End-to-end check (temporary test, not committed) importing the four repro workflows and running them with the goja Code runtime: before the fix 12-loop and 11-loop-2896 hit a 15 s deadline after ~32,000–36,000 node runs and 12b ran AfterEmpty/SetAfter; after the fix all four finish in six runs or fewer, 12/11 identical to 09 (the same loop without the flag), and 12b records AfterEmpty and SetAfter as skipped.
