# A validated document that computes values

Two Set nodes. The first normalises an incoming email and stamps the day; the
second reads the first node by name and also carries a string that is *not* an
expression, so both behaviours sit side by side. `kilasflow workflow validate`
accepts it, and a run with `{"email":"ADA@Example.com"}` produced the output
shown below.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Expressions in a Set node",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "clean",
      "name": "Normalise",
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
              "value": { "mode": "expression", "value": "{{ $json.email.toLowerCase() }}" }
            },
            {
              "id": "a2",
              "name": "seenOn",
              "type": "string",
              "value": { "mode": "expression", "value": "{{ $now.toFormat('yyyy-MM-dd') }}" }
            }
          ]
        }
      }
    },
    {
      "id": "say",
      "name": "Greeting",
      "type": "kilasflow.set",
      "typeVersion": 1,
      "position": { "x": 480, "y": 0 },
      "parameters": {
        "assignments": {
          "assignments": [
            {
              "id": "b1",
              "name": "message",
              "type": "string",
              "value": {
                "mode": "expression",
                "value": "Welcome {{ $node[\"Normalise\"].json.email }}, seen on {{ $json.seenOn }}"
              }
            },
            {
              "id": "b2",
              "name": "literal",
              "type": "string",
              "value": "{{ this stays text }}"
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
      "target": { "nodeId": "clean", "port": "main" }
    },
    {
      "id": "c2",
      "kind": "main",
      "source": { "nodeId": "clean", "port": "main" },
      "target": { "nodeId": "say", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What it shows

- **The marker is what makes it an expression.** `{ "mode": "expression",
  "value": "{{ … }}" }` is evaluated. The `literal` assignment holds a plain
  string with braces in it and comes out unchanged, as `{{ this stays text }}`;
  writing `"value": "{{ $json.email }}"` on its own stores text, not a lookup.
- **`$json` is the current item.** `$json.email.toLowerCase()` calls a method on
  a field, and `$now.toFormat('yyyy-MM-dd')` formats the clock with Luxon
  tokens, not a Go layout.
- **Another node is read by its name.** `$node["Normalise"].json.email` names
  the node's `name`, not its `id`; renaming that node breaks the expression.
- **Quotes inside JSON are escaped.** The inner `"Normalise"` is `\"Normalise\"`
  in the document, or write the call form with single quotes.

With `{"email":"ADA@Example.com"}` the last node outputs:

```json
{
  "email": "ada@example.com",
  "seenOn": "2026-10-09",
  "message": "Welcome ada@example.com, seen on 2026-10-09",
  "literal": "{{ this stays text }}"
}
```

## Check it

```bash
kilasflow workflow validate --file expressions.json
kilasflow workflow create --file expressions.json --quiet
kilasflow run <workflowId> --wait --input '{"email":"ADA@Example.com"}'
kilasflow debug eval --execution <executionId> --node say '{{ $json.seenOn }}'
```

`debug eval` evaluates an expression against the stored trace of a finished run,
as the named node saw it, so a new reference can be tried without another run.
It sees the node's *input*: `$json.seenOn` is there, `$json.message` is what the
node is about to write and is not.

The roots and functions a body may use are served, not listed here:
`kilasflow api get-expression-grammar` prints them for the instance in front of
you.
