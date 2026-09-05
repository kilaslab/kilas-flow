---
id: FEAT-fw0m2q
title: Allow a workflow to carry multiple trigger roots
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:00:37Z"
updated: "2026-09-05T05:00:37Z"
---

## Scope

`validateExecutableTopology` in `internal/workflow/compiler.go` collects every registered node that has no declared inputs and emits at least one `main` output, and then refuses the document unless there is exactly one: `if len(roots) != 1` (compiler.go:336) raises `workflow graph must contain exactly one trigger root`. Reachability is then computed from `roots[0]` alone (compiler.go:344-345), so a second trigger would additionally drag its whole subtree into `workflow node %q is disconnected from the trigger`.

Real n8n workflows routinely carry more than one trigger. A webhook for live traffic plus a schedule trigger for a nightly catch-up is the standard shape; a manual trigger left beside a webhook so the author can test by hand is even more common. Over half of the templates in the import corpus fail this rule before their node types are even considered, so this single condition blocks more imports than the entire missing node catalogue.

The rule was not arbitrary. `engine.Request` carries exactly one `Input` item (internal/engine/runner.go:71-84) and `Service.run` builds it from `record.Input` (internal/engine/service.go:298-303), so the runtime today genuinely has one entry point. Allowing several roots in the compiler without deciding what happens at run time would produce documents that save and activate and then execute the wrong thing: every root's executor runs, each seeded with the same trigger item, and a schedule trigger would fire on a webhook delivery. The execution record already names which trigger started the run (`execution.Trigger`, `internal/execution/records.go:21-28`) and the webhook binding names the exact `NodeID` (`repository.WebhookBinding.NodeID`), so the information needed to start from one specific root is present and unused.

## Acceptance criteria

- [x] A document containing a webhook trigger and a schedule trigger compiles, saves and activates without a topology error.
- [x] Every node must still be reachable from at least one trigger root; a node reachable from none is still rejected as disconnected, with the same error code.
- [x] A document with no trigger root at all is still rejected.
- [x] An execution started by one trigger runs that trigger and the nodes downstream of it; the other trigger's executor is not invoked and its exclusive downstream nodes do not run.
- [x] A node downstream of both triggers runs once per execution, receiving items only from the root that started the run.
- [x] The execution trace names the trigger node the run started from, and the API exposes it.

## Implementation Plan

Split the change in two: the compiler stops refusing, and the runner learns which root to start from. Do not land the first without the second.

In `internal/workflow/compiler.go`, replace `len(roots) != 1` with `len(roots) == 0`, and seed the reachability queue with every root rather than `roots[0]`. The attachment-provider back-walk below it (the loop over non-`main` edges) needs no change. Keep the "trigger must not have incoming connections" check exactly as it is.

In the engine, add the entry point to `engine.Request` — a `TriggerNodeID string` that names the root this execution starts from, empty meaning "every root", which preserves today's behaviour for a single-root graph and for a manual run of a graph that has only a manual trigger. `Runner.Run` then computes the set of nodes reachable from the named root over `main` edges plus their attachment providers, and treats every other node as not present for this execution. Reuse the branch-pruning machinery rather than writing a second traversal: a node outside the reachable set is skipped by the same rule and appears in the trace the same way.

Fill `TriggerNodeID` at the two call sites that know it. `internal/webhook/webhook.go` resolves a `repository.WebhookBinding` that already carries `NodeID`; thread it through `Runner.QueueWebhook` into the execution record so `Service.run` can read it back. The scheduler does the same from `scheduleModel.NodeID` (internal/repository/models.go:44-57). A manual run leaves it empty unless the caller names a node, which is what makes "test this webhook by hand" work in a graph that also has a schedule.

The open decision is what a manual run of a multi-trigger workflow does when the caller names nothing. Two options: run every root, or refuse and require the caller to name one. Recommendation: run every root. It matches what a manual run means today, it keeps the API unchanged for the single-root case, and refusing would break the common "manual trigger beside a webhook" shape that this ticket exists to support.

The trap is the execution record's `Trigger` field: it holds `manual | webhook | schedule`, not a node ID. Adding the node ID is a new column on `executionModel` and an OpenAPI change — regenerate with `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/` or the drift checks fail.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-3.
- `internal/workflow/compiler.go` — `validateExecutableTopology`, `producesItems`, the reachability walk.
- `internal/engine/runner.go` — `Request`, `Run`.
- `internal/engine/service.go` — `run`, `inputItem`.
- `internal/repository/webhooks.go`, `internal/repository/models.go` — `WebhookBinding.NodeID`, `scheduleModel.NodeID`.

## Outcome

Both halves landed together, as the plan required. Relaxing the compiler alone
would have produced documents that save and activate and then execute the wrong
thing: every root seeded with the same trigger item, so a schedule firing on a
webhook delivery.

### Compiler

`len(roots) != 1` became `len(roots) == 0`, and reachability is seeded from
every root rather than `roots[0]`. The attachment-provider back-walk needed no
change. Both bounds still hold and are tested: a graph with no root is refused
by name, and a node reachable from no root is still `disconnected from the
trigger` with the same error code and the node named.

### Runner

`Request.TriggerNodeID` names the root a run starts from; empty means every
root, which is what a manual run means and what keeps a single-root graph
behaving exactly as before.

`activeNodes` walks forward over the item channel from that node, then walks
attachment edges **backwards** from whatever was reached — a chat model is
upstream of the agent it configures rather than downstream of a trigger, so a
forward-only walk would exclude every provider from every triggered run.

The inactive part of the graph is not skipped node by node; it is **absent**.
Both the node set and the edge set are filtered before scheduling, which matters
for the shared-node case: a node fed by both triggers would otherwise wait
forever on the trigger that did not fire and the run would fail with "no
schedulable node". `TestRunStartsFromTheNamedTriggerOnly` asserts the shared node
runs once and receives only the firing trigger's item.

A named trigger that is not in the workflow is refused rather than falling back
to running everything, which would look like success.

### Plumbing

`execution.Record` and `executionModel` gained `TriggerNodeID`, threaded from
`repository.WebhookBinding.NodeID` and `Schedule.NodeID` through
`QueueTriggered`, and read back in `Service.run`. `scheduler.QueueFunc` gained
the parameter, and `TestScheduleQueuesItsOwnTriggerNode` asserts the scheduler
passes the node that fired rather than an empty string.

The API exposes it on both `ExecutionSummary` and `ExecutionResource` as
`triggerNodeId`; both clients were regenerated and the drift checks pass.

### The open decision

Taken as recommended: a manual run with nothing named runs **every** root. It is
what a manual run means today, it keeps the API unchanged for the single-root
case, and refusing would break the "manual trigger beside a webhook" shape this
ticket exists to support.

### Corpus effect

None, and that is expected rather than disappointing. Three of the 38 corpus
fixtures declare more than one trigger — `waha-trigger-explanation` (webhook +
wahaTrigger), `whatsapp-typebot` (manualTrigger + wahaTrigger) and
`send-bulk-messages` (manualTrigger + webhook) — and all three still fail
earlier, on WAHA nodes that have no mapping yet. The epic's "52 of 100" figure
is for the n8n.io template set, not this corpus. The rule is gone; what those
three are now waiting on is p3.
