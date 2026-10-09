# A validated loop over batches

A list is split into one item per element, a Loop Over Items node hands them to a
body two at a time, the body writes back into the loop, and when the batches run
out everything the body returned leaves on `done` and is gathered into one item.
`kilasflow workflow validate` accepts it, and a run with three orders produced
the output shown below.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Process orders in batches of two",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "split",
      "name": "One item per order",
      "type": "kilasflow.splitOut",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": { "fieldToSplitOut": "orders" }
    },
    {
      "id": "batches",
      "name": "Loop over orders",
      "type": "kilasflow.loop",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": { "batchSize": 2, "maxIterations": 20 }
    },
    {
      "id": "mark",
      "name": "Mark handled",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 720, "y": -100 },
      "parameters": {
        "assignments": {
          "assignments": [
            { "id": "a1", "name": "handled", "type": "boolean", "value": true }
          ]
        },
        "include": "all"
      }
    },
    {
      "id": "gather",
      "name": "Gather results",
      "type": "kilasflow.aggregate",
      "typeVersion": 1,
      "position": { "x": 720, "y": 100 },
      "parameters": { "aggregate": "aggregateAllItemData", "destinationFieldName": "orders" }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "split", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "split", "port": "main" },
      "target": { "nodeId": "batches", "port": "main" }
    },
    {
      "id": "c3",
      "kind": "main",
      "source": { "nodeId": "batches", "port": "loop" },
      "target": { "nodeId": "mark", "port": "main" }
    },
    {
      "id": "c4",
      "kind": "main",
      "source": { "nodeId": "mark", "port": "main" },
      "target": { "nodeId": "batches", "port": "main" }
    },
    {
      "id": "c5",
      "kind": "main",
      "source": { "nodeId": "batches", "port": "done" },
      "target": { "nodeId": "gather", "port": "main" }
    }
  ],
  "settings": {}
}
```

With `{"orders":[{"id":1},{"id":2},{"id":3}]}` the Gather node outputs one item:

```json
{ "orders": [ { "id": 1, "handled": true }, { "id": 2, "handled": true }, { "id": 3, "handled": true } ] }
```

## What it shows

- **Most nodes need no loop.** Every node already runs once per incoming item, so
  a Set node after `One item per order` marks all three orders without any loop.
  The Loop Over Items node exists for batching and pagination: a body that must
  see two items at a time, or a call that has to be made in groups.
- **The loop has two outputs.** `loop` carries the current batch into the body,
  and `done` carries everything the body returned, once, after the last batch.
  Work that should happen once afterwards hangs off `done`, not off the body.
- **The body is wired back.** `c4` runs from the body's last node into the loop's
  own `main` input. That back edge is the only cycle the compiler accepts, and
  only on a node that is declared as a loop entry; any other cycle is refused as
  an invalid topology.
- **It is bounded.** `maxIterations` fails the run rather than letting a loop that
  never converges report success. `batchSize` is how many items one pass receives.
- **Splitting and gathering are nodes.** `kilasflow.splitOut` turns a list field
  into one item per element, and `kilasflow.aggregate` collapses many items back
  into one list field. A list inside a single item is an array, and a node that
  expects items will not see its elements until it is split out.

## Check it

```bash
kilasflow workflow validate --file batches.json
kilasflow workflow create --file batches.json --quiet
kilasflow run <workflowId> --wait --input '{"orders":[{"id":1},{"id":2},{"id":3}]}'
kilasflow exec get <executionId> --json
```

In the trace, `Mark handled` has one node run per batch, told apart by
`runIndex`; a node that ran twice is a loop working, not a retry (a retry is a
higher `attempt` of the same run).
