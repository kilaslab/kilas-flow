# A validated document worth debugging

A manual trigger and a JavaScript Code node that totals the amounts it receives.
It is small enough to read in one glance and has the two things a debugging
session needs: a node that transforms data, and a trace to read afterwards.
`kilasflow workflow validate` accepts it, and a run with `{"amount": 5}` produced
the output shown below.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Total the amounts",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "total",
      "name": "Total",
      "type": "kilasflow.jsCode",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "mode": "runOnceForAllItems",
        "jsCode": "const sum = items.reduce((acc, item) => acc + Number(item.json.amount), 0);\nreturn [{ json: { total: sum, count: items.length } }];"
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "total", "port": "main" }
    }
  ],
  "settings": {}
}
```

The Code node's output for `{"amount": 5}` is one item, `{ "total": 5, "count": 1 }`.

## Read the run in this order

```bash
kilasflow workflow validate --file total.json
kilasflow workflow create --file total.json --quiet
kilasflow run <workflowId> --wait --input '{"amount": 5}'
kilasflow exec get <executionId> --json
kilasflow exec trace <executionId>
kilasflow debug eval --execution <executionId> --node total '{{ $json.amount * 2 }}'
```

1. `exec get` is the record: the execution's `status` and `error`, then one entry
   in `nodeRuns` per node attempt, each with its `input`, `output` and `error`.
   The failing node is the `failed` node run with the lowest `sequence`.
2. `exec trace` is the event feed, ending at the terminal event, which is how a
   run that has not finished shows up.
3. `debug eval` evaluates an expression against the stored trace as the named
   node saw it: here `$json.amount * 2` against the item `Total` received, with
   no second run and no second side effect.

To see a quiet failure, change `item.json.amount` to `item.json.amout`: the sum
becomes `NaN`, which is stored as `total: null`, the run still ends `succeeded`,
and the node's output in the trace is the only place the mistake is visible.
That is why a green status is not a diagnosis.
