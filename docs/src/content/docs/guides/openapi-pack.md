---
title: Generate a node pack from OpenAPI
description: Turn an OpenAPI 3 document into a declarative node pack with nodepackgen, supply the decisions the document cannot make in a generation manifest, and read the report that names everything the pack does not contain.
---

If your product already describes its API in an OpenAPI 3 document, a node
pack can be generated from it rather than written by hand. The generator is
`nodepackgen`, the same binary that
[scaffolds and validates hand-written packs](/guides/node-authoring/); the
packs it generates are ordinary declarative packs — the shipped WAHA packs
under `packs/waha/` are its output, and everything in the
[format reference](/reference/node-packs/) applies to them unchanged.

The generator never guesses. The document names the operations; a small
generation manifest supplies the decisions a document cannot make — what the
node is called, which credential type carries its authentication, which
defaults the document omitted. Anything the document contained that the pack
does not is listed in a report, because a pack that looks complete and is not
is the worst outcome available.

One honest note before the recipe. `nodepackgen -h` prints only the
generator's flags — the flag-only usage banner predates the `scaffold`,
`validate` and `pack` subcommands and does not list them. Both forms exist:

| Invocation | What it does |
| --- | --- |
| `nodepackgen -spec … -manifest … -out …` | generate a pack from an OpenAPI document |
| `nodepackgen scaffold -dir …` | write a minimal hand-written pack |
| `nodepackgen validate <dir or pack.json>…` | validate packs against the loader's rules |
| `nodepackgen pack -dir …` | write the checksum sidecar for installation |

## The generation manifest

The manifest is a small JSON file. It is *not* the pack manifest: the pack
(`pack.json`) is generated output, committed and reviewed; this file is the
input that says what to generate. Every field:

