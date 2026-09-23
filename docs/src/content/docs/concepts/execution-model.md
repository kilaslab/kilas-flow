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
| `node.not_available` | the node type exists, but is scoped to other tenants and is not available to this one |
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
                                        ├─ a Wait node suspends ─▶ status=waiting,
                                        │        worker and lease released
                                        │             │
                                        │             └─ resume call, approval
                                        │                decision or deadline
                                        │                ─▶ status=queued, continues
                                        │                   from its checkpoint
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

That second arm is the recovery path. A worker claims its lease for two run
timeouts — with a floor, so a short timeout does not make the claim shorter than
a database round trip — and then *renews* it by a heartbeat for as long as the
worker is alive. That is what makes the lease mean "this worker is still here"
rather than "this worker was predicted to need this long": a run that is simply
slower than its timeout, or a trace write that lands after it, keeps its claim
instead of being reclaimed underneath itself. If the process dies mid-run the row
becomes eligible again once the renewals stop and the lease expires, and another
worker picks it up. Nothing has to notice the crash; the absence of a renewed
lease is the signal.

Recovery has one consequence worth stating plainly. The trace is written in one
transaction once the graph has completed — a suspension writes the segment its
run produced before it parked — so a worker that died mid-run leaves either
nothing or a segment belonging to an attempt that will not continue. `ClaimNext`
deletes the abandoned trace when it reclaims an expired lease, because the
recovered attempt restarts its sequence numbering from zero and its rows would
otherwise match the abandoned ones' keys and be read as duplicates of rows
describing work the new attempt never did. **A recovered execution re-runs from
the beginning**, because the checkpoint that would let it continue belongs to a
suspension and not to a crash; see [the suspension
section](#suspending-to-storage-and-resuming) below. A workflow that performs
non-idempotent side effects can therefore perform them twice if its worker dies
partway. What bounds that is a reclaim count: an execution whose expired lease
has been handed on twice is settled `failed` with the code `execution.crashed`
rather than given to a third worker, so a run that kills its own worker ends as
one failed execution instead of a loop.

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

`execution.default_timeout` is the budget for one whole run and defaults to two
minutes, but it is a budget rather than a cap on how long a workflow may take: a
workflow can name its own in `settings.executionTimeout` — n8n's own name for it,
in seconds — and its own value wins, clamped to `execution.max_timeout` when the
instance sets a ceiling. A negative value is n8n's "no execution timeout" and
leaves the run bounded only by cancellation, a node's own timeout and the lease;
a workflow that names nothing still gets the default, so the bound is never
absent by accident. The lease length is derived from that timeout — twice it,
with a floor — and then renewed while the worker lives, which is what lets a run
that takes its whole budget keep its claim.

`execution.max_concurrent` sets the size of the worker pool and defaults to 10.
The instance's default, ceiling and pool size are read once at startup; a
workflow's own budget is read from its document when the run is claimed.

## Suspending to storage, and resuming

A `Wait` node does not sleep inside the worker. Any pause longer than zero parks
the execution: the trace produced so far is persisted first, then the wait row is
written and the lease released in one transaction, the record's status becomes
`waiting`, and the worker moves on to the next execution. The row holds the mode,
the node, the deadline, a checkpoint of the exact upstream data the run was
holding — and a single-use resume token whose SHA-256 is the lookup key, so a
database dump or a replica discloses no resume capability.

The run is told its own links before it can suspend, so a workflow can compose and
send them itself: `$execution.resumeUrl` for a machine call and
`$execution.approvalUrl` for the human page. **The Wait node sends nothing** — the
workflow has to reference the link (an HTTP node, a Set node, a Webhook node's URL
field) for anyone to receive it. A waiting execution answers
`GET /api/v1/executions/{id}` with both links, and with neither once it is no
longer `waiting`.

A suspension ends one of three ways.

**A deadline the wait carries itself.** `resume: timeInterval` and
`resume: specificTime` are timer waits: the process arms an exact timer for the
wait's deadline, and the execution is re-queued and continues from its checkpoint
with the suspending node's input passed through as its output. The timer is the
low-latency path; the periodic sweep is the floor under it, which is what settles
a wait whose process died before its deadline (`execution.wait_sweep_interval`,
ten seconds by default, and not switchable off).

**A call to the resume URL.** `resume: webhook` parks the execution until
something posts to `/resume/{token}`; `resume: form` parks it until a human
decides at `/approve/{token}`, the dashboard page that records the decision and
calls the same URL on the decider's behalf. An approval resumes the run with one
item carrying `{approved, respondedAt, decidedBy, note}`, the shape n8n's own
approval nodes emit, so an imported workflow branches on it unchanged. Both modes
accept n8n's own limit (`limitWaitTime` with `limitType`, `limitAmount`,
`limitUnit` or `limitAt`), which turns the wait into a timer that the same call
can still end early.

**A decision that never arrives.** A call-resumed wait with no limit of its own
gets 24 hours, and past its deadline it fails the execution with the code
`wait.expired` — a named failure, with no execution left suspended for ever. A
wait whose process died is settled by the sweep within one interval of the next
boot.

A resumed run continues **past** the suspending node rather than re-running it:
the checkpoint carries the completed nodes' outputs, the suspending node
completes with the stored resume output, and sequence numbering continues after
the node runs the suspension already persisted. Cancelling a waiting execution
completes it as `cancelled` at once, because there is no worker left to observe a
cancellation request.

Two things a suspension is not. It is **not available inside a sub-workflow**:
a child runs inline in its caller, so suspending it would snapshot the parent at
the calling node and re-execute the child's completed nodes on resume, and the
engine refuses it with that explanation rather than doing it. And it is **not
what happens when a worker dies** — that path has no checkpoint and re-runs from
the beginning, as the lease section above says.

