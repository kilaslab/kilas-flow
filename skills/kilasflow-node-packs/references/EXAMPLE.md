# A validated document that uses a node from a pack

A pack node is used exactly like a built-in one: a `type`, a `typeVersion`,
parameters, and a credential named by id. This document sends a Telegram message
with `pack.telegram`, one of the two declarative packs the binary embeds, and
`kilasflow workflow validate` accepts it. It is the proof that a pack's node
reached the catalogue; writing the pack itself is `PACK_FORMAT.md`.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Tell the on-call chat",
  "nodes": [
    {
      "id": "start",
      "name": "Manual Trigger",
      "type": "kilasflow.manual",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 }
    },
    {
      "id": "tell",
      "name": "Send message",
      "type": "pack.telegram",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "resource": "message",
        "operation": "sendMessage",
        "chatId": "<chat id>",
        "text": { "mode": "expression", "value": "Run {{ $execution.id }} reached the chat" }
      },
      "credentials": { "telegramApi": "<credentialId>" }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "tell", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What it shows

- **A pack type is in the catalogue under its own prefix.** A pack never claims
  `kilasflow.`: this one is `pack.telegram`, and `kilasflow node list` shows its
  `source` as `pack`. Read what the deployment serves with
  `kilasflow node describe pack.telegram` instead of a manifest.
- **The pack names a credential type, not a credential.** The node declares
  `telegramApi`; the document carries the id of a credential of that type, under
  that key, and the operator's `<credentialId>` is whichever one you stored.
- **Parameters follow the definition.** `operation` decides which other fields
  are shown and required (`chatId` and `text` for `sendMessage`), so the
  definition's visibility rules apply to a pack node exactly as to a built-in
  one.
- **A pack node is only as available as its pack.** A directory pack is loaded
  at boot from `packs.dir`; a document naming a type that is not loaded fails
  validation with the unknown type named.

## Check it

```bash
kilasflow pack validate ./packs/acme
kilasflow node describe pack.telegram
kilasflow workflow validate --file tell.json
```

`pack validate` is local and checks a pack directory the way the server would
load it. The two embedded packs are always present; a pack of your own appears
after it is placed under the packs directory and the process restarts.
