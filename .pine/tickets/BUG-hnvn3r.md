---
id: BUG-hnvn3r
title: Always Output Data looks at every output instead of only the first, as n8n does
status: done
priority: medium
labels:
    - engine
    - n8n
    - parity
created: "2026-09-25T10:10:33Z"
updated: "2026-10-08T15:48:35Z"
---

# Description

Found while settling BUG-4ch186. In n8n, Always Output Data looks only at the node's first output: if that output has no first item, n8n puts one empty item on it (paired with every input item) and leaves the other outputs as the node returned them. KilasFlow's `withEmptyItem` (internal/engine/runner.go) instead leaves the output alone whenever any port carries items, and pairs the empty item with nothing.

The two differ only for a node with several outputs whose first output is empty while another is not: an IF that sent every item to `false`, a Switch that routed nothing to output 0, or a node with `onError: continueErrorOutput` whose items all failed. n8n then also runs the branch on output 0 with one empty item; KilasFlow does not.

# Steps to Reproduce

1. Manual Trigger → IF (condition false for the item, `alwaysOutputData: true`) → a node on `true` and a node on `false`.
2. Run.

# Expected

As n8n: the `false` branch gets the item and the `true` branch gets one empty item.

# Actual

Only the `false` branch runs.

# Acceptance Criteria
- [x] The empty item is added when the first output has no items, whatever the other outputs carry, and goes on the first output only.
- [x] The empty item pairs with the node's input items, as n8n's does.
- [x] Confirm the rule against n8n (source as a behaviour reference, or black-box) before changing it, and record it in your own words.

## n8n's rule (read on 2026-10-08 from n8n master on GitHub, workflow-execute.ts; behaviour only, written in our own words)

- Always Output Data is applied once, after a node ran and after its failed items were split off to the error output. If the first output has no first item, n8n puts exactly one empty item on the first output and leaves every other output as the node returned it.
- The empty item's lineage lists every item of every input the node was given.
- A node that threw as a whole and continues on error does not go through this step. That path passes the input through on the first output, so the first output is not empty anyway.

## Fix

- **Root cause.** `withEmptyItem` (internal/engine/runner.go) did nothing whenever any output carried items. It was also only called on the plain-success path. The per-item path (`resolveItems`), which every tolerating single-input node takes, never applied Always Output Data. Neither did the two whole-batch tolerated paths. So a node whose items all went to its error output never got the empty item.
- **Change.**
  - `withEmptyItem` is now a `runState` method. It reads the setting, looks only at `output[0]`, and puts one empty item there with its lineage set.
  - Lineage: a node with one input item gets the empty item paired with that item (via `lineageOf`). Several input items have no single item to name. KilasFlow's lineage holds one origin where n8n's holds a list, so the item is marked lost there. That turns an ambiguous `.item` read into a refusal.
  - It is called after the success output is formed, at the end of a per-item run, after `assembleOutcomes`, and after `toleratedOutput`. The last one is KilasFlow's version of a node catching its own failures. In n8n that path does get Always Output Data, and for `continueRegularOutput` the call does nothing because the first output carries the error items.
- **Proving tests** (internal/engine/always_output_test.go):
  - `TestAlwaysOutputDataLooksOnlyAtTheFirstOutput`: an IF that sends its item to `false` with alwaysOutputData. `false` runs on the item, `true` runs on one empty item, and that item descends from Start's item. Red before the fix: `true` did not run.
  - `TestAlwaysOutputDataAppliesWhenEveryItemWentToTheErrorOutput`: under `continueErrorOutput` every item fails. The error branch gets the failure, and the success branch runs on one empty item paired with the input. Red before the fix.
  - The existing Always Output Data tests (BUG-4ch186) still pass unchanged.
- `go vet` and `go test` pass on ./internal/engine ./nodes.
