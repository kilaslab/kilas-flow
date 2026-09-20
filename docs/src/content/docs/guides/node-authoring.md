---
title: Authoring a community node pack
description: Write a declarative node pack as JSON, check it with nodepackgen validate, and install it with a checksum the operator approves.
---

A node pack is JSON, not code. You describe resources, operations, parameters
and routing metadata; the server interprets them at run time. There is no Go
to write, no SDK to import, and nothing to compile. The whole flow is three
steps — **scaffold**, **validate**, **pack** — and every sample on this page is
a real file that `nodepackgen validate` accepts.

`nodepackgen` is a second binary, not a subcommand of `kilasflow`: the server
binary takes flags only, so `kilasflow pack validate …` refuses the argument
rather than doing anything. Build the tool from the repository with
`go build ./cmd/nodepackgen`, or run it out of the container image, which ships
it at `/app/nodepackgen`.

The reference to imitate is `packs/telegram/pack.json`: a hand-written
declarative pack with resources, operations, a credential type and the
resource/operation cascade. The `packs/waha/` packs are generated from a
vendored OpenAPI document and carry a `generator` provenance block — do not
copy that block into a hand-written pack.

## What packs cannot do

Read this before designing anything. A pack covers an API that is regular:
resources, operations, request shapes derivable from parameters. It does not
cover an integration needing computation between calls, conditional pagination,
or a bespoke signing scheme. If that is your API, a declarative pack is the
wrong tool — that is a judgement about fit, not a bug in the format.

Concretely, the routing interpreter refuses two things **at registration**,
deliberately, rather than ignoring them:

- `preSend` hooks. They are JavaScript closures and have no data
  representation, so there is nothing a JSON pack could carry.
- Function-form `postReceive`. Same reason: a function is code, and a pack
  that could carry code could not keep the egress policy.

An author arriving from n8n who designs around `execute()`, `preSend` or a
function `postReceive` will discover the rejection as a refused pack. Design
with what the interpreter implements instead: `requestDefaults`,
`routing.request` (method, URL, headers, query, body), `routing.send` with
`body`/`query`/`path`/`binary` placements, and post-receive `rootProperty`,
`setKeyValue`, `limit` and `binaryData`. The supported post-receive actions
are exactly `rootProperty`, `setKeyValue`, `limit` and `binaryData` — anything
else is refused with a diagnostic naming the operation.

## Step 1: scaffold

`nodepackgen scaffold` writes a minimal working pack directory: one resource,
one operation, one credential reference, shaped after the Telegram pack. It
loads and executes without further editing.

```sh
$ nodepackgen scaffold -dir ./packs/acme
nodepackgen: scaffolded ./packs/acme/pack.json
$ ls ./packs/acme
pack.json  pack.sha256
```

