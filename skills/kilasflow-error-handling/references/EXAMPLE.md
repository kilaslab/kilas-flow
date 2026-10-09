# A validated document with an error branch

An HTTP Request node that retries twice and, when it still fails, sends the
failed item down its own `error` port into a Stop and Error node instead of
ending the run on the spot. `kilasflow workflow validate` accepts it; the `error`
port exists only because the node's `settings.onError` is
`continueErrorOutput`, so a connection from it compiles. Replace the placeholder
host with your own endpoint before a run is expected to succeed.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Call an API and fail loudly",
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
      "name": "Call API",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "method": "GET",
        "url": "https://api.example.com/v1/status"
      },
      "settings": {
        "retryOnFail": true,
        "maxTries": 2,
        "waitBetweenTries": 2000,
        "onError": "continueErrorOutput"
      }
    },
    {
      "id": "ok",
      "name": "Mark success",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 480, "y": -80 },
      "parameters": {
        "assignments": {
          "assignments": [
            { "id": "a1", "name": "outcome", "type": "string", "value": "reached" }
          ]
        }
      }
    },
    {
      "id": "fail",
      "name": "Stop with a message",
      "type": "kilasflow.stopAndError",
      "typeVersion": 1,
      "position": { "x": 480, "y": 80 },
      "parameters": {
        "errorMessage": { "mode": "expression", "value": "Upstream call failed: {{ $json.error.message }}" }
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
      "target": { "nodeId": "ok", "port": "main" }
    },
    {
      "id": "c3",
      "kind": "main",
      "source": { "nodeId": "call", "port": "error" },
      "target": { "nodeId": "fail", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What it shows

- **Retry is a node setting, not a node.** `retryOnFail`, `maxTries` and
  `waitBetweenTries` sit in `settings`, never in `parameters`. `maxTries` is
  bounded at 8 and the wait at 300000 ms, and a value outside either is refused
  when the document compiles.
- **`onError` chooses what a failure costs.** `continueErrorOutput` routes each
  failed item to the node's `error` port (the port appears only in this mode);
  `continueRegularOutput` keeps failures on `main`; the default
  `stopWorkflow` ends the run.
- **The error item is data.** Behind the `error` port the item carries `error`
  with a `message`, so the Stop and Error message can quote it with an
  expression.
- **A graph can fail on purpose.** Stop and Error ends the run as `failed`, which
  is also what starts the workflow named in `settings.errorWorkflow`, if the
  failing workflow has one.

## Check it

```bash
kilasflow workflow validate --file call-api.json
kilasflow workflow create --file call-api.json --quiet
kilasflow run <workflowId> --wait
kilasflow exec get <executionId> --json
```

With the placeholder host the run ends `failed` at `Stop with a message`, which
is the branch working: `exec get` shows `Call API` failing, the error item it put
on its `error` port, `Mark success` skipped, and the Stop and Error node as the
run's failure with the upstream message quoted in its own.