| Field | Required | Meaning |
| --- | --- | --- |
| `type` | yes | The node type the pack registers, e.g. `pack.acmeNotes`. Must not claim the reserved `kilasflow.` namespace. |
| `version` | yes | The type version — an integer like `1`, or a calendar stamp like `202502` when the API versions by date. |
| `displayName` | yes | Picker name. |
| `description` | no | Picker description. |
| `category` | yes | Picker section. |
| `icon`, `iconColor`, `subtitle`, `documentationUrl` | no | Presentation, same meanings as in a hand-written pack. |
| `baseUrl` | no | Overrides the document's first server. A self-hosted product has no fixed host, so this is normally a literal or a template over a credential field — the WAHA manifest uses `{{ $credentials.baseUrl }}`. If omitted, the document's first `servers` entry is used; a document with neither is a refusal. |
| `security` | yes when the document declares schemes | Maps each OpenAPI security scheme name to a KilasFlow credential type. A scheme the document uses and this map does not name is **fatal** — deliberately, because a pack that generates cleanly and then cannot authenticate is worse than one that fails here. With several schemes the first (in sorted order) is used and the report notes the rest are unreachable. |
| `skipDeprecated` | no | Leaves operations marked `deprecated` out of the pack, and the report says so. |
| `parameterDefaults` | no | Overrides a parameter's default by key. This is not a convenience: some generators inject defaults that are nowhere in the document (WAHA's `session` and `chatId` are filled from the trigger item), and a pack that treated absent as empty would produce workflows that look correct, activate, and send nothing. A parameter with a manifest default stops being required. |
| `trigger` | no | Produces a second pack — a webhook trigger — from the same document, in the same run. See [below](#generating-a-trigger-from-the-same-document). |

## A worked example

A four-operation document is enough to see every rule. Save this as
`spec.json`:

```json
{
  "openapi": "3.1.0",
  "info": { "title": "Acme Notes", "version": "1.0.0" },
  "servers": [{ "url": "https://api.acme.example" }],
  "security": [{ "apiKeyAuth": [] }],
  "components": {
    "securitySchemes": {
      "apiKeyAuth": { "type": "http", "scheme": "bearer" }
    }
  },
  "paths": {
    "/notes": {
      "get": {
        "operationId": "listNotes", "summary": "List notes", "tags": ["notes"],
        "responses": { "200": { "description": "The notes" } }
      },
      "post": {
        "operationId": "createNote", "summary": "Create a note", "tags": ["notes"],
        "requestBody": { "required": true, "content": { "application/json": { "schema": {
          "type": "object",
          "properties": { "title": { "type": "string" }, "body": { "type": "string" } },
          "required": ["title"]
        } } } },
        "responses": { "201": { "description": "Created" } }
      }
    },
    "/notes/{id}": {
      "get": {
        "operationId": "getNote", "summary": "Read one note", "tags": ["notes"],
        "parameters": [{ "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }],
        "responses": { "200": { "description": "The note" } }
      },
      "delete": {
        "operationId": "deleteNote", "summary": "Delete one note", "deprecated": true, "tags": ["notes"],
        "parameters": [{ "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }],
        "responses": { "204": { "description": "Deleted" } }
      }
    }
  }
}
```

And this manifest as `manifest.json`:

```json
{
  "type": "pack.acmeNotes",
  "version": 1,
  "displayName": "Acme Notes",
  "category": "Productivity",
  "security": { "apiKeyAuth": "httpBearerAuth" },
  "skipDeprecated": true
}
```

Build the tool from the repository (`go build ./cmd/nodepackgen`, or run it
out of the container image at `/app/nodepackgen`) and generate:

```sh
$ nodepackgen -spec spec.json -manifest manifest.json -out pack.json -report REPORT.md
nodepackgen: 3 operations in 1 resources, 1 skipped, 0 notes
```

The generated `pack.json` is an ordinary declarative pack: one resource
(`Notes`, from the `notes` tag), three operations (`List Notes`, `Create
Note`, `Get Note`, from the operation ids), `credentialType:
"httpBearerAuth"` (from the security mapping), and a parameter per path,
query and first-level body property — `id` is required because a path
parameter always is, `title` because the document says so, `body` not.
Deleting the deprecated operation was the manifest's choice, and the report
says so.

## Reading the report

The report is not a courtesy — it is the part of the output a reviewer reads
first, because anything it lists is **absent from the pack**:

```markdown
# Node pack generation report

- Source: `spec.json`
- SHA-256: `fec06923fcda3f40af7adae323176826e72f863076cdc24e5e25ce8171e9b03c`
- API: Acme Notes 1.0.0
- Operations generated: 3

## Operations left out (1)

- DELETE /notes/{id}: deprecated

## Degraded or merged (0)

None.
```

Two lists, with different meanings. **Operations left out** are absent
entirely — no tag, no operationId, deprecated and skipped, a request body
that is not `application/json`, a `oneOf`/`anyOf` body with no single
parameter set, or a body with no named properties. **Degraded or merged**
are present but changed — a parameter declared as two kinds by different
operations (generated as a string), different option sets (same), a header
or cookie parameter the interpreter cannot place (operation generated without
it), or a manifest default applied over the document's. Regeneration is
byte-identical, so the report and the pack cannot disagree.

## Naming: how a document becomes a resource

The resource is the operation's first tag and the operation name is its
operation id, both start-cased — and the first `_`-separated segment of an
operation id is dropped, because the generators that produce these documents
name operations `ControllerName_methodName` and the controller is already the
tag. `listNotes` under tag `notes` is resource `Notes`, operation
`List Notes`. This reproduces lodash's `startCase` exactly, which is
load-bearing: an imported workflow matches a pack's `resource`/`operation`
options by literal string, and one character out selects nothing — silently.

## Generating a trigger from the same document

When your API also delivers events, the manifest's `trigger` block produces a
second pack — a webhook trigger node — in the same run, so the action pack
and the trigger pack can never be generated against different versions of the
document. The block is a full pack manifest for the trigger (its own `type`,
`displayName`, `credentialType`, `trigger` declaration with `hmac`,
`lifecycle`, `media` as you would write them by hand — the
[authoring guide's trigger section](/guides/node-authoring/#step-5-triggers)
covers each), plus one field the generator fills:

```json
"trigger": {
  "eventsFrom": { "schema": "WAHAWebhookMessage", "property": "event" },
  "type": "pack.acmeNotesTrigger",
  "version": 1,
  "displayName": "Acme Notes Trigger",
  "category": "Triggers",
  "credentialType": "httpBearerAuth",
  "trigger": {
    "events": [],
    "catchAll": "other",
    "eventPath": "event",
    "shape": "bodyAsItem",
    "webhook": { "name": "default", "pathParameter": "path", "method": "POST" }
  }
}
```

`eventsFrom` names the component schema and the property whose enum is the
event list. The generator reads the enum in document order and fills
`trigger.events` with it — never sorted, because the order is the contract:
an output index is what a workflow's connections address. The shipped
`packs/waha/manifest-202502.json` is a complete real example, and its trigger
also declares an `hmac` scheme and a registration lifecycle.

## From generated pack to running node

A generated pack is installed like any other:

```sh
$ mkdir -p packs/acme-notes && cp pack.json packs/acme-notes/
$ nodepackgen pack -dir packs/acme-notes
nodepackgen: wrote packs/acme-notes/pack.sha256
$ nodepackgen validate packs/acme-notes
nodepackgen: packs are valid
```

Then restart the server with `packs.dir` pointing at the parent directory —
the full steps, and what the operator accepts by approving the checksum, are
in [Build nodes for your API](/guides/build-nodes-for-your-api/). The pack's
`generator` provenance block names the source document and its SHA-256; do
not copy it into a hand-written pack, and do not hand-edit a generated pack —
change the manifest or the document and regenerate.

## Regenerating

Generation is deterministic: same spec, same manifest, same bytes. That is
what makes a generated pack committable and reviewable, and what makes drift
detectable — regenerate and the diff is exactly what changed in the document.
The repository's own packs are regenerated by `make node-packs` from the
vendored WAHA specs, and their `REPORT-*.md` files are committed beside them,
so a review of a spec upgrade sees the operations that appear and disappear
in the report before it reads the pack.
