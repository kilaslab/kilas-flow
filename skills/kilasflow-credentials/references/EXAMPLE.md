# A validated credential body and the node that uses it

Two blocks. The request body is what `kilasflow credential create --file` sends;
the document is a workflow whose HTTP Request node names the credential it
authenticates with. Both are accepted by the product: the body by the real
`create-credential` handler, the document by `kilasflow workflow validate`.

Every value in `fields` below is a `<placeholder>`. The real value is supplied
once, by whoever owns it, into a file that is never printed, committed or pasted
into a chat; a skill, a document and a command line never carry it.

## The request body

```json kilasflow-request create-credential
{
  "name": "Upstream API key",
  "type": "httpHeaderAuth",
  "fields": {
    "name": "X-Api-Key",
    "value": "<the key, supplied by its owner>"
  },
  "allowedDomains": ["api.example.com"]
}
```

- `name` is the display name; `type` is a credential type id and is immutable
  after creation; `fields` holds the type's fields as strings. The types and
  their fields are served: `kilasflow api list-credential-types` prints every
  type with each field's `key`, whether it is `required` and whether it is
  `secret`.
- `allowedDomains` scopes where the credential may be sent. A generic HTTP type is
  unrestricted when it is empty, so name the host.
- Store it with `kilasflow credential create --file cred.json --yes`: the verb is
  guarded, so it refuses without `--yes`, which is passed only on the user's
  instruction. The answer carries the new `id` and the public half of the
  credential; the secret is never returned again.

## The workflow that uses it

```json kilasflow-workflow
{
  "schemaVersion": 1,
  "name": "Call an API with a stored credential",
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
        "url": "https://api.example.com/v1/me"
      },
      "credentials": { "httpHeaderAuth": "<credentialId>" }
    }
  ],
  "connections": [
    {
      "id": "c1",
      "kind": "main",
      "source": { "nodeId": "start", "port": "main" },
      "target": { "nodeId": "call", "port": "main" }
    }
  ],
  "settings": {}
}
```

- A node names a credential by its *type*, and the value is the credential's
  id: `"credentials": { "<credentialType>": "<credentialId>" }`. The document
  stores that id and nothing else, so no revision, export or trace holds the
  secret. Take the id from `kilasflow credential list`, never from a name.
- The credential types a node accepts are listed by `kilasflow node describe
  <type>` under its credentials.

## Check it

```bash
kilasflow api create-credential --schema
kilasflow credential create --file cred.json --yes
kilasflow credential test <credentialId>
kilasflow workflow validate --file call-api.json
```

`credential test` runs the type's own probe, which is the only check that a
stored credential works; a 2xx from the create proves nothing about it.
