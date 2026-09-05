---
id: FEAT-jwhdsy
title: Reach parity on the data-shaping node family
status: todo
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-9knk67
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:01:10Z"
updated: "2026-09-05T05:01:10Z"
---

## Scope

KilasFlow's whole data-shaping surface is one node. `setNode()` in `nodes/core.go` declares a single required parameter, `assignments`, of kind `node.PropertyKeyValue`, and `executeSet` in `nodes/executors.go` copies every incoming item and writes each assignment at the top level of `item.JSON`. There is no type on an assignment, no dot notation, no raw-JSON mode, no include/exclude of incoming fields, no duplicate-item option, and no ordering — `assignments` is read as `map[string]any`, so the order the user typed is lost on the first round trip and two assignments to the same name are impossible. There is no Aggregate, Sort, Split Out, Summarize or Remove Duplicates node at all; each of those imports as `kilasflow.unsupported`, whose validator always fails, so one Sort blocks activation of the whole workflow.

n8n's Set is much larger, and the gap is silent rather than reported. `setToKilas` in `internal/interop/n8n/parameters.go` reads both the v3 `assignments.assignments` array and the legacy `values.*` groups, then throws away each entry's declared type and keeps only `{name: value}`. It reports exactly one lossy case — `includeOtherFields == false` — and says nothing about `mode: "raw"`, `duplicateItem`, `include` (all / none / selected / except), `includeFields`, `excludeFields`, or the options `dotNotation`, `ignoreConversionErrors`, `includeBinary` and `stripBinary`, all of which are real parameters of `Set` v3 in the reference checkout at `packages/nodes-base/nodes/Set/v2/SetV2.node.ts` (n8n's directory is `v2`; the node's own `version` list covers 3.x). An imported Set that replaced the item now silently augments it, and the diagnostic that would have said so is only emitted for one of the four `include` values.

This ticket brings Set to n8n's v3 semantics and adds the five transform nodes that the p0 corpus actually uses. It is measured against that corpus in `imported / activatable / executable` counts.

## Acceptance criteria

- [ ] Set carries typed, ordered assignments — each `{name, type, value}` with n8n's types (string, number, boolean, array, object) — and converts values to the declared type, honouring `ignoreConversionErrors`.
- [ ] Set supports `mode: manual` and `mode: raw` (a whole-object JSON body), the `include` choice of all / none / selected / except with `includeFields` and `excludeFields`, `duplicateItem` with `duplicateCount`, and `dotNotation` defaulting to on as n8n does.
- [ ] Set passes binary through by default and strips it on request, over the binary store from p3-8.
- [ ] Aggregate, Sort, Split Out, Summarize and Remove Duplicates are registered nodes with n8n's parameter surface, and each has table tests covering the aggregation and comparison rules rather than a single happy path.
- [ ] `internal/interop/n8n` maps `n8n-nodes-base.set`, `.aggregate`, `.sort`, `.splitOut`, `.summarize` and `.removeDuplicates` in both directions, `SupportedMappings()` lists them, and every unmapped option produces a named import diagnostic instead of being dropped.
- [ ] A workflow saved before this ticket, whose Set carries the old `map[string]any` assignments, still loads, still runs and produces the same items.
- [ ] The p0 corpus counts improve and the new figure is recorded in the ticket's work evidence.

## Implementation Plan

Change the Set document shape first, because everything else in this ticket is additive and this one is not. Today `executeSet` reads `node.Parameters["assignments"].(map[string]any)`; the target is an ordered array of `{name, type, value}` entries, which needs p2-2's `fixedCollection` property kind and its `typeOptions.multipleValues` to be renderable in the editor. Accept both shapes on read — a map keeps working, an array is the new form — and always write the array. Do not migrate stored documents in place: V1 workflows exist, and a reader that handles both costs a dozen lines while a migration costs a whole class of failure. Touch `nodes/core.go` (`setNode`), `nodes/executors.go` (`executeSet`, `validateSetConfiguration`) and `internal/interop/n8n/parameters.go` (`setToKilas`, `setToN8N`) together, and note that `Definition` is serialized directly as the `/api/v1/node-types` payload by `internal/api/handlers/nodes.go`, so a new property kind is an OpenAPI change: `pnpm generate:api` must be rerun or `pnpm generate:api:check` fails in CI.

Dot notation is the trap. n8n's `dotNotation` defaults to true, so an n8n assignment named `user.email` writes a nested object, while KilasFlow's `executeSet` writes a literal key containing a dot. Turning dot notation on changes the behaviour of every already-imported Set that has a dotted field name. Ship it on by default to match n8n, expose the option, and say so in the import diagnostics — the alternative, defaulting it off, makes every imported workflow subtly wrong in a way nobody notices until the HTTP node sends the wrong body.

Then the five transform nodes, in a new `nodes/transform.go` with executors registered in `RegisterExecutors`. Split Out and Aggregate are inverses and should be written together, so the round trip is testable in one fixture. Sort has three n8n modes — simple field ordering, random, and a JavaScript comparator; implement the first two and refuse the third with the diagnostic p4-5 defines, so the Code decision stays in exactly one ticket. Summarize needs n8n's aggregation set (count, count unique, sum, min, max, average, concatenate, append) and split-by fields. Remove Duplicates has two operations: removing duplicates within the incoming items, which is local and easy, and removing items seen in previous executions, which needs durable per-workflow state that this repository does not have — implement the first, and refuse the second with a diagnostic naming it, leaving the durable form to the PostgreSQL tier in p6.

Every one of these nodes must return exactly one item stream per declared output port even when it is empty, or `Runner.Run` in `internal/engine/runner.go` aborts the execution on its output-arity check.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (data shaping); p2-2 property kinds; p3-8 binary data storage.
- PRD `gflow-prd-v1.md` §22 Workflow Data Model, §23 Node Registry, §24 Native V1 Nodes.
- Verified in this repository: `nodes/core.go` (`setNode`), `nodes/executors.go` (`executeSet`, `validateSetConfiguration`), `internal/interop/n8n/parameters.go` (`setToKilas`, `setToN8N`), `internal/interop/n8n/n8n.go` (`mappings`), `internal/node/registry.go` (`PropertyKind` has six values), `internal/api/handlers/nodes.go`, `nodes/unsupported.go`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/Set/v2/SetV2.node.ts` and `.../nodes/Set/test/`. The transform nodes ship from `nodes/Transform/{Aggregate,Sort,SplitOut,Summarize,Limit,RemoveDuplicates}` per that package's `package.json`; `n8n-nodes-base.splitOut` is confirmed as a type string in the checkout, and the rest must be confirmed against the widened checkout from p0-1 before the mapping table is written.
