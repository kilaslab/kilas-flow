# A validated document that probes the installation

A Schedule trigger on a cron expression and an HTTP Request node that reads the
installation's readiness endpoint. It is the workflow-shaped version of
`kilasflow ready`: something outside the process asking, on a clock, whether the
instance can serve. `kilasflow workflow validate` accepts it. Replace the host
with the base URL of the instance being watched.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Probe readiness every five minutes",
  "nodes": [
    {
      "id": "tick",
      "name": "Every 5 minutes",
      "type": "kilasflow.schedule",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 },
      "parameters": {
        "rule": {
          "interval": [
            { "field": "cronExpression", "expression": "*/5 * * * *" }
          ]
        }
      }
    },
    {
      "id": "probe",
      "name": "Probe readiness",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "method": "GET",
        "url": "https://flow.example.com/api/v1/ready",
        "fullResponse": true,
        "neverError": true,
        "requestTimeoutSeconds": 10
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "tick", "port": "main" },
      "target": { "nodeId": "probe", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What it shows

- **Readiness, not liveness.** `/api/v1/health` answers `200` while the database
  is down; `/api/v1/ready` answers `503` when the instance cannot serve, and the
  `ready` verb carries that back as exit code 6. A probe that should route
  traffic reads readiness.
- **`neverError` keeps a 503 as data.** With it set, a non-2xx response becomes
  an item instead of a failure, and `fullResponse` puts `statusCode` beside the
  body, so the next node can branch on `$json.statusCode`. Without it the first
  503 ends the run and the failure is the only record.
- **A cron row stores `expression`.** `{ "field": "cronExpression",
  "expression": "*/5 * * * *" }`, read in the workflow's timezone setting,
  which defaults to UTC. The scheduler wakes every fifteen seconds, so a
  finer schedule fires once per wake.
- **This workflow runs inside the thing it watches.** If the instance is down the
  probe is down with it; an outside watcher is the right shape for an outage, and
  this one is for a dependency that fails while the process lives. The default
  egress policy also refuses a loopback or private target, so point it at the
  instance's public name or have the operator grant that one endpoint.

## Check it

```bash
kilasflow ready
kilasflow workflow validate --file probe.json
kilasflow workflow create --file probe.json --quiet
kilasflow run <workflowId> --wait --trigger tick
```

`ready` is the same question asked once, from a shell: exit 0 is ready, exit 6 is
"wait and retry". The workflow only starts asking on its own after the user has
activated it with `kilasflow workflow activate <workflowId> --yes`.
