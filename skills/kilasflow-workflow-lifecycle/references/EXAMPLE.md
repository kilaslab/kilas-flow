# A validated workflow document

A webhook that builds a greeting and answers the caller: three nodes, two
connections, and every field of the canonical document a write sends. It is the
document `kilasflow workflow validate` was run against when this file was
tested, so copy it and change it rather than writing one from memory.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Greeting webhook",
  "nodes": [
    {
      "id": "hook",
      "name": "Webhook",
      "type": "kilasflow.webhook",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 },
      "parameters": {
        "path": "greeting",
        "httpMethod": "POST",
        "responseMode": "responseNode"
      }
    },
    {
      "id": "greet",
      "name": "Build greeting",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "a1",
              "name": "greeting",
              "type": "string",
              "value": { "mode": "expression", "value": "Hello {{ $json.body.name }}" }
            }
          ]
        }
      }
    },
    {
      "id": "answer",
      "name": "Respond",
      "type": "kilasflow.respondToWebhook",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "respondWith": "firstIncomingItem",
        "responseCode": 200
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "hook", "port": "main" },
      "target": { "nodeId": "greet", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "greet", "port": "main" },
      "target": { "nodeId": "answer", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What each part is for

- **Top level.** `schemaVersion`, `name`, `nodes`, `connections` and `settings`
  are all present, and there is no `id`: the server assigns the workflow id on
  create and takes it from the path on update. A document read back with
  `kilasflow workflow get` carries an `id`; drop it before you send the document
  again.
- **A node.** `id` and `name` are yours (a connection names the `id`, an
  expression names the `name`), `type` and `typeVersion` pick the registered
  definition, and `position` is an object `{ "x": number, "y": number }` — an
  array such as `[0, 0]` is refused.
- **A connection.** `id`, `kind`, `source` and `target`, where each end names a
  `nodeId` and a `port`. Data between ordinary nodes is `kind: "main"` on the
  port named `main`; a node that offers other ports lists them in
  `kilasflow node describe <type>`.
- **A value that is computed.** `{ "mode": "expression", "value": "…" }` is an
  expression; a plain string that happens to contain braces is literal text
  (see the kilasflow-expressions skill).
- **A Set assignment.** Rows live under `assignments.assignments`, each with an
  `id`, a `name`, a `type` and a `value`. A bare array in `assignments` is
  refused.

## Check it, then save it

```bash
kilasflow workflow validate --file greeting.json
kilasflow workflow create --file greeting.json --quiet
kilasflow workflow get <workflowId>
kilasflow run <workflowId> --wait --input '{"body":{"name":"Ada"}}'
```

`validate` saves nothing and prints `valid` and a list of `diagnostics`, each
with a `field` path and a `reason`; fix every one before you create. After the
create, `workflow get` is the proof that the connections and parameters you
meant came back. A manual run takes the item a delivery would carry, so the
last command shows `greeting: "Hello Ada"` in the output without publishing
anything. The document answers a real caller only once it is active, and
activation publishes an endpoint, which is the user's decision.
