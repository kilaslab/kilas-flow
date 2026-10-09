# One workflow calling another

Two documents. The child starts with a trigger that another workflow fires; the
parent calls it with an Execute Sub-workflow node and carries on with what it
returns. Both are accepted by `kilasflow workflow validate`. Replace
`<childWorkflowId>` in the parent with the id the child's create answered.

## The child

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Price an order line",
  "nodes": [
    {
      "id": "entry",
      "name": "When called",
      "type": "kilasflow.executeWorkflowTrigger",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 },
      "parameters": {
        "inputSource": "fields",
        "workflowInputs": {
          "values": [
            { "name": "quantity", "type": "number" },
            { "name": "unitPrice", "type": "number" }
          ]
        }
      }
    },
    {
      "id": "price",
      "name": "Compute total",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "a1",
              "name": "total",
              "type": "number",
              "value": { "mode": "expression", "value": "{{ $json.quantity * $json.unitPrice }}" }
            }
          ]
        },
        "include": "all"
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "entry", "port": "main" },
      "target": { "nodeId": "price", "port": "main" }
    }
  ],
  "settings": {}
}
```

## The parent

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Price an order",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "call",
      "name": "Price the line",
      "type": "kilasflow.executeWorkflow",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "workflowId": { "__rl": true, "mode": "id", "value": "<childWorkflowId>" },
        "mode": "each",
        "itemsPerCall": "eachItem"
      }
    },
    {
      "id": "report",
      "name": "Label the result",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "b1",
              "name": "summary",
              "type": "string",
              "value": { "mode": "expression", "value": "total is {{ $json.total }}" }
            }
          ]
        }
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "call", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "call", "port": "main" },
      "target": { "nodeId": "report", "port": "main" }
    }
  ],
  "settings": {}
}
```

## The rules this pair shows

- **The child begins with `kilasflow.executeWorkflowTrigger`.** It has no input
  and receives the items the caller sent. With `inputSource: "passthrough"` it
  accepts whatever arrives; with `"fields"` it lists the fields it expects, as
  documentation and a check, never a filter: an item with more fields still
  arrives whole.
- **The parent names the child by id.** `workflowId` is a resource locator, stored
  as `{ "__rl": true, "mode": "id", "value": "…" }`. A bare string is the legacy
  shape and is read, but write the object.
- **The child must be active.** A sub-workflow run is pinned to the called
  workflow's active revision. A child that was only saved fails the calling node
  at run time ("repository record not found: active workflow"), and activating
  the parent while the child is inactive is refused too; the server log names the
  child, but the API answers only a generic error, so when an activation of a
  caller fails, check its callees first. Activating is a guarded verb the user
  must ask for: `kilasflow workflow activate <childWorkflowId> --yes`, then the
  parent. A test run of the parent needs the child active as well.
- **`mode` chooses what comes back.** `each` is "wait for the sub-workflow to
  finish" and its items replace this node's output; `fireAndForget` runs it and
  lets the incoming items pass through unchanged. `itemsPerCall` is
  `allItems` (one run with the whole batch) or `eachItem` (one run per item).
- **`inputFields` shapes what is sent.** A key/value object of field name to
  value, expressions allowed, evaluated once per item; empty sends the incoming
  items unchanged.
- **Each call is its own execution.** The child's run is a row of its own with
  `parentExecutionId`, so a failure inside it is read in the child's trace, not
  in the parent's: `kilasflow exec list --workflow <childWorkflowId>`.

## Check it

```bash
kilasflow workflow validate --file child.json
kilasflow workflow create --file child.json --quiet
kilasflow workflow activate <childWorkflowId> --yes
kilasflow workflow validate --file parent.json
kilasflow workflow create --file parent.json --quiet
kilasflow run <parentWorkflowId> --wait --input '{"quantity":3,"unitPrice":4}'
```

The activation line is the one to ask the user about. With both saved, the
parent's run ends `succeeded` and its last node outputs `summary: "total is 12"`.
