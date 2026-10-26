---
title: Authoring a community node pack
description: Write a declarative node pack as JSON, check it with nodepackgen validate, and install it with a checksum the operator approves.
---

A node pack is JSON, not code. You describe resources, operations, parameters
and routing metadata; the server interprets them at run time. There is no Go
to write, no SDK to import, and nothing to compile. The whole flow is three
steps — **scaffold**, **validate**, **pack** — and every sample on this page is
a real file that `nodepackgen validate` accepts.

Two binaries share this work, and each has its own job. `nodepackgen` is the
authoring tool — scaffold, validate, pack, and generate from an OpenAPI
document; build it from the repository with `go build ./cmd/nodepackgen`, or
run it out of the container image, which ships it at `/app/nodepackgen`. The
server binary's `kilasflow pack validate` verb validates a pack exactly the
way the loader will read it — directory (sidecar first) or manifest file —
which is the check to run on the server's own host before a restart; see the
[CLI reference](/reference/cli/#kilasflow-pack-validate).

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
one operation, one credential reference, shaped after the Telegram pack. Left
at its defaults it names a credential type called `exampleApi`, which is not
in the catalogue and never can be — a pack cannot add a credential type — so
pass the two flags that make it real: the credential type your API
authenticates with and its base URL. [Build nodes for your
API](/guides/build-nodes-for-your-api/) covers choosing the type; the
generic ones (`httpHeaderAuth`, `httpBearerAuth`, `httpBasicAuth`,
`httpQueryAuth`, `httpCustomAuth`) cover most APIs.

```sh
$ nodepackgen scaffold -dir ./packs/acme \
    -type pack.acme -display-name Acme \
    -credential-type httpHeaderAuth \
    -base-url https://api.acme.example
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
  "type": "pack.acme",
  "version": 1,
  "displayName": "Acme",
  "description": "An example hand-written pack. Rename the resource and operation to match the API it calls.",
  "category": "Messaging",
  "icon": "builtin:send",
  "subtitle": "{{ $parameter.operation }}: {{ $parameter.resource }}",
  "credentialType": "httpHeaderAuth",
  "requestDefaults": {
    "baseURL": "https://api.acme.example"
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
  "type": "pack.acme",
  "version": 1,
  "displayName": "Acme",
  "description": "An example hand-written pack. Rename the resource and operation to match the API it calls.",
  "category": "Messaging",
  "icon": "builtin:send",
  "subtitle": "{{ $parameter.operation }}: {{ $parameter.resource }}",
  "credentialType": "httpHeaderAuth",
  "requestDefaults": {
    "baseURL": "https://api.acme.example"
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

Credentials are where a pack meets a decision it cannot make alone: **which
existing credential type does your API authenticate with?** A pack cannot add
a credential type — the catalogue is declared in Go
(`internal/credentials/builtin.go`) and nothing in startup lets a pack
contribute one — so the pack names a type that already exists. For most APIs
that is one of the generic HTTP types, and `httpHeaderAuth` (a fixed header
whose name is a field) is the one to reach for first:

```json
{
  "type": "pack.acme",
  "version": 1,
  "displayName": "Acme",
  "description": "An example hand-written pack. Rename the resource and operation to match the API it calls.",
  "category": "Messaging",
  "icon": "builtin:send",
  "subtitle": "{{ $parameter.operation }}: {{ $parameter.resource }}",
  "credentialType": "httpHeaderAuth",
  "requestDefaults": {
    "baseURL": "https://api.acme.example",
    "headers": {
      "X-Client": "kilasflow-pack"
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
resolves to **non-secret fields only**, and the generic HTTP types carry none
— so the base URL is a literal in `requestDefaults`, exactly as above. A type
whose address is part of the credential changes that: `wahaApi`'s `baseUrl`
is deliberately not secret, which is why the WAHA pack templates
`{{ $credentials.baseUrl }}` and works. Second, the secret itself never
passes through a template: `httpHeaderAuth`'s declarative authentication
attaches the header to every outbound request, which is the whole reason the
pack does not mention it. The operator binds a real credential of the type
after installation — the pack never sees credential values.

The full decision — which generic type, where a base URL may live, what a new
named type costs (a Go change, not a pack) — is
[Credentials for your API](/guides/build-nodes-for-your-api/#credentials-for-your-api).

## Loading options from the customer's service

A static `options` list cannot offer the customer's own channels, agents, tags
or team members: those live behind their API, and no binary can know them. A
parameter can declare a `loadOptions` block instead, and the editor fills the
picker at edit time — the request the server makes is the same governed one
every other outbound call is:

```json
{
  "key": "channel",
  "label": "Channel",
  "kind": "options",
  "required": true,
  "loadOptions": {
    "source": "http",
    "method": "GET",
    "endpoint": "/api/channels",
    "credentialType": "httpHeaderAuth",
    "itemsPath": "data",
    "labelTemplate": "{{ name }} ({{ provider }})",
    "valueField": "id",
    "dependsOn": []
  }
}
```

- `endpoint` may carry `{{ key }}` placeholders naming the parameters in
  `dependsOn`; every substituted value is URL-escaped, never concatenated, and
  a change to a listed parameter refetches the list. `baseUrlParameter` names
  the parameter holding the service's own base URL, prepended without being
  escaped.
- `credentialType` must be the pack's own `credentialType`: the request is
  signed with the credential the node attached, under the egress policy and
  the credential's allowed domains. Omit it for an unsigned public list.
- `itemsPath` walks the response to the list (dot-separated, empty means the
  response is the list), `valueField` reads each option's value, and
  `labelTemplate` renders its label from the item's fields.
- `loadOptions` is allowed on `options` and `multiOptions` parameters. A
  `resourceLocator` declares modes instead, and carries the loader on its list
  mode — a typed "by ID" mode has no list to load:

```json
{
  "key": "agent",
  "label": "Agent",
  "kind": "resourceLocator",
  "required": true,
  "modes": [
    {"name": "id", "label": "By ID", "kind": "string", "pattern": "^a_[0-9]+$", "patternHint": "an agent id, a_<digits>"},
    {"name": "list", "label": "From list", "kind": "options", "placeholder": "Search agents…",
     "loadOptions": {"source": "http", "method": "GET", "endpoint": "/api/agents", "credentialType": "httpHeaderAuth", "itemsPath": "data", "valueField": "id", "labelTemplate": "{{ name }}"}}
  ]
}
```

A load failure shows inline in the picker, as the reason the server reports —
a missing credential, a host the policy refuses, a service answering 4xx —
rather than as an empty list. Internal loaders (`source: "internal"`) are the
server's own and cannot be named by a pack.

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
  "credentialType": "httpHeaderAuth",
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
        "credentialType": "httpHeaderAuth"
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
the raw body before an execution exists (the algorithms are `sha256` and
`sha512`), with the secret from a node parameter (`secretParameter`), from a
value the registration captured (`secretCapture`, covered below), or from a
field of the credential the node carries (`secretCredential`, covered below).
`lifecycle.set` sends the minted webhook URL to the remote service with the
method the pack declares (`PUT` in this example, `POST` in the one below) on
activation when the `autoRegister` parameter is on; `check` and `remove`
are optional requests that ask whether it is registered and take the
registration down, and `set` can `capture` values from the answer for them to
use, as the section after this one describes. `media`, not shown
here, downloads linked media into the binary store once, at the trigger, so
the run owns it instead of referencing a URL that expires. And `notice` covers
the activation-notice case: when the URL still has to be pasted into the
remote service by hand, the editor shows this text with `{{ url }}` filled in.

`kilasflow pack validate` checks a trigger pack the way the server would load
it, including the trigger rules: the event list and catch-all, the webhook
declaration, the `hmac` scheme (algorithm, encoding, the `signingString`
template, the preset's exclusivity), and the lifecycle. It refuses a lifecycle
`check` or `remove` that declares `capture` or reads a `{{ .Captured.<key> }}`
that `set` does not capture; an `hmac.secretCapture` that names no captured
key; and any two of `secretParameter`, `secretCapture` and `secretCredential`
together. Both trigger examples in this guide
pass it with no issues, as does the shipped WAHA trigger pack. What it cannot
check is the run-time side: that the credential named by `credentialType` is
bound on the node, or that the remote service accepts the request. And one
run-time behaviour it cannot see: a `deliveryIdHeader` parameter only dedupes
when the workflow document sets it on the node — a pack's parameter default
fills the editor form, not the stored binding. The sender's half of the
contract — what to sign, what to send, what a replay is answered with — is
[App events as triggers](/guides/app-events/).

### Keeping what the service answers

The `acmeTrigger` example above registers the webhook and forgets the answer.
A service that registers a webhook often answers with something the pack needs
later: the id of the subscription it created, so a later call can delete it, or
the secret it will sign every delivery with. The lifecycle keeps those with
`capture`, reads them back with `{{ .Captured.<key> }}`, and can verify
deliveries with a captured secret through `hmac.secretCapture`. Here is a
complete trigger pack that uses all three, plus `{{ .ParameterJSON.<key> }}` to
send a multi-select of events:

```json
{
  "type": "pack.acmeSubscriptionTrigger",
  "version": 1,
  "displayName": "Acme Subscription Trigger",
  "description": "Subscribes to Acme events and verifies the secret Acme generates.",
  "category": "Triggers",
  "icon": "builtin:zap",
  "credentialType": "httpHeaderAuth",
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
      "secretCapture": "secret"
    },
    "lifecycle": {
      "id": "acme.subscription",
      "enabledParameter": "autoRegister",
      "check": {
        "method": "GET",
        "url": "{{ .baseUrl }}/api/subscriptions/{{ .Captured.id }}",
        "credentialType": "httpHeaderAuth"
      },
      "set": {
        "method": "POST",
        "url": "{{ .baseUrl }}/api/subscriptions",
        "headers": {"Content-Type": "application/json"},
        "body": "{\"url\":\"{{ .PublicURL }}\",\"events\":{{ .ParameterJSON.subscribedEvents }}}",
        "credentialType": "httpHeaderAuth",
        "capture": {"id": "data.id", "secret": "data.secret"}
      },
      "remove": {
        "method": "DELETE",
        "url": "{{ .baseUrl }}/api/subscriptions/{{ .Captured.id }}",
        "credentialType": "httpHeaderAuth"
      }
    }
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
      "key": "subscribedEvents",
      "label": "Events to subscribe to",
      "description": "The Acme events this URL is registered for.",
      "kind": "multiOptions",
      "default": [],
      "options": [
        {"label": "Message created", "value": "message.created"},
        {"label": "Message updated", "value": "message.updated"}
      ]
    },
    {
      "key": "autoRegister",
      "label": "Register this URL with Acme",
      "description": "Create a subscription in Acme on activation and remove it on deactivation.",
      "kind": "boolean",
      "default": false
    }
  ]
}
```

#### `capture`: keep values from the registration answer

`capture` belongs to `lifecycle.set`, the call that registers the webhook. It
is a map from a key you choose to a dotted path into the JSON response, so
`"capture": {"id": "data.id", "secret": "data.secret"}` keeps `data.id` as `id`
and `data.secret` as `secret`. The values are kept against the node's route,
sealed with the credential encryption key, and cleared again once `remove` has
succeeded.

- Only `set` may declare `capture`. A `check` or `remove` that does is refused
  when the pack loads, because they are addressed by what `set` kept.
- A key is letters, digits and underscores, so a template can name it. A path
  is dotted object keys with no empty segment.
- The value at each path must be non-empty text, a number or a boolean. A
  number keeps the digits the service sent. A missing path, an empty string, an
  object or a list fails the whole registration, and nothing is kept.
- If the service accepted the registration but its answer could not be kept,
  the server calls `remove` straight away when what was captured is enough to
  address it. Otherwise the error says the registration may need removing by
  hand.
- A server without the credential encryption key refuses a `set` that
  captures, before it sends anything, because there is nowhere to keep the
  values.

#### `{{ .Captured.<key> }}`: read them back

`check` and `remove` read a kept value as `{{ .Captured.<key> }}` in their URL,
headers or body, as `{{ .Captured.id }}` does in the pack above. `set` runs
before anything is captured, so it cannot read one: the pack is refused if it
tries. Every key a `check` or `remove` reads must be one `set` captures.

A captured value comes from a remote service, so it is escaped for where it
lands: as one path segment, as a query value after a `?`, or inside a JSON
string. Anywhere else it is substituted as it is: a header value is only
checked for a line break (one stops the request), and a body that is not JSON,
such as a form body, receives the raw text, so keep captured values out of
those. A captured value before the URL's path starts, or one that would make a
path segment `.` or `..`, is refused, and in a JSON body outside a string it
must itself be valid JSON, such as a bare number. When a
route has no kept value for a key a `check` or `remove` reads, the server does
not send the placeholder as literal text: `check` reports the webhook as not
registered, so `set` runs, and `remove` is skipped.

#### `{{ .ParameterJSON.<key> }}`: lists and objects

`{{ .Parameter.<key> }}` is a parameter as text, which works for a string, a
number or a boolean: a URL segment, or the inside of a JSON string.
A `multiOptions` selection or a `collection` has no text form a service would
read. `{{ .ParameterJSON.<key> }}` is every parameter encoded as JSON, lists
and objects included. In a body that starts with `{` or `[`, the placeholder
outside a string is the JSON value itself:

```text
"events": {{ .ParameterJSON.subscribedEvents }}
→ "events": ["message.created","message.updated"]
```

Use it over a plain string placeholder whenever the value is a list or an
object, or when the service needs the JSON type kept. Outside a string,
`{{ .Parameter.<key> }}` is accepted only if the value is itself one valid JSON
value, which includes `null`, objects and arrays typed into a text parameter,
not only numbers and booleans; any other text there is refused with a message that says
to quote it or use `ParameterJSON`, because a bare `sales` would make the body
invalid. Inside a string, `ParameterJSON` is the JSON text as a string, which
is still only a string. The rendered body must be valid JSON, or nothing is
sent.

#### `secretCapture`: verify with a secret the service generated

`hmac.secretParameter` reads the signing secret from a node parameter the
customer typed in. A service that generates the secret at registration time
says so in its answer instead. Capture it and name that key as
`hmac.secretCapture`, as the pack above does with `"secretCapture": "secret"`.

- At most one of `secretParameter`, `secretCapture` and `secretCredential` may
  be set. A pack that names two is refused, because a delivery is verified
  with one secret; with none, deliveries are not verified.
- `secretCapture` must name a key that `lifecycle.set` captures. A pack with no
  lifecycle, or whose `set` does not capture that key, is refused when it
  loads: it would look verified and never be.
- Deliveries are verified with the captured value only. A parameter that
  happens to share the key's name is never used as a fallback.
- While the node's `enabledParameter` is on and nothing is captured yet, every
  delivery is refused. That covers the window before the registration has
  answered, and the window after a `remove` has cleared the secret while the
  workflow is still active. With the registration off, nothing is ever
  captured, which is the same as no secret configured: deliveries are not
  verified, as with an empty `secretParameter`.

#### `secretCredential`: keep the secret out of the workflow document

A `secretParameter` is stored in the workflow document, which is the one place
a secret does not belong: documents are exported, imported, diffed and read.
When the service's signing secret is something your customer already keeps in
a credential — or should — read it from there instead:

```json
"hmac": {
  "header": "X-Acme-Signature",
  "algorithm": "sha256",
  "secretCredential": {"type": "httpHeaderAuth", "field": "value"}
}
```

- `type` must be the pack's own `credentialType`, so the credential the node
  attaches is the one the delivery verifies with. A pack that names another
  type is refused when it loads.
- The field is read per delivery, so a credential rotated on the tenant's side
  is the secret the sender now signs with, without reactivating anything.
- A node that carries no credential of that type is treated like an empty
  `secretParameter`: deliveries are not verified. A credential that *is*
  carried but cannot be read, or has no such field, refuses every delivery —
  the node said it verifies, and this delivery is not one it can.

#### The scheme: algorithm, encoding, prefix, signing string, timestamp

Services disagree about everything except the HMAC. The `hmac` block spells
the scheme your service signs with:

| Field | Values | What it is |
| --- | --- | --- |
| `algorithm` | `sha256`, `sha512` | the digest |
| `encoding` | `hex` (default), `base64` | how the sender writes the digest |
| `prefix` | any text, e.g. `sha256=` or `v1,` | what stands between the version marker and the digest |
| `signingString` | a template | what is signed; empty is the raw body |
| `timestampHeader` | a header name | where the signing timestamp comes from |
| `toleranceSeconds` | a positive number | how old that timestamp may be |

`signingString` is templated like the lifecycle's requests, over three things:
the raw body (`{{ .body }}`), the timestamp header's value
(`{{ .timestamp }}`, which requires naming a `timestampHeader`), and any
request header (`{{ .header.X-Request-Id }}`). GitHub's scheme is the body
alone; a `timestamp.body` string covers Stripe-style and Standard Webhooks
schemes; Slack's is `v0:{{ .timestamp }}:{{ .body }}` with the literal `v0:`
written out.

A header may carry several signatures, comma-separated, during a secret
rotation; any one of them matching is a pass. A `toleranceSeconds` turns the
timestamp into a replay guard: a delivery older (or further ahead) than the
tolerance is refused before an execution exists, together with a missing,
malformed or wrong signature.

#### `preset`: `standardWebhooks`

A service that follows [standardwebhooks.com](https://standardwebhooks.com)
is one word instead of six:

```json
"hmac": {"preset": "standardWebhooks", "secretCredential": {"type": "httpHeaderAuth", "field": "value"}}
```

The preset is the whole scheme — `webhook-signature`, sha256, base64, the
`v1,` prefix, `{{ .timestamp }}.{{ .body }}`, `webhook-timestamp`, a
300-second tolerance — so naming any scheme field beside it is refused, and
only the secret is yours to choose.

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

Upgrading follows the same steps — new `pack.json`, new `pack.sha256`,
restart — and the version rule is what makes it safe: a workflow written
against version 1 stays on version 1 after you register version 2, so ship a
new version alongside rather than instead. The full sequence, including what
each step asks of the operator, is
[Publishing and installation](/guides/build-nodes-for-your-api/#publishing-and-installation).

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
