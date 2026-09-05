---
title: The execution model
description: How a workflow gets from a trigger to a node run to a recorded execution, and what happens when the worker running it dies.
sidebar:
  order: 2
---

This is the page to read first. Everything else in this section is a detail of
some step described here.

## A workflow is saved as versions, and a version is what runs

`POST /api/v1/workflows` stores an immutable snapshot. Every save appends a new
one, so a workflow is a name and an activation state with a history of revisions
behind it, and `/workflows/{id}/versions` lists them.

An execution is pinned to one version at the moment it is queued, and reads its
document back from that pin. That is why editing a workflow cannot change what a
run in flight is doing: the run is not holding a workflow ID and looking it up
again, it is holding a version.

## Compilation happens before an execution record exists

A document is validated in two stages, and the split matters because they refuse
different things.

`workflow.DecodeDocument` checks the shape: strict JSON field names via
`DisallowUnknownFields`, the required object and array fields that a zero-valued
Go struct cannot represent, and the structural rules a document must satisfy on
its own. Parameter and settings maps stay deliberately open here, because only
the node catalogue knows what belongs in them.

`workflow.Compile` then does everything that needs the catalogue, and produces
the IR — a validated, copied graph the engine executes. The copy is the point:
`IRNode` does not embed `Node`, so a later mutation of the canonical JSON cannot
change a compiled execution plan.

Compilation is where a workflow is refused for a reason a user can act on, each
with its own stable error code:

| Code | What it means |
| --- | --- |
| `node.unknown_type` | no such node type is registered |
| `node.unknown_version` | the type exists, but not at a version this install can resolve |
| `port.unknown` | a connection names a port the node does not have |
| `port.incompatible` | the two ends speak different connection channels |
| `port.full` | the port's `maxConnections` is already taken |
| `port.required` | a port that must be connected is not |
| `port.node_not_allowed` | the port restricts which node types may connect there |
| `config.required` | a parameter this node needs, in this configuration, is missing |
| `config.invalid` | a parameter is present and wrong |
| `workflow.invalid_topology` | the graph itself is wrong — empty, a connection naming a node that is not there, a duplicated connection, or a cycle that is not a declared loop |

Three of those are worth dwelling on. `port.full`, `port.required` and
`port.unknown` are deliberately separate codes rather than one, because "this
port is full", "this port must be connected" and "this port does not exist" are
three different problems for a user to fix and one code for all three tells them
nothing. `config.required` is computed per node rather than read from a static
list, because required-ness stopped being a property of the node type the moment
visibility became conditional — a Telegram node's chat ID is required only when
the resource is `message`. And `workflow.invalid_topology` covers cycles: a back
edge is legal only when its target is a node whose definition declares
`LoopEntry`, so a loop somebody built on purpose compiles and an accidental
cycle does not.

## Running is asynchronous, always

`POST /api/v1/workflows/{id}/run` writes a queued execution record and returns
`202 Accepted` with the execution ID. It does not run anything. Neither does an
inbound webhook, and neither does the scheduler — all three write the same kind
of row through the same durable queue, so a trigger cannot bypass lifecycle,
validation or execution-record rules by having its own path into the engine.

```
   POST /workflows/{id}/run  ─┐
   POST /webhook/{route}     ─┼─▶  executions row: status=queued, pinned version
   cron scheduler            ─┘         │
                                        │  worker pool (execution.max_concurrent, default 10)
                                        ▼
                              ClaimNext: status=running, lease_owner, lease_expires_at
                                        │
                                        ▼
                              compile the pinned document  ──▶  IR
                                        │
                                        ▼
                              run nodes in topological order
                                 └─ per node: a node_runs row, then an event
                                        │
                                        ▼
                              status=succeeded | failed | cancelled
                                 └─ terminal event, stream closes
```

Nothing here is a message broker. The queue is a table in the same database as
everything else, which for the deployments this is aimed at is one fewer thing to
run — and is also a ceiling worth knowing about before you reach it.

## The claim is a lease, which is what makes it durable

`GORMExecutionStore.ClaimNext` opens one transaction, selects the oldest eligible
row, and then updates it with the eligibility predicate repeated in the `WHERE`
clause. That second statement is a compare-and-set: if another worker claimed the
row between the select and the update, zero rows are affected and this worker
claims nothing rather than trampling the winner. Eligible means either
`status = 'queued'`, or `running` (or `cancelling`) with a `lease_expires_at`
that has already passed.

That second arm is the recovery path. A worker sets its lease to *now plus the
execution timeout* when it claims, and if the process dies mid-run the row simply
becomes eligible again once that lease expires, and another worker picks it up.
Nothing has to notice the crash; the absence of a renewed lease is the signal.

Recovery has one consequence worth stating plainly. Node runs are written after
the in-memory graph completes, so a process that died between individual trace
writes leaves a partial attempt whose `(execution, sequence)` and
`(execution, node, attempt)` keys would collide with the recovered attempt's.
`ClaimNext` therefore deletes the abandoned trace when it reclaims an expired
lease. **A recovered execution re-runs from the beginning.** There is no
checkpointing and no resume from the last completed node, so a workflow that
performs non-idempotent side effects can perform them twice if its worker dies
partway. Nothing in the system prevents that today.

