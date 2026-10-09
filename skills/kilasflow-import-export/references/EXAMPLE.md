# A validated document that survives the trip to n8n and back

A manual trigger, an HTTP Request node and a Set node, all of them types the
exporter maps to n8n nodes one for one. The example harness creates this
document, exports it with the n8n format, requires `lossy` to be empty, and
imports the exported file again, requiring that the only `unsupported` entry is
the one `dropped` `settings` field (see below) and that every node comes back
with the type and name it left with. That is what "round-trips" means here, and
it is why this document uses only mapped node types: a node outside the mapping
table leaves the export as itself and shows up in `lossy`.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Fetch a record and label it",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "fetch",
      "name": "Fetch record",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "method": "GET",
        "url": "https://api.example.com/v1/records/42"
      }
    },
    {
      "id": "label",
      "name": "Label record",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "a1",
              "name": "label",
              "type": "string",
              "value": { "mode": "expression", "value": "record {{ $json.id }}" }
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
      "target": { "nodeId": "fetch", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "fetch", "port": "main" },
      "target": { "nodeId": "label", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What changes at the border

- **Expressions change marker.** n8n marks an expression with a leading `=` on a
  plain string; KilasFlow marks one with `{ "mode": "expression", "value": "…" }`.
  The `{{ … }}` inside is the same, so the exported `label` is
  `"=record {{ $json.id }}"` and the importer turns it back.
- **Node types change names.** `kilasflow.httpRequest` leaves as
  `n8n-nodes-base.httpRequest` at the version whose parameter shape the exporter
  translated, and comes back as `kilasflow.httpRequest`. `supportedMappings[]`
  in the export lists every pair this server stands behind; read it instead of
  recalling a table.
- **Connections change shape.** The canonical list of edges with ids becomes
  n8n's map keyed by node *name*, so two nodes sharing a name cannot cross the
  border.
- **One field is dropped on the way back.** The exporter writes n8n's
  workflow-level `settings.executionOrder`, which KilasFlow has no equivalent
  for, so the import reports it as `dropped` at `settings`. It is a severity
  that costs nothing; a `lossy` or `blocking` entry is the one to act on.
- **Credentials are not exported.** A node's `credentials` map is dropped from
  the file, and the receiving side attaches its own.

## Check it

```bash
kilasflow workflow create --file fetch.json --quiet
kilasflow workflow export <workflowId> --format n8n
kilasflow api export-workflow --path id=<workflowId> --query format=n8n --out workflow.json
```

The first export prints the n8n `workflow`, `lossy[]` and `supportedMappings[]`.
An empty `lossy[]` is the claim this example makes; any entry in it names the
node and what the file could not carry. Bringing a file in goes the other way:
`kilasflow api import-workflow --body @import.json` with the body
`{"format": "n8n", "workflow": <the file>}`, and read `unsupported[]` in the
answer before trusting the draft.
