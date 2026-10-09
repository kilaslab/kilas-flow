# A validated document that starts itself

A Schedule trigger that fires every fifteen minutes and calls an upstream
health endpoint. It is the smallest document whose first node is something other
than a person: nothing runs it until the workflow is active, and `kilasflow run`
starts it by hand for a test. `kilasflow workflow validate` accepts it; replace
the placeholder host with your own endpoint before a run is expected to succeed.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Poll the upstream every 15 minutes",
  "nodes": [
    {
      "id": "tick",
      "name": "Every 15 minutes",
      "type": "kilasflow.schedule",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 },
      "parameters": {
        "rule": {
          "interval": [
            { "field": "minutes", "minutesInterval": 15 }
          ]
        }
      }
    },
    {
      "id": "poll",
      "name": "Poll upstream",
      "type": "kilasflow.httpRequest",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "method": "GET",
        "url": "https://status.example.com/api/health"
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "tick", "port": "main" },
      "target": { "nodeId": "poll", "port": "main" }
    }
  ],
  "settings": { "timezone": "Asia/Jakarta" }
}
```

## What it shows

- **A trigger is a node with no input.** The Schedule node has an output port and
  nothing connects into it. A document may hold several triggers (a webhook and a
  schedule on the same graph); a bare `kilasflow run` then starts all of them,
  and `--trigger <nodeId>` names one.
- **Intervals are rows in a fixed collection.** `rule.interval` is a list, each
  row choosing a `field` (`seconds`, `minutes`, `hours`, `days`, `weeks`,
  `months` or `cronExpression`) and the settings that field shows. Two rules on
  one trigger are two rows, not two triggers. A cron row stores
  `{ "field": "cronExpression", "expression": "0 9 * * 1-5" }`.
- **Times are read in the workflow's timezone.** `settings.timezone` is where
  that is set; it falls back to UTC.
- **Nothing fires until it is active.** Saving does not create the schedule row;
  `kilasflow workflow activate <workflowId> --yes` does, and it is a guarded verb
  that the user must ask for.

## Check it

```bash
kilasflow workflow validate --file poll.json
kilasflow workflow create --file poll.json --quiet
kilasflow run <workflowId> --wait --trigger tick
kilasflow schedule list
```

`run --trigger tick` proves the graph without activating anything. Once the user
has activated the workflow, `schedule list` shows its row with `cron`, `active`
and `nextRunAt`, which is where to look first when nothing fires.