Workers poll: `RunOnce` claims and completes at most one execution, and an idle
worker waits on either a wake signal — which the API sends after queueing — or a
100 millisecond timer, whichever comes first.

## Inside one run

The runner schedules a compiled DAG in stable topological order. Nodes inside
one execution do not run in parallel with each other; independent *executions*
run concurrently, one per worker. That is a deliberate trade: it eliminates
race-dependent item concatenation, so the same graph over the same input
produces the same trace every time.

**Only the trigger's part of the graph runs.** A workflow may declare several
trigger roots — a webhook beside a nightly schedule is the standard shape — and
only one of them fires on any given run. The runner walks forward over the item
channel from the named trigger, then walks *backwards* from whatever it reached
to pick up attachment providers such as a chat model, a memory or a tool, which
sit upstream of the node they configure. Everything else is not skipped node by
node, it is **absent** — so a node fed by both triggers waits only on the one
that fired instead of deadlocking on the one that did not. A run with no named
trigger runs every root, which is what a manual run means.

**An untaken branch is recorded, not omitted.** A node no incoming channel
delivered anything to is marked `skipped`, and gets a `node_runs` row saying so.
Recording it as succeeded would make the untaken arm of an `IF` look like one
that ran and happened to produce nothing, and omitting it entirely would make the
execution look as though the branch never existed. The distinction between "did
not run" and "ran and produced nothing" is exactly what someone reading a branch
needs to see.

**A node can run more than once.** A node inside a loop, or downstream of a
fan-out, produces several distinct runs, and `RunIndex` counts them from zero.
That is deliberately separate from `Attempt`, which counts retries of the *same*
run: conflating them would make a retry inside a loop unrepresentable. Both are
persisted on every `node_runs` row alongside a monotonic `Sequence`.

**Loops are declared, not inferred.** A loop is a node whose definition sets
`LoopEntry`, plus the edges that close back onto it. The back edge is hidden from
ordinary scheduling until the loop has actually dispatched a batch — without
that, a loop node waits forever on its own body, which has not run because the
loop node has not dispatched. Iteration is driven by the loop node's own output
rather than by a counter the scheduler keeps: the node says whether it has more
work by emitting on its `loop` port.

### Failure, retries and tolerance

Every node carries three retry settings — `retryOnFail`, `maxTries` and
`waitBetweenTries` — and the compiler has already rejected out-of-range values,
so the runner clamps rather than errors. Turning `retryOnFail` on with no
`maxTries` gives three attempts, and with no `waitBetweenTries` a non-zero
default delay applies. That default is deliberate: without it every attempt
fires inside a millisecond, and one failing request becomes a burst against an
upstream that is already struggling.

A fourth setting, `continueOnFail`, makes a node tolerate its own failure. It
then emits one
error item per input item, so downstream item counts and
[paired-item lineage](/concepts/items-and-lineage/) survive the failure, and a
single error item when it had no input to pair against. The input is
deliberately *not* passed through unchanged — a downstream node has to be able to
tell a tolerated failure from a success, and identical items would make that
impossible.

An untolerated failure ends the execution. The record's status becomes `failed`
with a structured error carrying a code: `execution.failed`, or
`execution.timeout` if a node reported hitting the deadline, or
`execution.cancelled` if the context was cancelled.

### Timeouts and concurrency

`execution.default_timeout` bounds one whole run and defaults to 60 seconds. It
is also the lease length, which is what ties the two together — a run that
exceeds its timeout has, by definition, an expired lease.
`execution.max_concurrent` sets the size of the worker pool and defaults to 10.
Both are read once at startup.

## What gets recorded

An execution row carries its status, its trigger kind (`manual`, `webhook`,
`schedule` or `subworkflow`), the trigger node it started from, the parent
execution if it was a sub-workflow call, its input, its output and its error.
Every node invocation gets its own `node_runs` row with that node's full input
and output.

`LeaseOwner` is on both types and is never part of the API. It is an internal
fencing token, and exposing it would publish a detail whose only purpose is to
let a repository refuse a write from a worker whose claim has been taken away.

Redaction happens before anything is persisted or published:
`internal/execution` holds the redaction rules, and webhook headers in particular
go through them. Bodies deliberately do not — a workflow's whole purpose is often
the body, and redacting it would make the trace useless.

Listing executions is **keyset-paginated, not offset-paginated**. The cursor pins
the last `(started_at, id)` pair seen, so an execution created while somebody is
paging through history cannot shift rows onto a page they have already read. The
cursor is opaque; treat it as a token to hand back, not a value to construct.

There is **no execution retention or pruning today.** Nothing in the codebase
removes an execution row. The binary store has a deletion path ready for the
pruner that will use it, but that pruner does not exist yet, so an execution
history grows without bound.