The directory holds the manifest `pack.json` plus the `pack.sha256` checksum
sidecar the loader verifies (more on that under
[Install](#install-where-the-pack-directory-is)). The scaffolded manifest, in
full:

```json
{
  "type": "pack.example",
  "version": 1,
  "displayName": "Example",
  "description": "An example hand-written pack. Rename the resource and operation to match the API it calls.",
  "category": "Messaging",
  "icon": "builtin:send",
  "subtitle": "{{ $parameter.operation }}: {{ $parameter.resource }}",
  "credentialType": "exampleApi",
  "requestDefaults": {
    "baseURL": "{{ $credentials.baseUrl }}"
  },
  "parameters": [
    {
      "key": "chatId",
      "label": "Chat ID",
      "description": "Where to send it. Sent as written.",
      "kind": "string",
      "required": true,
      "resources": ["message"],
      "operations": ["sendMessage"]
    },
    {
      "key": "text",
      "label": "Text",
      "description": "The message text.",
      "kind": "string",
      "resources": ["message"],
      "operations": ["sendMessage"]
    }
  ],
  "resources": [
    {
      "name": "message",
      "operations": [
        {
          "name": "sendMessage",
          "description": "Send a message.",
          "method": "POST",
          "url": "/sendMessage",
          "sends": [
            {"from": "chatId", "type": "body", "property": "chatId"},
            {"from": "text", "type": "body", "property": "text"}
          ]
        }
      ]
    }
  ],
  "generator": {
    "tool": "nodepackgen",
    "source": "scaffold"
  }
}
```

Three things to notice. The `resource`/`operation` picker cascade is generated
automatically: an internal options loader narrows the operations to the
selected resource, so you never declare the cascade itself. Each parameter
says which resources and operations show it — one parameter per key, because
the registry refuses duplicate keys. And the pack names a credential *type* by
string (`exampleApi`); it never carries a credential.

## Step 2: validate

`nodepackgen validate` reports every problem in a file at once, each with the
file and JSON path it came from, rather than aborting on the first unknown
field. It runs the same rules the server enforces at registration, so what the
validator accepts is what the server accepts:

```sh
$ nodepackgen validate ./packs/acme
nodepackgen: packs are valid
```

Run it after every edit. A clean run is the definition of "this pack loads".

## Step 3: grow to two operations

Add a second operation to the `message` resource and scope the parameters to
the operations that need them. `chatId` is now shown for both operations;
`text` stays on `sendMessage` only. A static `options` parameter adds a
priority picker. The manifest, in full:

```json
{
  "type": "pack.example",
  "version": 1,
  "displayName": "Example",
  "description": "An example hand-written pack. Rename the resource and operation to match the API it calls.",
  "category": "Messaging",
  "icon": "builtin:send",
  "subtitle": "{{ $parameter.operation }}: {{ $parameter.resource }}",
  "credentialType": "exampleApi",
  "requestDefaults": {
    "baseURL": "{{ $credentials.baseUrl }}"
  },
  "parameters": [
    {
      "key": "chatId",
      "label": "Chat ID",
      "description": "Where to send it. Sent as written.",
      "kind": "string",
      "required": true,
      "resources": ["message"],
      "operations": ["sendMessage", "getStatus"]
    },
    {
      "key": "text",
      "label": "Text",
      "description": "The message text.",
      "kind": "string",
      "resources": ["message"],
      "operations": ["sendMessage"]
    },
    {
      "key": "priority",
      "label": "Priority",
      "description": "Delivery priority.",
      "kind": "options",
      "default": "normal",
      "options": [
        {"value": "low", "label": "Low"},
        {"value": "normal", "label": "Normal"},
        {"value": "high", "label": "High"}
      ],
      "resources": ["message"],
      "operations": ["sendMessage"]
    }
  ],
  "resources": [
    {
      "name": "message",
      "operations": [
        {
          "name": "sendMessage",
          "description": "Send a message.",
          "method": "POST",
          "url": "/sendMessage",
          "sends": [
            {"from": "chatId", "type": "body", "property": "chatId"},
            {"from": "text", "type": "body", "property": "text"}
          ]
        },
        {
          "name": "getStatus",
          "description": "Check whether the message was delivered.",
          "method": "GET",
          "url": "/status",
          "sends": [
            {"from": "chatId", "type": "query", "property": "chatId"}
          ]
        }
      ]
    }
  ],
  "generator": {
    "tool": "nodepackgen",
    "source": "scaffold"
  }
}
```

Note the option entries use `value`/`label` — `name` is not a field here and
`validate` will tell you so. Note also that `sends` placements differ per
operation: `body` for the POST, `query` for the GET. `path` fills `{name}`
placeholders in the URL with one escaped segment each — deliberately not a
template, so a value containing a slash can never silently change which
endpoint is called.

```sh
$ nodepackgen validate ./packs/acme
nodepackgen: packs are valid
```

## Step 4: credentials

Rename the pack to its own type, point it at its own credential type, and
template the shared request over non-secret credential fields. The manifest,
in full (only `type`, `displayName`, `credentialType` and `requestDefaults`
change from the previous stage):

```json
{
  "type": "pack.acme",
  "version": 1,
  "displayName": "Acme",
  "description": "An example hand-written pack. Rename the resource and operation to match the API it calls.",
  "category": "Messaging",
  "icon": "builtin:send",
  "subtitle": "{{ $parameter.operation }}: {{ $parameter.resource }}",
  "credentialType": "acmeApi",
  "requestDefaults": {
    "baseURL": "{{ $credentials.baseUrl }}",
    "headers": {
      "X-Client": "{{ $credentials.clientId }}"
    }
  },
  "parameters": [
    {
      "key": "chatId",
      "label": "Chat ID",
      "description": "Where to send it. Sent as written.",
      "kind": "string",
      "required": true,
      "resources": ["message"],
      "operations": ["sendMessage", "getStatus"]
    },
    {
      "key": "text",
      "label": "Text",
      "description": "The message text.",
      "kind": "string",
      "resources": ["message"],
      "operations": ["sendMessage"]
    },
    {
      "key": "priority",
      "label": "Priority",
      "description": "Delivery priority.",
      "kind": "options",
      "default": "normal",
      "options": [
        {"value": "low", "label": "Low"},
        {"value": "normal", "label": "Normal"},
        {"value": "high", "label": "High"}
      ],
      "resources": ["message"],
      "operations": ["sendMessage"]
    }
  ],
  "resources": [
    {
      "name": "message",
      "operations": [
        {
          "name": "sendMessage",
          "description": "Send a message.",
          "method": "POST",
          "url": "/sendMessage",
          "sends": [
            {"from": "chatId", "type": "body", "property": "chatId"},
            {"from": "text", "type": "body", "property": "text"}
          ]
        },
        {
          "name": "getStatus",
          "description": "Check whether the message was delivered.",
          "method": "GET",
          "url": "/status",
          "sends": [
            {"from": "chatId", "type": "query", "property": "chatId"}
          ]
        }
      ]
    }
  ],
  "generator": {
    "tool": "nodepackgen",
    "source": "scaffold"
  }
}
```

Two credential rules matter here. First, `$credentials` in a routing template
resolves to **non-secret fields only**. `baseUrl` and `clientId` are fine in a
template; an API token is not. The correct way to place a secret is through
the credential type's declarative authentication, which attaches it to the
outbound request without ever passing through a template. Second, a pack names
a credential type and the operator binds a real credential of that type after
installation — the pack never sees credential values.

## Step 5: triggers

A trigger pack declares events instead of resources: one output per event, in
the order listed — the order is the contract, because workflow connections
address outputs by index. It carries no routing description; a trigger makes
no outbound request itself. HMAC verification, media download and lifecycle
registration ride along in the `trigger` block:

```json
{
  "type": "pack.acmeTrigger",
  "version": 1,
  "displayName": "Acme Trigger",
  "description": "Starts a workflow when Acme delivers an event.",
  "category": "Triggers",
  "icon": "builtin:zap",
  "subtitle": "{{ $parameter.event }}",
  "credentialType": "acmeApi",
  "requestDefaults": {},
  "trigger": {
    "events": ["message.created", "message.updated"],
    "catchAll": "other",
    "eventPath": "event",
    "shape": "bodyAsItem",
    "webhook": {"name": "default", "pathParameter": "path", "method": "POST"},
    "hmac": {
      "header": "X-Acme-Signature",
      "algorithm": "sha512",
      "secretParameter": "hmacSecret"
    },
    "lifecycle": {
      "id": "acme.webhook",
      "enabledParameter": "autoRegister",
      "set": {
        "method": "PUT",
        "url": "{{ .baseUrl }}/api/webhooks",
        "headers": {"Content-Type": "application/json"},
        "body": "{\"url\":\"{{ .PublicURL }}\"}",
        "credentialType": "acmeApi"
      }
    },
    "notice": "Acme is not configured to deliver here yet. Add {{ url }} to the Acme dashboard, or turn on auto-register and activate again."
  },
  "parameters": [
    {
      "key": "path",
      "label": "Path",
      "description": "A label for this endpoint.",
      "kind": "string",
      "required": true
    },
    {
      "key": "event",
      "label": "Event",
      "description": "The event to listen for.",
      "kind": "options",
      "options": [
        {"label": "Message created", "value": "message.created"},
        {"label": "Message updated", "value": "message.updated"}
      ]
    },
    {
      "key": "hmacSecret",
      "label": "Webhook HMAC secret",
      "description": "When set, every delivery must carry a matching X-Acme-Signature header (SHA-512 over the raw body) or it is refused before an execution exists.",
      "kind": "string",
      "typeOptions": {"password": true}
    },
    {
      "key": "autoRegister",
      "label": "Register this URL with Acme",
      "description": "PUT this workflow's URL into Acme on activation.",
      "kind": "boolean",
      "default": false
    }
  ],
  "generator": {
    "tool": "nodepackgen",
    "source": "scaffold"
  }
}
```

The pieces: `webhook` binds the inbound endpoint; each delivery fans out to
the one port matching its event, and anything unrecognised goes to the
`catchAll` output rather than being dropped. `hmac` verifies a signature over
the raw body before an execution exists (the implemented algorithm is
`sha512`). `lifecycle.set` PUTs the minted webhook URL to the remote service
on activation when the `autoRegister` parameter is on. `media`, not shown
here, downloads linked media into the binary store once, at the trigger, so
the run owns it instead of referencing a URL that expires. And `notice` covers
the activation-notice case: when the URL still has to be pasted into the
remote service by hand, the editor shows this text with `{{ url }}` filled in.

One honest caveat about checking a trigger pack: file-level `validate` runs
against a throwaway registry without the trigger executor installed, so it
reports `bound to executor "core.packTrigger", which this server has not
installed` — the shipped WAHA trigger pack produces the identical line. That
line is a harness gap, not a pack defect; the authoritative check for a
trigger pack is loading it through the server.

## Icons

`builtin:<name>` names a glyph the editor already imports and costs no bytes —
prefer it. Anything else ships artwork that is validated at registration, and
the rules are refusals, not advice. A pack is refused for an icon that
contains a script element, contains a `foreignObject`, contains an event
handler attribute, or references an external or `javascript:` URL — because
the editor renders inside a customer's page and a hostile icon is cross-tenant
stored XSS.

## Install: where the pack directory is

An operator installs a pack by placing files in a configured directory and
restarting — no rebuild, no fork, no Go toolchain. Each immediate subdirectory
of the packs directory is one pack:

```sh
$ nodepackgen pack -dir ./packs/acme
nodepackgen: wrote ./packs/acme/pack.sha256
$ cat ./packs/acme/pack.sha256
c8478053baa406f3918d3ea9d0478212f8e8762dac153374d21e4e98a73a648a  pack.json
```

`nodepackgen pack` writes the checksum record the loader verifies, in
`sha256sum`-compatible shape, so an operator never computes a digest by hand.
The sidecar is a lockfile-style record of something a human approved: the
loader hashes `pack.json` and refuses a pack whose bytes no longer match, or
that has no sidecar at all. A pack that fails to load refuses the whole boot,
naming the pack and the reason — a pack that silently failed to register would
leave workflows referencing a node type that can never activate.

The server reads the directory named by `packs.dir`
(`KILASFLOW_PACKS_DIR`), at composition before the registry is shared. An
absent or empty directory is a normal, silent condition. Two things are
deliberately out of scope: hot reload — the registry is read-only once the
server is serving — and remote installation, which would turn a startup path
into a network dependency and a supply-chain surface.

Every externally loaded definition is tagged `pack`, and a pack claiming the
`kilasflow.` prefix is refused with an error naming the pack and the reason —
that namespace is reserved for built-in nodes. The registry also resolves
versions downward: registering `202409` and `202502` and requesting `202410`
gets `202409`, which is what lets an imported workflow keep its original
version.

To ship a pack to one customer rather than every tenant, scope its node type by
declaring `visibleTo` in the manifest or by giving the operator a
`packs.visible_to` override — see [Tenant-scoped
nodes](/guides/tenant-scoped-nodes/) for both, and for what a tenant that is not
on the list sees.

## Licence: yours to distribute, theirs to install

A pack you write is your JSON to distribute. What runs on a server is what
that server's operator placed in the packs directory and approved with a
checksum — operator-installed, never silently fetched. That is the same line
the project draws for executable sidecars: KilasFlow ships no third-party
code, and the operator installs what they chose to trust.

One boundary to keep while writing: reimplement the format, do not copy code.
Resource and operation strings, parameter shapes and routing metadata are
interoperability facts — the "could you have derived it from observing the
wire format?" test. The TypeScript implementing them is not. Do not paste node
source into or alongside your pack.

## When packs are not enough

If your integration needs computation between calls, conditional pagination,
or a bespoke signing scheme, say so early and stop shaping it into a pack.
That work belongs to programmatic nodes, not to a format that is deliberately
data. The [node pack format](/reference/node-packs/) reference lists every
field and every refusal in one place.
