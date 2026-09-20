---
title: Migrating from n8n
description: What an n8n export becomes when KilasFlow imports it, what does not carry, and how far the importer actually gets today.
---

KilasFlow's workflow document is a reimplementation of n8n's interchange format
in Go, so an n8n export can be read directly. That is not the same as being a
drop-in replacement, and this page exists so you can find out which of your
workflows will work before you spend a weekend discovering it one node at a
time.

The importer is built on a single rule: it never guesses. A node type it has no
equivalent for is not silently mapped onto a similar one and not quietly
dropped — it becomes a visible placeholder that keeps the original node whole
and refuses to compile. Everything it could not carry faithfully comes back in
the import response as a structured diagnostic. The consequence is that an
import is rarely a clean yes or no. It is a workflow plus a list of things to
fix, and reading that list is the actual work of migrating.

## Before you start: what carries

The advertised subset is a hand-written list rather than a pattern match,
because an interoperability claim is only meaningful if the exact set is
written down and testable. Thirty-seven pairs are advertised today, covering
thirty-five distinct KilasFlow node types — two n8n type strings map onto each
WAHA node, for reasons given below.

:::note[Get the list from your own instance rather than from this page]
Every export response carries the live list in its `supportedMappings` field,
and a test pins that list exactly, so the code cannot add or remove a pair
without something going red. If this page and your instance disagree, your
instance is right.

```bash
curl -s -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  "https://your-host/api/v1/workflows/$ID/export?format=n8n" \
  | jq -r '.supportedMappings[]'
```
:::

### Triggers

| n8n node type | KilasFlow node type |
| --- | --- |
| `n8n-nodes-base.manualTrigger` | `kilasflow.manual` |
| `n8n-nodes-base.webhook` | `kilasflow.webhook` |
| `n8n-nodes-base.scheduleTrigger` | `kilasflow.schedule` |
| `n8n-nodes-base.executeWorkflowTrigger` | `kilasflow.executeWorkflowTrigger` |
| `n8n-nodes-base.telegramTrigger` | `kilasflow.telegramTrigger` |
| `@devlikeapro/n8n-nodes-waha.wahaTrigger` | `pack.wahaTrigger` |
| `n8n-nodes-waha.wahaTrigger` | `pack.wahaTrigger` |

### Core and flow control

| n8n node type | KilasFlow node type |
| --- | --- |
| `n8n-nodes-base.set` | `kilasflow.set` |
| `n8n-nodes-base.if` | `kilasflow.if` |
| `n8n-nodes-base.switch` | `kilasflow.switch` |
| `n8n-nodes-base.filter` | `kilasflow.filter` |
| `n8n-nodes-base.limit` | `kilasflow.limit` |
| `n8n-nodes-base.merge` | `kilasflow.merge` |
| `n8n-nodes-base.noOp` | `kilasflow.noOp` |
| `n8n-nodes-base.splitInBatches` | `kilasflow.loop` |
| `n8n-nodes-base.wait` | `kilasflow.wait` |
| `n8n-nodes-base.dateTime` | `kilasflow.dateTime` |
| `n8n-nodes-base.executeWorkflow` | `kilasflow.executeWorkflow` |
| `n8n-nodes-base.stickyNote` | `kilasflow.stickyNote` |

### Data shaping

| n8n node type | KilasFlow node type |
| --- | --- |
| `n8n-nodes-base.aggregate` | `kilasflow.aggregate` |
| `n8n-nodes-base.splitOut` | `kilasflow.splitOut` |
| `n8n-nodes-base.sort` | `kilasflow.sort` |
| `n8n-nodes-base.summarize` | `kilasflow.summarize` |
| `n8n-nodes-base.removeDuplicates` | `kilasflow.removeDuplicates` |

### Integrations

