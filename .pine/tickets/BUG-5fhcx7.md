---
id: BUG-5fhcx7
title: Batch-shaped nodes (Aggregate, Summarize, Limit, Remove Duplicates …) split into one-item calls under continue-on-fail
status: done
priority: medium
labels:
    - engine
    - parity
created: "2026-09-24T13:16:23Z"
updated: "2026-10-08T16:30:00Z"
---

# Description

Under continue-on-fail, the engine's `perItemTolerance` splits a node's batch
into one-item calls unless the node's definition is `WholeBatch`. That is
right for per-item nodes, but a node whose answer depends on the whole batch
computes something different on one item at a time: Aggregate, Summarize,
Limit, Remove Duplicates, Item Lists-style nodes and the like. Sort had the
same bug and was made `WholeBatch` in FEAT-mammrz; the Code node in P4.

# Acceptance Criteria
- [x] Every batch-shaped native node is `WholeBatch`; a test enumerates them so
      a new batch node cannot forget it.
- [x] Under continue-on-fail each such node gives the same output as without
      it when nothing fails.

## Fix (2026-10-08)

The following are now `WholeBatch`, so the runner never splits them into one-item calls to tolerate a failure:

- Aggregate, Summarize and Remove Duplicates (nodes/transform.go) and Limit (nodes/flow.go).
- Respond to Webhook (nodes/webhook.go). It answers once, and split per item, "All Incoming Items" answered with one item.
- The Go Code node (nodes/code.go) and Execute Workflow (nodes/subworkflow.go). Their default modes ("Run once for all items", "All items") hand the whole batch to one call.

Sort, the JS Code node and Foreign Code were already `WholeBatch`.

The Go Code node's "Run once for each item" mode and Execute Workflow's "Each item" mode loop over the items themselves. When `request.TolerateItemFailures` is set, they continue past a failed item and return `engine.ItemOutcomes`, as the JS Code node does. The runner then routes the failed items, so per-item tolerance in those modes is unchanged. Without tolerance, the first failure still fails the node.

Enumeration: `TestEveryBatchShapedNodeIsWholeBatch` (nodes/batch_shape_test.go) lists every registered native node the runner could split (one main input, not a loop entry). Each one must be in `batchShapedNodes`, which also requires `WholeBatch`, or in `perItemNodes`. A new single-input node fails the test until its author classifies it.

Same output: `TestBatchShapedNodesGiveTheSameAnswerUnderContinueOnFail` (internal/engine/batch_nodes_tolerance_test.go) runs the real nodes on four items with no `onError`, with `continueRegularOutput` and with `continueErrorOutput`, and compares the outputs and the published response. It covers Aggregate (individual fields, all item data), Summarize (split by), Limit (first, last), Remove Duplicates (selected fields), Respond (all incoming items) and Execute Workflow (all items, against a called workflow that counts its batch). `TestPerItemModesOfWholeBatchNodesReportEachItem` covers the per-item modes. All three failed first.

docs/src/content/docs/concepts/execution-model.md says which nodes run as one call under `continueOnFail`.

Left as per-item, worth a follow-up look:
- Wait: the runner's per-item path checkpoints a suspension per item (`resolveItems`), and recent Wait error-output work depends on it.
- The SQL nodes' `queryBatching` single/transaction options.

`go test ./internal/engine/ ./nodes/ ./internal/interop/... ./internal/workflow/ ./internal/node/ ./internal/skills/` and `go vet ./internal/engine/ ./nodes/` pass.