A suspension publishes `execution.waiting`, which is deliberately non-terminal:
`events.Type.Terminal` reports false for it, so a live feed stays open across the
wait instead of closing and making the client reconnect. The event carries the
node, the mode and the deadline — never the token and never the payload, both of
which are run data.

## What gets recorded

An execution row carries its status, its trigger kind (`manual`, `webhook`,
`schedule` or `subworkflow`), the trigger node it started from, the parent
execution if it was a sub-workflow call, its input, its output and its error.
Every node invocation gets its own `node_runs` row with that node's full input
and output.

`LeaseOwner` is on both types and is never part of the API. It is an internal
fencing token, and exposing it would publish a detail whose only purpose is to
let a repository refuse a write from a worker whose claim has been taken away.

Redaction is a read-surface guarantee rather than a storage one. A trigger
delivery is stored exactly as it arrived — headers included — because the stored
record *is* the input the run executes on: a workflow reading its own
`headers['x-api-key']`, or a cookie, has to see what the caller sent rather than
a placeholder. The surfaces that serve a record back redact instead; API
responses, the live feed and the inspector pass the payload through
`internal/execution`'s rules, which normalise header names and withhold
credential keys as `[redacted]`. The node-run trace keeps redacting on the way
*in*, so a credential the runtime resolved never lands in a node's stored input
or output. A raw database dump or a backup therefore carries inbound trigger
headers and bodies verbatim, and [the security
page](/operate/security/) says what that asks of an operator.

Listing executions is **keyset-paginated, not offset-paginated**. The cursor pins
the last `(started_at, id)` pair seen, so an execution created while somebody is
paging through history cannot shift rows onto a page they have already read. The
cursor is opaque; treat it as a token to hand back, not a value to construct.

Execution retention exists and is **off by default.** `execution.retention`
deletes a finished execution — with its node runs and its stored binary payloads
— once it has been finished for longer than the configured age. A pruner sweeps
every fifteen minutes in bounded batches, selects by `finished_at` rather than
`started_at`, and never touches a queued, running, cancelling or still-waiting
row. Zero, the default, means keep everything, so an installation nobody
configured keeps growing; that default is deliberate, because an operator who has
never configured retention must not discover that a new build deleted the history
they were about to debug.

Workflow *version* history has its own machinery: `history.retention` and
`history.max_versions` prune old revisions, and the count bound is applied inside
the same transaction that created the revision which broke it rather than by a
sweep that runs later. Both default to zero, meaning unbounded, so they only help
once an operator sets them.

Even then the bound is a target rather than a guarantee, because some revisions
are protected from pruning: the published version, the newest revision, and any
version an execution or a webhook binding still points at. The execution pin is
not a courtesy — `executions.workflow_version_id` is `ON DELETE RESTRICT`, so
deleting one would fail the whole statement. A workflow whose old revisions are
all pinned by executions therefore stays above `history.max_versions` for as long
as those executions exist — indefinitely on a default install, where nothing
prunes them.

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
`ai.tool.failed`, `ai.agent.completed` and `ai.agent.failed`. Together with
`execution.waiting` from the suspension section above and `webhook.response`,
each of them reaches the stream as a named frame, so an `EventSource` listener
for the name receives it. A name added later, before a client knows it, is sent
under the fallback name `execution.event`, with its real name in the payload's
`type`. Nothing is ever sent unnamed, and a client must still tolerate a name it
does not recognise.

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

**A suspension has ceilings.** A single wait is refused past seven days: a wait
that cannot resolve is a leak, so the node fails at composition time with the
limit named rather than parking an execution nobody will resume. A wait that
resumes on a call and is never called fails by name — `wait.expired` — at its own
deadline, which is 24 hours when the node names none. And a wait inside a called
sub-workflow is refused outright, because a child runs inline in its caller. The
mechanism these are the edges of is in [Suspending to storage, and
resuming](#suspending-to-storage-and-resuming).

**Scheduling is single-process.** The cron scheduler claims due rows
transactionally, so a second instance will not double-fire, but distributed
scheduling is not designed.

**Crash recovery is a restart, not a resume.** A reclaimed execution re-runs from
the beginning rather than from the last completed node, so a non-idempotent node
can perform its side effect twice; the reclaim cap of two hand-offs stops a
repeatedly crashing execution from being re-run for ever, settling it `failed`
instead. See the lease section above.

## API operations

`POST /workflows/{id}/run`, `GET /executions`, `GET /executions/{id}`,
`POST /executions/{id}/cancel`, `GET /executions/{id}/events`. A waiting
execution's `GET` also carries its `resumeUrl` and `approvalUrl` — the
`/resume/{token}` and `/approve/{token}` paths that live beside `/webhook` rather
than under the API prefix, and the same links the run can compose for itself as
`$execution.resumeUrl` and `$execution.approvalUrl`. See the [HTTP API
reference](/reference/api/) for the full surface, and a running server's own
`/docs` for the instance you are talking to.

## Source

`internal/engine/runner.go` (the run loop, branch pruning, loops, retries),
`internal/engine/service.go` (the worker pool, the lease heartbeat, the run
budget, event publication, sub-workflow calls), `internal/engine/wait_service.go`
(suspension, resume, the wait sweep), `internal/repository/waits.go` (the wait
row and its checkpoint), `internal/repository/executions.go` (`ClaimNext` and the
lease), `internal/repository/execution_retention.go` (the execution pruner),
`internal/workflow/compiler.go` (validation and the IR),
`internal/execution/records.go` (what is persisted), `internal/events/events.go`
(the event contract and the broker).
