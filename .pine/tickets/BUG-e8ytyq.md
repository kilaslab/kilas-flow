---
id: BUG-e8ytyq
title: Merge in chooseBranch mode runs on one input where n8n requires both
status: done
priority: medium
labels:
    - engine
    - n8n
    - parity
created: "2026-09-25T10:10:33Z"
updated: "2026-10-08T15:43:59Z"
---

# Description

Found while settling BUG-4ch186. n8n's Merge node (v3) declares which of its inputs must have received items before it may run: in `chooseBranch` mode both inputs are required, in every other mode one is enough. KilasFlow's scheduler runs a node with several item inputs as soon as any one of them delivered (`delivered` in internal/engine/runner.go), so a Merge in `chooseBranch` mode runs on one side only, where n8n does not run it at all.

With `alwaysOutputData` on that Merge the difference grows: n8n emits nothing, KilasFlow hands on an item.

# Steps to Reproduce

1. Manual Trigger → IF; `true` → Merge input 1, `false` → Merge input 2; Merge mode `chooseBranch`.
2. Run with an item that goes only to `true`.

# Expected

As n8n: the Merge does not run (both inputs are required in this mode).

# Actual

The Merge runs with only input 1.

# Acceptance Criteria
- [x] A node definition can say which item inputs must have delivered before it runs (fixed, or depending on a parameter as Merge's mode does), and the scheduler honours it.
- [x] Merge in `chooseBranch` mode needs both inputs; other modes need one.
- [x] Confirm against n8n before changing it, and record the rule in your own words.

## n8n's rule (read on 2026-10-08 from n8n master on GitHub — the Merge v3 description and workflow-execute.ts; behaviour only, written in our own words)

- A node type may declare required inputs, either as a list of input positions that must have received items, or as a minimum number of inputs that must have. The declaration may be an expression over the node's parameters.
- It matters only for a node fed by several item inputs that is waiting with some of them still empty once nothing else is left to run (v1 order). Such a node runs on what it has if at least one input received items, unless its declaration is not met; then it does not run at all.
- Merge v3 declares inputs 1 and 2 (positions 0 and 1) required in `chooseBranch` mode and a minimum of one input in every other mode.
- In v1 order an output with no items never reaches a waiting node, so "received" means "received at least one item".

## Fix

- **Root cause.** The fallback in `runState.next` started a multi-input node through `delivered` (internal/engine/runner.go), which accepted any one item input that received items. Nothing in a node definition could say an input was required, so a `chooseBranch` Merge ran on one side.
- **Change.**
  - `workflow.NodeDefinition` and `node.Definition` gain `RequiredInputsFor(parameters, typeVersion) []string`. It names the item inputs, by port name, that must have received items. A fixed rule ignores its arguments, and a mode-dependent one reads them. Nil means any one input is enough. It is plumbed through the registry's `Lookup` and `cloneNodeDefinition`.
  - `delivered` now records which ports received items and, when at least one did, asks `requiredInputsReceived`. That function checks every port the definition names.
  - Merge's definition sets `RequiredInputsFor: mergeRequiredInputs` (nodes/flow.go): `input1` and `input2` in `chooseBranch` mode, nothing otherwise.
  - A Merge that is not run is recorded as skipped by the existing `recordSkips`. Always Output Data then has nothing to act on, so nothing goes on, as in n8n.
- **Proving tests** (internal/engine/required_inputs_test.go):
  - `TestMergeChooseBranchDoesNotRunOnOneInput`: IF into a `chooseBranch` Merge with alwaysOutputData. Only the `false` input receives items, so Merge is skipped and the node after it does not run. Red before the fix.
  - `TestMergeInOtherModesRunsOnOneInput`: an `append` Merge still runs on one input.
  - `TestMergeChooseBranchRunsWhenBothInputsDelivered`: both inputs arrive and input 2 goes on.
  - `TestRequiredInputsAreReadFromTheDefinition`: a test node with a fixed required input `input2` does not run when only `input1` received items, and does run on `input2` alone. Red before the fix.
- `go vet` and `go test` pass on ./internal/engine ./internal/node ./internal/workflow ./nodes.
