# A validated document with every complex property kind

Four connected nodes that store each shape an agent gets wrong without seeing it
once: an assignment collection, a resource locator, a resource mapper, a fixed
collection and two key/value properties. `kilasflow workflow validate` accepts
this document, and its datastore row was written by a real run, so the stored
shapes are the ones the executors read. Replace `<datastoreId>` with an id from
`kilasflow datastore list` and the host in the URL with your own API.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Record a signup and look it up",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "shape",
      "name": "Shape signup",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "a1",
              "name": "email",
              "type": "string",
              "value": { "mode": "expression", "value": "{{ $json.email }}" }
            },
            { "id": "a2", "name": "plan", "type": "string", "value": "free" }
          ]
        }
      }
    },
    {
      "id": "save",
      "name": "Save signup",
      "type": "kilasflow.datastore",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "resource": "row",
        "operation": "upsert",
        "dataTableId": { "__rl": true, "mode": "id", "value": "<datastoreId>" },
        "columns": {
          "mappingMode": "defineBelow",
          "value": {
            "email": { "mode": "expression", "value": "{{ $json.email }}" },
            "plan": { "mode": "expression", "value": "{{ $json.plan }}" }
          },
          "matchingColumns": ["email"]
        },
        "match": "all",
        "filters": {
          "conditions": [
            {
              "keyName": "email",
              "condition": "eq",
              "keyValue": { "mode": "expression", "value": "{{ $json.email }}" }
            }
          ]
        }
      }
    },
    {
      "id": "lookup",
      "name": "Look up account",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 720, "y": 0 },
      "parameters": {
        "method": "GET",
        "url": "https://api.example.com/v1/accounts",
        "sendQuery": true,
        "queryParameters": {
          "email": { "mode": "expression", "value": "{{ $json.email }}" }
        },
        "sendHeaders": true,
        "headers": {
          "Accept": "application/json",
          "X-Source": "kilasflow"
        }
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "shape", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "shape", "port": "main" },
      "target": { "nodeId": "save", "port": "main" }
    },
    {
      "id": "c3",
      "kind": "main",
      "source": { "nodeId": "save", "port": "main" },
      "target": { "nodeId": "lookup", "port": "main" }
    }
  ],
  "settings": {}
}
```

## The stored shape of each kind

| Kind | Where it is above | Stored as |
| --- | --- | --- |
| `assignmentCollection` | `shape` · `assignments` | an object holding the list: `{ "assignments": [ { "id", "name", "type", "value" } ] }`. A bare array is refused with `assignments must be a non-empty object`. `type` is `string`, `number`, `boolean`, `array` or `object`. |
| `resourceLocator` | `save` · `dataTableId` | `{ "__rl": true, "mode": "id", "value": "…" }`. The `__rl` sentinel is what marks it; `mode` is one of the modes `node describe` lists (`list`, `name`, `id` here). |
| `resourceMapper` | `save` · `columns` | `{ "mappingMode": "defineBelow", "value": { "<column>": <value or expression> }, "matchingColumns": [ "<column>" ] }`. `autoMapInputData` takes the item's own keys instead of `value`. |
| `fixedCollection` | `save` · `filters` | an object keyed by the group name, each holding a list of rows: `{ "conditions": [ { "keyName", "condition", "keyValue" } ] }`. |
| `keyValue` | `lookup` · `headers`, `queryParameters` | a flat object of names to values: `{ "Accept": "application/json" }`. A value may be an expression. |

Anything computed is `{ "mode": "expression", "value": "{{ … }}" }`; a column
name, an operator and a match list are never expressions.

## Check it

```bash
kilasflow node describe kilasflow.datastore
kilasflow datastore list
kilasflow workflow validate --file signup.json
```

`node describe` prints every key, kind and option the document above uses, so
compare a node you are writing with it rather than with this file. After a
successful `workflow create`, `kilasflow run <workflowId> --wait --input '{"email":"ada@example.com"}'`
writes the row; read it back with `kilasflow datastore rows <datastoreId>`.
