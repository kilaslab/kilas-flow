# A validated table definition and a workflow that fills and reads it

Two blocks. The request body creates a data table with its columns in one call;
the document inserts the incoming item into that table with automatic column
mapping and then reads the row back with a filter. The body is sent to the real
`create-datastore` handler by the example harness, and the document is accepted
by `kilasflow workflow validate`. Replace `<datastoreId>` with the id the create
answered.

## The table

```json kilasflow-request create-datastore
{
  "name": "contacts",
  "columns": [
    { "name": "email", "type": "string" },
    { "name": "name", "type": "string" },
    { "name": "subscribed", "type": "boolean" }
  ]
}
```

- A column `type` is `string`, `number`, `boolean` or `date`. A column name is 1 to
  63 bytes of letters, digits and underscores starting with a letter, and `id`,
  `createdAt`, `updatedAt` and `dryRunState` are reserved.
- `kilasflow datastore create <name> --yes` makes the table without columns and
  `kilasflow datastore columns add <datastoreId> <name> --type string --yes` adds
  them one at a time; the body above does it in one call with
  `kilasflow api create-datastore --body @contacts.json --yes`. Both are guarded,
  so ask the user first.
- The answer carries the table's `id` and its `columns`. Read it again with
  `kilasflow datastore get <datastoreId>` before writing a row: that is the only
  thing that says which columns exist.

## The workflow

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Save a contact and read it back",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "save",
      "name": "Save contact",
      "type": "kilasflow.datastore",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "resource": "row",
        "operation": "insert",
        "dataTableId": { "__rl": true, "mode": "id", "value": "<datastoreId>" },
        "columns": {
          "mappingMode": "autoMapInputData",
          "value": {},
          "matchingColumns": []
        }
      }
    },
    {
      "id": "find",
      "name": "Find contact",
      "type": "kilasflow.datastore",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "resource": "row",
        "operation": "get",
        "dataTableId": { "__rl": true, "mode": "id", "value": "<datastoreId>" },
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
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "save", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "save", "port": "main" },
      "target": { "nodeId": "find", "port": "main" }
    }
  ],
  "settings": {}
}
```

- **The table is a locator.** `dataTableId` is stored as `{ "__rl": true, "mode":
  "id", "value": "…" }`; mode `name` finds the table by its name in the tenant's
  own catalogue instead.
- **`autoMapInputData`** takes the incoming item's keys that match the live
  columns; `defineBelow` sets each column by hand under `value`. Either way a
  column name is a literal, never an expression, and only a value may be one.
- **A filter is a fixed collection.** `filters.conditions` is a list of
  `keyName`, `condition` and `keyValue` rows, and `match` says whether `any` or
  `all` of them must hold. A write that matches rows (`update`, `upsert`,
  `delete`, `increment`) needs at least one condition: an empty filter is refused
  rather than meaning every row.

## Check it

```bash
kilasflow api create-datastore --schema
kilasflow datastore get <datastoreId>
kilasflow workflow validate --file contacts.json
kilasflow run <workflowId> --wait --input '{"email":"ada@example.com","name":"Ada","subscribed":true}'
kilasflow datastore rows <datastoreId>
```

`rows` shows the row the run wrote. Rows themselves are written and filtered
through `kilasflow api` operations or the node; there is no row-write verb.