| n8n node type | KilasFlow node type |
| --- | --- |
| `n8n-nodes-base.httpRequest` | `kilasflow.httpRequest` |
| `n8n-nodes-base.respondToWebhook` | `kilasflow.respondToWebhook` |
| `n8n-nodes-base.postgres` | `kilasflow.postgres` |
| `n8n-nodes-base.mySql` | `kilasflow.mysql` |
| `n8n-nodes-base.telegram` | `pack.telegram` |
| `@devlikeapro/n8n-nodes-waha.WAHA` | `pack.waha` |
| `n8n-nodes-waha.WAHA` | `pack.waha` |

**The HTTP Request node's output is n8n's, not a KilasFlow envelope.** This is
the one mapping where the *shape* a migrated workflow reads changed, so it is the
first thing to check when an expression downstream of an HTTP node resolves to
nothing. n8n's default output is the parsed body itself: an object body becomes
the item, a top-level array becomes one item per element, a body that is neither
lands under `data`, and an empty body yields `{}` — so `$json.<field>` keeps
meaning what it meant in n8n. KilasFlow used to wrap every response in
`{body, headers, statusCode, truncated}`, which the executor's own note counts as
199 nodes across the template corpus reading a field that was not there.

`fullResponse` asks for the envelope instead, and then it is n8n's own:
`{body, headers, statusCode, statusMessage}` with lower-case header names. A
response stored as a file puts only the binary reference in the item (under the
`outputPropertyName` parameter, default `data`), with that envelope beside it when
`fullResponse` was asked for too; a response large enough to be truncated is
refused rather than stored half.

Two other defaults are n8n's and did not used to be. **Redirects are not
followed unless you ask**: the 302 itself is handed to the workflow, where
KilasFlow used to follow it unconditionally, so a workflow that meant to inspect a
redirect never saw one. Nothing about the egress policy changes with it — the
redirect target is checked on the same terms as the first request. And
**`bodyFields` is a structured map**, n8n's "Using Fields Below", encoded *after*
the per-item expression pass, so `{{ $json.id }}` reaches the wire as the value
rather than as the template that produced it; it is sent as JSON for
`bodyType: json` and form-encoded for `form`, and a raw body carries
`rawContentType` (default `text/plain; charset=utf-8`). An interpolated value in
the URL's query string is percent-encoded after resolution as well, so a space or
an `&` inside `{{ $json.query }}` no longer truncates the request line.

### AI

| n8n node type | KilasFlow node type |
| --- | --- |
| `@n8n/n8n-nodes-langchain.agent` | `kilasflow.agent` |
| `@n8n/n8n-nodes-langchain.lmChatOpenAi` | `kilasflow.lmChatOpenAi` |
| `@n8n/n8n-nodes-langchain.lmChatOpenRouter` | `kilasflow.lmChatOpenRouter` |
| `@n8n/n8n-nodes-langchain.memoryBufferWindow` | `kilasflow.memoryBuffer` |
| `@n8n/n8n-nodes-langchain.toolHttpRequest` | `kilasflow.httpTool` |

An AI cluster's typed edges — `ai_languageModel`, `ai_memory`, `ai_tool` — need
no translation. n8n keys a connection by its source node, and for a typed
channel the source is the sub-node and the target is the agent it configures,
which is the direction KilasFlow already uses.

### Code

| n8n node type | KilasFlow node type |
| --- | --- |
| `n8n-nodes-base.code` | `kilasflow.foreignCode` |