Workflow *version* history at least has the machinery: `history.retention` and
`history.max_versions` prune old revisions, and the count bound is applied inside
the same transaction that created the revision which broke it rather than by a
sweep that runs later. Both default to zero, meaning unbounded, so they only help
once an operator sets them.

Even then the bound is a target rather than a guarantee, because some revisions
are protected from pruning: the published version, the newest revision, and any
version an execution or a webhook binding still points at. The execution pin is
not a courtesy — `executions.workflow_version_id` is `ON DELETE RESTRICT`, so
deleting one would fail the whole statement. A workflow whose old revisions are
all pinned by executions therefore stays above `history.max_versions`
indefinitely, which is another consequence of executions never being pruned.

## The event stream

`GET /api/v1/executions/{id}/events` is a server-sent event stream fed by an
in-process broker. Events carry a monotonic per-execution ID, so a browser that
reconnects sends `Last-Event-ID` and resumes rather than restarting; a bounded
history is retained per execution to make that replay possible. The stream closes
after a terminal event — `execution.completed`, `execution.failed` or
`execution.cancelled` — which is what lets a client stop reconnecting.

Delivery is best effort by design. The durable execution and node-run records are
the source of truth, and a run must succeed whether or not anyone is watching, so
nothing on this channel can block or fail a workflow. A subscriber that cannot
keep up has events dropped rather than stalling the run.

The vocabulary in `internal/events` has nine names:

| Event | Emitted today? |
| --- | --- |
| `execution.started` | yes, when a worker claims and begins a run |
| `execution.completed` | yes, terminal |
| `execution.failed` | yes, terminal |
| `execution.cancelled` | yes, terminal |
| `node.completed` | yes, after the node run is durable |
| `node.failed` | yes, after the node run is durable |
| `node.started` | **no — declared and typed, never published** |
| `node.output` | **no — declared and typed, never published** |
| `workflow.saved` | **no — declared and typed, never published** |

The three that are never published are real entries in the contract and in the
OpenAPI document — the SSE registration binds one Go type per event name so the
specification can name each one — but no code path emits them. Do not build a
client that waits for `node.started`.

A node event is published only after that node's run is durable, so a subscriber
can never observe a state the record does not already carry. The one exception is
nested progress from inside a long-running node, which is published as it happens
and therefore carries no state a consumer may treat as authoritative. The AI
agent node uses this to report its model turns and tool calls, which arrive on
the same stream under eight further names — `ai.model.started`,
`ai.model.delta`, `ai.model.completed`, `ai.tool.started`, `ai.tool.completed`,
`ai.tool.failed`, `ai.agent.completed` and `ai.agent.failed`. They are not in the
`events.Type` constant list, so a client must tolerate an event name it does not
recognise.

## Sub-workflows

An Execute Workflow node runs another workflow of the same tenant, resolved
inside the calling execution's tenant scope — so an ID from another tenant simply
does not exist.

A child runs **inline in the caller's goroutine**, holding the parent's worker
slot. "Fire and forget" therefore still runs the child to completion; it just
does not hand the items back. It is not "start it in the background", because a
detached child would need a worker slot the parent is already holding.

Recursion is refused by a call *stack*, checked before a depth counter rather
than instead of one. The stack refuses the second A in A→B→A immediately and
names the cycle in the error; a counter alone would let that run all the way to
the limit and spend the whole budget before failing, then report only that a
number was exceeded. The counter is still there behind it —
`MaxWorkflowCallDepth` is 16 — because a chain of sixteen *distinct* workflows is
a runaway too, and every level of it holds the caller's goroutine.

## What this model does not do yet

**Executions are not suspended to storage.** A `Wait` node holds its worker for
the duration. Two limits apply and the smaller one usually bites first: the node
refuses a wait longer than an hour outright, but the whole run is bounded by
`execution.default_timeout`, which is **60 seconds** by default — so on a stock
install a `Wait` of more than a minute ends the execution with
`execution.timeout` rather than waiting. The hour is the ceiling an operator
reaches only after raising that timeout. Waiting on an inbound webhook or a form
submission returns an error rather than parking the execution and resuming it
later.

**Scheduling is single-process.** The cron scheduler claims due rows
transactionally, so a second instance will not double-fire, but distributed
scheduling is not designed.

**Recovery is a restart, not a resume.** See the lease section above.

## API operations

`POST /workflows/{id}/run`, `GET /executions`, `GET /executions/{id}`,
`POST /executions/{id}/cancel`, `GET /executions/{id}/events`. See the
[HTTP API reference](/reference/api/) for the full surface, and a running
server's own `/docs` for the instance you are talking to.

## Source

`internal/engine/runner.go` (the run loop, branch pruning, loops, retries),
`internal/engine/service.go` (the worker pool, event publication, sub-workflow
calls), `internal/repository/executions.go` (`ClaimNext` and the lease),
`internal/workflow/compiler.go` (validation and the IR),
`internal/execution/records.go` (what is persisted), `internal/events/events.go`
(the event contract and the broker).
