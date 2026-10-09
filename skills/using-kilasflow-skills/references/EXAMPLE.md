# A validated document to start every session from

The smallest workflow that is connected and does something: a manual trigger and a
Set node that writes one field. Every other skill's example is a larger version of
this shape, so when you do not yet know which skill you need, copy this, run it,
and read the trace. `kilasflow workflow validate` accepts it, and a run of it
ends `succeeded` with the greeting in the Set node's output.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Hello from KilasFlow",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "hello",
      "name": "Say hello",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            { "id": "a1", "name": "greeting", "type": "string", "value": "hello" }
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
      "target": { "nodeId": "hello", "port": "main" }
    }
  ],
  "settings": {}
}
```

## The loop this document is for

```bash
kilasflow context
kilasflow workflow validate --file hello.json
kilasflow workflow create --file hello.json --quiet
kilasflow run <workflowId> --wait
kilasflow exec get <executionId> --json
```

1. `context` says what the server is and what already exists.
2. `validate` compiles the draft and saves nothing; fix its diagnostics first.
3. `create` saves it and prints the workflow id (`--quiet` prints only the id).
4. `run --wait` starts a manual run and follows it to a terminal status.
5. `exec get` is the record to read back, including each node's output.

From here, the decision tree in this skill names the skill for the next step: a
trigger, a node's parameters, an expression, a credential, a table.