This one is in the table and still does not run, which is why it has its own
section: see [The Code node](#the-code-node) below.

### Two things about this table worth knowing

**The two unscoped WAHA entries are import-only.** The published package is
`@devlikeapro/n8n-nodes-waha` and an older one was unscoped, so a workflow
authored against either has to find the same node. Export always writes the
scoped form. The capitalisation is not a typo either — the package ships `WAHA`
in capitals for the action node and `wahaTrigger` in camel case for the trigger,
and these strings are matched byte for byte.

**A mapped node keeps its own `typeVersion`.** KilasFlow's node versions mirror
n8n's, so an imported node lands on the parameter shape it was authored against
rather than being rewritten to a single number. For the WAHA nodes, whose two
sides genuinely share a version numbering, a version this installation does not
have registered is a **blocking** diagnostic rather than a silent resolve-down:
a workflow authored at WAHA 202409 that quietly landed on 202502 would be wired
against a different event order.

## What does not carry

### Unsupported nodes

A node type outside the list becomes `kilasflow.unsupported`. This is designed
behaviour and not a failure mode, so it is worth being precise about what
happens:

- The placeholder keeps the original `type`, the original `typeVersion`, and a
  capsule holding the **whole** source node — its parameters, its credentials
  reference, its notes, its retry policy, its `webhookId`.
- It renders on the canvas where the original node was, so the graph keeps its
  shape and you can see exactly where the gap is.
- Its validator always fails. The compiler is what gates activation and
  running, so the draft **saves and opens and can be edited, and can never be
  activated or run** until the node is replaced.
- Exporting it back to n8n returns the original node whole. A round trip
  through KilasFlow does not cost you the node.

The placeholder is registered at four arities — one, two, four and eight ports —
and the import picks the smallest one that covers the edges the source workflow
actually drew. It has to work this way because the node registry is keyed by
type and version and is immutable for the life of the process, so one definition
cannot have a variable port count, and a node's real arity is only knowable from
the connections around it. A placeholder standing in for a five-output Switch
gets the eight-port member, and all five branches survive.

### The Code node

An n8n Code node is JavaScript or Python. KilasFlow does not run either, and it
does not translate them: translating JavaScript to Go is a compiler project with
no correct stopping point, where a one-line `items.map(…)` translates cleanly
and the next body translates into Go that compiles and computes something else.
Silently different is the outcome this codebase refuses everywhere.

So an imported Code node becomes `kilasflow.foreignCode` — a distinct type
rather than the generic placeholder, because the two are different problems.
An unsupported node type is something the product has not built; a Code node is
something it has deliberately not built, the source is right there, and what you
need is to be told which native node now does the same job. The node keeps your
source verbatim in `jsCode` or `pythonCode`, refuses to compile, and the
diagnostic names a likely replacement based on what the body does — a
`.reduce(` gets pointed at Aggregate or Summarize, a `.sort(` at Sort, a
`fetch(` at HTTP Request, and anything unrecognised gets the general list.

Your two ways forward are the native nodes — Filter, Switch, Set, Sort,
Aggregate, Split Out, Summarize, Remove Duplicates — or rewriting the body in
KilasFlow's own Go Code node (`kilasflow.code`), which compiles Go to WebAssembly
and runs it under a time and memory limit.

### Document-level elements

Four things on the n8n document are read and then not carried, each reported as
a **dropped** diagnostic:

| Field | Why it is not carried |
| --- | --- |
| `settings` | Everything except the timezone and the workflow's own `executionTimeout`. Error workflow, execution order and the rest have no KilasFlow equivalent yet. Both carried keys matter: a scheduled workflow whose zone was dropped runs at the wrong hour every day, and the timeout is the workflow's run budget. A zone this server cannot resolve is reported as lossy rather than silently falling back to UTC, and so is n8n's `-1` "no timeout", because this server always applies the instance's budget. |
| `pinData` | Pinned test data is an n8n editor feature. It is dropped rather than parked under a reserved key, because carrying data nothing reads would create a second silent-drop problem a release later. The nodes that had data pinned will run for real. |
| `meta` | n8n instance metadata describing where the workflow came from. It has no meaning in another installation. |
| `staticData` | n8n's per-workflow scratch space that its nodes persist between runs. There is no equivalent. |

### Node-level elements

Per node, on a **mapped** node only — an unsupported node keeps all of this in
its capsule — the following are reported as **dropped**:

- `notes`, the node's note.
- `webhookId`, n8n's per-node webhook identity, which is meaningless in another
  installation; KilasFlow assigns its own binding on activation.

Everything else n8n writes on a node crosses, and is honoured rather than merely
stored: `disabled` lands on the imported node and goes back out on export,
`onError` carries the mode verbatim (with the legacy `continueOnFail` boolean
kept beside it), an edge leaving n8n's error slot resolves to the node's own
`error` port, `alwaysOutputData` emits the empty item n8n emits, `executeOnce`
runs the node once for the whole batch, and `retryOnFail`, `maxTries`,
`waitBetweenTries` land on the node's canonical settings. `maxTries` is clamped
to 8 and `waitBetweenTries` to 300,000 ms — clamped rather than refused, so a
workflow with a larger retry budget still imports and one typo cannot become
thousands of calls.

## The diagnostic vocabulary

Every import returns a list under `unsupported`, and every export returns one
under `lossy`. Both use the same three severities, and the difference between
them is the difference between "fix this before you activate" and "we noticed
and moved on".

| Severity | Meaning | What to do |
| --- | --- | --- |
| `blocking` | The workflow cannot run as imported. | Fix it. The workflow will not activate until you do. This is an unsupported node type, a Code node, a credential that must be re-bound, an unavailable `typeVersion`, or a parameter the mapped node genuinely cannot express. |
| `lossy` | The element was carried, but differently. | Read it and decide. The workflow will activate. Whether the difference matters is a judgement only you can make — a query replacement split into three bound values is fine if the values had no commas in them and wrong if they did. |
| `dropped` | The element was not carried at all. | Decide whether you need it. Nothing about it survived, and calling it lossy would imply a setting was applied in some reduced form when it was ignored entirely. A dropped `webhookId` means this installation minted its own binding for the node. |

Each issue names the node (`nodeName`, `nodeId`), the original n8n `type` and
`typeVersion`, and where there is one, the specific `field` — `pinData`,
`options.queryReplacement`, `retryOnFail`. The `reason` is a full sentence
written for a person.

The `dropped` severity is also the project's own progress signal. The
error-handling set was dropped for exactly as long as the runner did not honour
it, and the importer stopped reporting it in the same change that taught the
runner the modes — which is why those diagnostics disappear on their own rather
than being curated away.

## The migration, step by step

### 1. Export from n8n

Use n8n's own workflow export. The importer reads the standard document — `name`,
`nodes`, `connections`, plus `settings`, `pinData`, `meta` and `staticData`,
which it reports on rather than carrying. There is no KilasFlow-specific export
format and nothing to install on the n8n side.

:::caution[Two nodes with the same name cannot be imported]
n8n keys its connections by node **name**, not by id, so two nodes sharing a
name make the graph ambiguous. The importer refuses the whole file rather than
importing something that routes wrongly. Rename one of them in n8n and export
again.
:::

### 2. Import

```bash
curl -sS -X POST https://your-host/api/v1/workflows/import \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --slurpfile wf ./my-workflow.json \
        '{format:"n8n", workflow:$wf[0], name:"My workflow (imported)"}')"
```

The workflow is saved through the ordinary draft path — the same validation, the
same revision history, no import-specific write route. A malformed file comes
back as `422` with the exact reason. Anything else comes back as `201` with a
`Location` header and this body:

```json
{
  "workflow": { "id": "wf_01J…", "name": "My workflow (imported)", "…": "…" },
  "unsupported": [
    {
      "severity": "blocking",
      "nodeName": "Send message",
      "nodeId": "n8n-node-0a1b2c3d",
      "type": "@devlikeapro/n8n-nodes-waha.WAHA",
      "typeVersion": 202502,
      "field": "credentials",
      "reason": "this node used wahaApi \"WAHA account\" in n8n. Credential identifiers belong to the instance they came from, so the node was imported unbound: attach a local credential before activating."
    },
    {
      "severity": "dropped",
      "field": "pinData",
      "reason": "pinned test data is an n8n editor feature with no KilasFlow equivalent; it was not carried, so the nodes that had it pinned will run for real"
    }
  ],
  "webhooks": [
    {
      "nodeId": "n8n-node-4e5f6a7b",
      "method": "POST",
      "path": "waha-trigger",
      "url": "/webhook/9f2c1ae0b41d6c8e5a7f30b2d4e19c86"
    }
  ]
}
```

The report is part of a **success** response, not an error. An import that
carried most of a workflow and named the rest is far more useful than one that
refused the whole file over a single node.

### 3. Read the diagnostics

Sort by severity and start at the top:

```bash
… | jq '.unsupported | group_by(.severity) | map({severity: .[0].severity, count: length})'
```

If there are no `blocking` entries, the workflow will activate. If there are,
each one names the node to open.

### 4. Recreate credentials

**Credentials are never carried, and this is deliberate.** An n8n credential
reference is `{id, name}` scoped to the instance it came from: the id names a
row in somebody else's database and means nothing here. Carrying it would leave
a node that looks configured and fails at run time, which is the worst of both
outcomes.

So the reference is dropped and the node arrives visibly unbound — but not
silently. The diagnostic names the credential **type** and the **name** the
workflow was authored against, so you know what to create:

> this node used `wahaApi` "WAHA account", `postgres` "Production DB" in n8n.
> Credential identifiers belong to the instance they came from, so the node was
> imported unbound: attach a local credential before activating.

Create the equivalent credential in KilasFlow and attach it to the node. In
practice this is the single largest category of blocking diagnostics on a real
import, and there is no way around it — the secrets were never in the export
file in the first place.

### 5. Re-point your webhooks

**Every webhook URL changes, and nothing outside KilasFlow will find out unless
you tell it.**

n8n mints its own route and stores it as an opaque `webhookId`, so an imported
trigger carries no path at all — and a trigger with no path binds no route,
which means the workflow activates and receives nothing. The importer therefore
invents a label from the node's own name, slug-cased, and reports it as
`dropped` because a value you did not write should never appear in your workflow
silently. Rename the label freely; it is only a label.

The public URL is a separate thing: a route of sixteen random bytes, minted per
tenant, per workflow, per trigger node, and returned in the import response's
`webhooks` array. Prefix it with your host and that is the address to configure
in the sending system:

```
https://your-host/webhook/9f2c1ae0b41d6c8e5a7f30b2d4e19c86
```

Two properties of that route are worth understanding, because they explain why
the template's original path could not simply be reused:

**It is opaque, so it is not guessable.** A webhook endpoint is very often
unauthenticated, and an unguessable route is a real defence for one.

**It is per tenant and per workflow, so templates do not collide.** An n8n
template ships a hardcoded path. Importing the same template twice — for a
second client, say — would have both workflows claiming the same endpoint, and
the second activation would fail. Keeping the template's path as a display
label and routing on a minted route means both imports keep the path they came
with and neither collides. Within one tenant the old rule still holds: two
active workflows cannot claim the same label.

The route is minted the first time it is needed and reused forever after —
which is at import, and is why the URL is already in the import response.
Deactivating and reactivating a workflow does **not** change its URL, because a
route minted per activation would break every sender already configured against
it. The address does not *answer*, though, until the workflow is active: the
routable binding is created inside the activation transaction, so there is no
window in which a workflow is active but unroutable, or routable but inactive.

:::tip[Import each source instance into its own tenant]
Because webhook labels are unique per tenant and routes are globally unique and
opaque, an agency migrating several clients off separate n8n instances should
give each client its own KilasFlow tenant. The same template then imports
cleanly for every one of them, each with its own distinct URL, with no renaming
and no collisions. See [Embedding and multi-tenancy](/guides/embedding/).
:::

### 6. Activate and verify

Activation compiles the workflow. If a `blocking` diagnostic is still
outstanding, compilation fails and names the node — an unsupported placeholder
reports:

> this node was imported from `n8n-nodes-base.convertToFile`, which KilasFlow
> does not support. Replace it before activating or running this workflow

Once it activates, run it once by hand before pointing live traffic at it. The
diagnostics tell you what changed structurally; they cannot tell you whether a
`dropped` `alwaysOutputData` matters to the branch downstream of it.

## Expressions

The dialects differ in exactly one way that matters, and the importer translates
it in both directions.

n8n marks an expression by prefixing the string with `=`. KilasFlow marks one
with an explicit object. The interpolation syntax inside — `{{ … }}` — is the
same.

In n8n:

```json
{ "parameters": { "url": "=https://api.example.com/users/{{ $json.userId }}" } }
```

The same thing in KilasFlow:

```json
{
  "parameters": {
    "url": {
      "mode": "expression",
      "value": "https://api.example.com/users/{{ $json.userId }}"
    }
  }
}
```

The explicit marker exists to remove an ambiguity. In n8n a fixed string that
genuinely begins with `=` is unrepresentable; here, a fixed string containing
`{{ }}` is still just data, and only a value carrying the marker is evaluated.

The grammar is JavaScript expression syntax over a closed surface. Roots, field
and index reads, calls, operators, the ternary, optional chaining, template
literals, array and object literals and arrow functions all parse; what does not
exist is a host. `require('fs')` is not blocked by a denylist — it cannot be
written, because there is no `require` in the surface and no way to reach a
module, a file or a process from a parameter. Statement-level code is refused
too: a parameter is an expression, not a program. Practically, the n8n
expressions that survive an import are the ones that read data and transform
it:

```
{{ $json.email.trim().toLowerCase() }}
{{ $node["Fetch user"].json.name }}
{{ $now.plusDays(7).format('yyyy-MM-dd') }}
```

Two differences to look for in your own workflows. **A statement will not
parse**: an n8n expression that assigns, declares a variable or spans several
statements has to become a node or a Go Code node, even though its operators and
arrow functions would have parsed. And **`$('Name').item` fails
loudly rather than guessing**: when the paired-item lineage genuinely cannot be
established, after a node that changed the item count or merged unrelated
streams, it reports the reason instead of falling back to the first item, which
is an answer that is correct only when every node processed exactly one item.

## The database nodes

The PostgreSQL and MySQL nodes carry n8n's whole operation set — `executeQuery`,
`insert`, `update`, `upsert`, `select`, `deleteTable` — and three specifics are
worth knowing before you import one.

**`deleteCommand` is always written explicitly, in both directions, because the
two systems default it differently.** n8n's default is `truncate`; this node's is
`delete`. A node whose author never opened that dropdown carries no key at all,
so letting the absence cross the boundary would mean each side read its own
default and an "empty this table" would silently become a row delete. The
importer therefore writes `truncate` when n8n stored nothing, and the exporter
writes `delete` when KilasFlow stored nothing. A default never crosses.

**Query replacements are split on every comma, because that is what n8n does.**
n8n stores bound values as one comma-separated string with no escape, and splits
on the comma at run time — which means a value containing a comma was already
two values over there, and there is nothing in the stored document that could
say otherwise. The importer reproduces n8n's own splitting exactly, including
its quirk of dropping an empty entry from `a,,b` while keeping a whitespace-only
one from `a, ,b`, and reports the resulting count as **lossy** rather than
guessing:

> n8n's query replacements were split on the comma into 3 bound values, which is
> what n8n itself does with them — its format has no escape, so a value
> containing a comma was already two values there and is two here

Check that number against what your query expects. Going forward, set the node's
own **Query Parameters** field to a JSON array, which has no such ambiguity.

**Three options are kept on the form and not acted on.** They are named rather
than silently ignored, and a test fails if a fourth is ever added without either
an implementation or an entry on this list:

| Option | Why it does nothing |
| --- | --- |
| `delayClosingIdleConnection` | This server opens a connection per node run and closes it when the run ends, so there is no idle connection to delay closing. |
| `queryReplacement` | n8n stores bound values here as a comma-separated string; this node binds a JSON array in its own Query Parameters field, and the importer translates one into the other rather than keeping both. |
| `treatQueryParametersInSingleQuotesAsText` | It governs n8n's own textual substitution, which this node does not do — every value is bound. |

Any *other* option a newer n8n writes is filtered out on import and reported as
dropped, naming the keys. That filtering is not fussiness: the node's own
validator refuses a key this server does not know, so passing an unknown option
straight through would turn an import into a workflow that cannot even be saved.

## Going back to n8n

Export is the same operation in reverse:

```bash
curl -sS -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  "https://your-host/api/v1/workflows/$ID/export?format=n8n" | jq '.workflow'
```

The response carries the n8n document, a `lossy` list in the same three
severities, and the `supportedMappings` list.

**An unsupported placeholder goes back as the node it came from**, whole — its
parameters, credentials, notes and retry policy, all from the capsule. The node
came from n8n and belongs there.

**A KilasFlow node with no n8n equivalent is exported under its own type.** n8n
will not recognise it, will show it as unrecognised, and will refuse to run the
workflow. That is the honest outcome and it is a deliberate change from omitting
the node: omitting it took every edge that touched it too, so a linear workflow
came out as two disconnected halves and looked complete while running only the
first part. Keeping the node keeps the graph's shape, and the one node that
cannot work says so where it is.

**An imported node goes back at the version it arrived at**, not at a fixed pin,
provided n8n publishes that version. A node imported at PostgreSQL 2.4 and
rewritten to the 2.7 pin on the way out would go back with different `DATE`
handling than it arrived with — below 2.7 n8n hands `DATE` columns back as
JavaScript `Date` objects where this server returns RFC 3339 strings — which is
a behaviour change with nothing in the diff to show for it. A node sitting at
exactly the version this server registers was authored here rather than
imported, and does get the pin. Where n8n does not publish the version at all,
the export falls back to the pin, because n8n's type lookup is an exact match
with no resolve-down and a version it does not have makes it refuse to open the
file.

Three things export does **not** carry, and it is worth knowing before you treat
a round trip as lossless:

- **Credential references.** They are KilasFlow identifiers and mean nothing in
  an n8n instance, so they are reported as lost rather than emitted as broken
  references. Reattach credentials in n8n.
- **The per-node error-handling settings** on a mapped node. `continueOnFail`,
  `retryOnFail`, `maxTries` and `waitBetweenTries` are carried *in* on import
  and honoured by the runner, but the exporter writes a node's type, version,
  position and parameters only. An unsupported placeholder, by contrast, returns
  all of them from its capsule.
- **Workflow settings, including the timezone.** The exported document carries
  an empty `settings` object, so n8n will apply its own default zone.

## How far the importer actually gets

Everything above describes the mechanism. This section is the measurement.

### The instrument

A regression corpus of **39 real-world workflows** is scored on every change,
drawn from n8n's own node test fixtures and from the WAHA templates repository —
real workflows written by other people, not examples written to pass. Each is
scored into three cumulative tiers:

| Tier | What it means |
| --- | --- |
| **imported** | `Import` returned no error — the file was read and a document was produced. |
| **activatable** | `Compile` returned no error — the workflow would activate. |
| **runnable** | The workflow ran to completion with every outbound call refused by policy. |

A fourth state matters for reading the score honestly. A fixture recorded as
**blocked** compiled, began running, and was stopped by the offline policy
refusing an outbound HTTP or database call. That is a limit of the instrument —
the harness refuses every outbound call by design — and not a defect in the
workflow. Only a fixture that is neither runnable nor blocked actually failed to
execute.

### The score

**Measured 5 September 2026, over 39 fixtures.**

| Tier | Count | Share |
| --- | ---: | ---: |
| imported | 39 | 100% |
| activatable | 13 | 33% |
| runnable | 3 | 8% |
| _stopped only by the offline policy_ | 10 | 26% |

Read the third and fourth rows together: **every one of the 13 activatable
fixtures either ran to completion or was stopped only by the harness having no
network.** Nothing that compiled then failed on its own terms.

### What the other 26 fail on

The 26 fixtures that do not reach `activatable` fall into four classes, and only
two of them are about import fidelity:

- **Twelve stop at a node that exists only inside n8n's test harness**
  (`n8n-nodes-testing.testData`). These are n8n's own unit-test scaffolding
  rather than workflows anybody runs, and the node will never be mapped. They
  are in the corpus because they exercise the If and Set nodes thoroughly, which
  is what they are useful for.
- **Twelve stop at a credential that does not exist** — nine WAHA, three
  PostgreSQL. This is the expected and correct outcome for a workflow imported
  into an instance where nobody has created a credential yet, and it is the same
  thing you will see on your first import. It is not a fidelity failure.
- **One uses a node type with no mapping** (`n8n-nodes-base.convertToFile`).
  This is a genuine gap, and it is what the placeholder exists for.
- **One loses a branch.** It uses n8n's error output, and the mapped node
  declares no second output for that edge to land on, so the node on that branch
  arrives with no incoming connection and the compiler refuses the graph as
  disconnected from its trigger. This is a real limitation and the honest place
  to point at it.

Set against the node census across the same corpus, the eight most common types
by a wide margin — No Op, Set, Sticky Note, HTTP Request, If, Manual Trigger,
WAHA and Postgres, together well over three hundred instances — are all mapped.
The ninth is `n8n-nodes-testing.testData`, which is the test scaffolding above.

### Two cautions about this number

**It is a floor, not a ceiling, and it has a date on it.** The baseline was
regenerated on 5 September 2026, and six commits touching the importer have
landed since — the AI cluster mappings and the full database operation sets
among them. The measurement has not been re-run against those. Take 33%
activatable as the number that was true on that date and not as a claim about
today, and re-run it yourself rather than trusting this page:

```bash
KILASFLOW_N8N_REFERENCE=/path/to/n8n make corpus
go test ./internal/interop/n8n/corpus -update-baseline
```

**It is not a comparison.** 33% activatable does not mean two thirds of *your*
workflows will fail. Twenty-four of the 26 failures are a credential nobody
created or n8n's own test scaffolding. What the number is good for is the thing
a number is normally bad for: it is a measurement this project publishes about
itself, including the parts that look bad, so that when the mapping table says
51 types you have some reason to believe it.

:::note[Why you cannot see the fixtures]
The corpus is measured, never committed. n8n's fixtures are under
`LicenseRef-n8n-sustainable-use`, and the WAHA templates repository carries no
licence file at all — the GitHub API reports `license: null` — which is stricter
than a restrictive licence rather than looser. Both are fetched into a
gitignored directory, pinned by upstream commit and verified per file by
digest. Only the aggregate scores in this section are published. A clean clone
has no corpus, and the corpus-dependent tests skip with the command that fixes
it.

The same boundary explains the whole approach: no n8n source is used, no n8n
package appears in any manifest, and the format is reimplemented from its
observable behaviour rather than adapted. A test in `internal/guardrails`
enforces that on every run rather than trusting anyone to remember it.
:::

## Current limits worth planning around

- **The Code node does not run**, in either language.
- **The node subset is 51 n8n node types**, mapped onto 41 KilasFlow node types
  — `internal/interop/n8n/n8n.go` is the list of record, and the tables above
  name the ones you are most likely to meet. Anything outside it imports as a
  placeholder that blocks activation. If your workflows lean on integrations
  that are not in those tables, import them and read your diagnostics rather than
  guessing: the honest answer is that each one will import, open, and not run
  until you replace that node — with an HTTP Request node against the same API,
  in most cases, since that is what the missing node would have been doing.
- **n8n's error workflow and execution order have no equivalent.** `settings` is
  read for the timezone and the workflow's own `executionTimeout` — both of which
  are carried, and honoured — and the rest is reported as dropped rather than
  quietly applied.

For what exists more broadly and what does not, start with
[what KilasFlow is](/start/what-kilasflow-is/). For the node types available to
replace an unsupported one with, see the
[node pack format](/reference/node-packs/).
