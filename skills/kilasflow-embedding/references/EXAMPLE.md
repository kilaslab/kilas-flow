# A validated document an embed session is minted for

The workflow a host product creates for one customer and then opens in the
iframe editor: a webhook that answers every caller with a fixed acknowledgement.
It is the smallest document an embed session can be scoped to, because a session
names exactly one `workflowId`. `kilasflow workflow validate` accepts it.

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Lead intake for customer acme",
  "nodes": [
    {
      "id": "intake",
      "name": "Lead webhook",
      "type": "kilasflow.webhook",
      "typeVersion": 1,
      "position": { "x": 0, "y": 0 },
      "parameters": {
        "path": "lead",
        "httpMethod": "POST",
        "responseMode": "responseNode"
      }
    },
    {
      "id": "ack",
      "name": "Acknowledge",
      "type": "kilasflow.respondToWebhook",
      "typeVersion": 1,
      "position": { "x": 240, "y": 0 },
      "parameters": {
        "respondWith": "json",
        "responseCode": 202,
        "responseBodyJSON": "{\"received\": true}"
      }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "intake", "port": "main" },
      "target": { "nodeId": "ack", "port": "main" }
    }
  ],
  "settings": {}
}
```

## What it is for

- **One session, one workflow.** The editor session body names a `workflowId`,
  the scopes it grants and the exact `origin` of the page that frames the editor;
  nothing else in the tenant is reachable with the token. Create the workflow
  with the customer's own key, so it lands in the customer's tenant, and mint
  the session for the id the create answered.
- **A public trigger is a decision.** The Webhook's `authentication` defaults to
  `none`, which makes the endpoint callable by anyone who learns its address, and
  a deployment that sets `webhook.require_auth` refuses it outright. For a
  customer-facing endpoint set `basicAuth`, `headerAuth` or `jwtAuth` and attach
  a credential of that type.
- **Activation is a scope the host grants on purpose.** Saving the draft needs
  `workflow:write`; publishing a revision needs `workflow:publish` and turning the
  workflow on needs `workflow:activate`, which only an embed session can carry
  and a scoped API key cannot. Without it, the host's backend key does the
  activation when the customer asks for it. Deleting and importing are refused to
  a session at any scope.

## Check it

```bash
kilasflow auth whoami
kilasflow workflow validate --file lead-intake.json
kilasflow workflow create --file lead-intake.json --quiet
kilasflow api create-embed-session --schema
```

`auth whoami` proves which tenant the configured key acts as before anything is
created, and `--schema` prints the request body the session mint takes. The
session itself is minted with `kilasflow api create-embed-session --body
@session.json` using the customer's key, then handed to the page, never to the
frame before it announces itself.
