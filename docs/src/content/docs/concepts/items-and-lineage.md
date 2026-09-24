---
title: Items and lineage
description: The unit of data a node receives and emits, how binary payloads are referenced rather than carried, and how an item knows where it came from.
sidebar:
  order: 3
---

Everything that moves between nodes is an item. A node receives items grouped by
the input port they arrived on, and emits one list of items per output port it
declares.

```go
type Item struct {
	JSON   map[string]any       `json:"json"`
	Binary map[string]BinaryRef `json:"binary,omitempty"`
	Paired *PairedItem          `json:"pairedItem,omitempty"`
}

type NodeInput  map[string][]Item   // keyed by destination port
type NodeOutput [][]Item            // one stream per declared output port, in order
```

The `json` wrapper is not decoration. It is why an expression reads
`$json.email` rather than `$item.email`, and why `$('Node Name').first().json`
has that shape. It comes from n8n's format, and keeping it is what lets an
imported workflow's expressions resolve without rewriting them.

Note the asymmetry between input and output. Input is keyed by port *name*,
because a node has to know which of its ports an item came in on. Output is a
slice indexed by position, because the compiled IR has already resolved the
stable output-port order — which is how `IF`'s false branch becomes output index
1 without the node knowing what a "false port" is.

## A node is called once, over all of its items

The runner hands an executor everything on each input port in one call. A node
that wants per-item behaviour loops inside itself. This is why `Wait` waits once
rather than once per item — "wait an hour" said over a hundred items means an
hour, not a hundred hours.

Items are deep-copied at the boundary between nodes. `cloneItem` is the single
choke point every item passes through, which is why provenance is copied there
rather than at each call site: missing it in one place is the kind of failure
that stays invisible until one specific graph shape hits it.

## Binary payloads are references, never bytes

An item's `binary` map holds `BinaryRef` values, not content:

```go
type BinaryRef struct {
	ID        string `json:"id"`
	FileName  string `json:"fileName,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Size      int64  `json:"size,omitempty"`
}
```

The bytes live on a filesystem root managed by `internal/binary`, keyed by
tenant, execution and that ID. Only this metadata enters a workflow document, an
execution record, an API response body or a log line — which is why the fields
are exactly the ones a person needs to recognise an attachment and nothing that
could carry content. A trace of a run that moved a 4 MB PDF is a few hundred
bytes.

Two operational consequences follow.

The store is **scoped to the execution before an executor ever sees it**, so a
node cannot read another tenant's payload even by holding a reference it should
not have. And an unset `binary.root` leaves the store nil rather than boxing a
nil into the interface, so a node can *ask* whether storing a payload is even
possible and degrade cleanly. A node that had to attempt a write to find out is
a node that fails where it could have reported.

Writes are bounded by `binary.max_bytes`, which defaults to 16 MiB, and an
oversized payload is refused rather than truncated. Tenant, execution and
reference identifiers are each validated against the same conservative path
pattern before they become a directory name — all three are checked the same way
rather than trusting provenance.

## Lineage: which input item did this output item come from?

This is the `pairedItem` contract, and it is what makes `$('Node Name').item`
answerable.

```go
type PairedItem struct {
	SourceNodeID string `json:"sourceNodeId"`
	SourcePort   string `json:"sourcePort,omitempty"`
	RunIndex     int    `json:"runIndex"`
	ItemIndex    int    `json:"itemIndex"`
	Lost         bool   `json:"lost,omitempty"`
	Parent       *PairedItem `json:"parent,omitempty"`
}
```

n8n expresses the same idea as `{item, input?}` pointing at an incoming item
index. KilasFlow needs more for two structural reasons: its ports are named
rather than positional, so the origin has to name a port; and a node can run
several times in one execution, so the origin has to name a run. The origin is
therefore a node, a port, a run and an item.

### One origin, plus an explicit "lost"

n8n models the origin as a *list* of candidates. KilasFlow records a single
origin and, when the correspondence genuinely cannot be established, sets `Lost`
instead. One origin plus an explicit marker is easier to reason about and is
enough for every expression in the import corpus; a list would force every
consumer to handle an ambiguity that only aggregating nodes can produce.

`Lost` is set after a merge of unrelated streams, or after a node that changed
the item count without saying how. This is the whole point of the design: a
lookup against lost lineage **fails with a reason** rather than quietly returning
the first item. Returning the first item is correct only when every node
processed exactly one item, and confidently wrong otherwise — which is the worst
failure mode an imported workflow can have, because it produces plausible output.

### Who fills it in

An executor sets its own provenance when it knows it. The runner fills in what
the executor did not, by position — but only under a narrow rule:

> Inference by position applies only when a node has exactly one incoming item
> port and produced exactly as many items as it consumed.

That is the shape of every one-to-one node: Set, HTTP Request, a database query,
a model call. A node that reorders, filters, aggregates or fans out **must** set
its own provenance, because guessing there would produce a confident answer that
happens to be wrong.

### A fan-out's items are origins of their own

When several items of one node's output share one origin — Split Out splitting
one item's list, a Code node returning several items for one input — the runner
makes each of them an origin of its own: that node, run, port and position,
with the origin they shared kept as `Parent`. Everything further down copies
that stamp, so a Sort, a Filter or a Code node that reorders after the fan-out
cannot move an item away from the one it came from.

`$('X').item` compares the current item's origin with X's items first, then
each `Parent` in turn: X after the fan-out shares the item's own origin, X
before it shares the parent. A fan-out after another nests one level deeper,
up to sixteen levels; past that the levels in the middle are dropped, and the
item's own origin, the nearest fan-outs and the root are kept.

A node that tolerates its own failure emits one error item per input item for
exactly this reason: downstream item counts and lineage survive a tolerated
failure, so an expression reaching back past it still resolves.

### What reads it, and how far that has got

`$('Node Name').item` in the [expression evaluator](/concepts/expressions/).

Be precise about what it does today, because it is less than the name suggests.
The runner checks that **every** item the named node emitted carries non-nil,
non-`Lost` provenance, and then resolves `.item` only when that node produced
**exactly one** item. Anything else yields a refusal that names the reason:

| Situation | What `.item` says |
| --- | --- |
| the node set no provenance | "this node did not record where its items came from" |
| any item is marked `Lost` | "node *X* changed the item correspondence, so there is no single item to pair with" |
| the node produced nothing | "node *X* produced no items" |
| the node produced several | "node *X* produced *n* items; use `.all()`, `.first()` or `.last()` to choose one" |

So `.item` is currently "the named node's single item, when its provenance is
intact" rather than "the ancestor of the item I am processing". Selecting among
several needs a current-item context that the evaluator is not yet given — an
executor that evaluated expressions once per item would supply it, and none does.

What matters is that the gap is **reported rather than guessed**. There is no
fallback to the first item, because the first item is the right answer only when
every node processed exactly one, and confidently wrong otherwise.

## Item counts and node runs

`RunIndex` on a `PairedItem` refers to the same counter as `RunIndex` on a
[node run record](/concepts/execution-model/): the Nth time that node ran in this
execution, counting from zero. A node inside a loop produces one run per
iteration, and an item's lineage names which iteration produced it.

That is distinct from `Attempt`, which counts retries of the *same* run.
Conflating them would make a retry inside a loop unrepresentable.

## Source

`internal/workflow/document.go` (`Item`, `BinaryRef`, `PairedItem`,
`NodeInput`, `NodeOutput`), `internal/engine/runner.go` (`cloneItem`,
`stampProvenance`, `errorOutput`, `nodeItemFor`), `internal/binary/` (the
payload store and its bounds).
