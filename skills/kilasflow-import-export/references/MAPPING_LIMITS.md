# Mapping and limits

The n8n boundary is a translation with a report, in both directions. This file
is what the reports mean, what maps, and what a report costs you. The mapping
table itself is the `mappings` list in internal/interop/n8n/n8n.go, and the
advertised subset is what `supportedMappings[]` returns.

## The two report shapes

An **import** answers with the saved draft, `unsupported[]` and `webhooks[]`,
and the same report is stored with the revision it created. An **export**
answers with `format`, `workflow`, `lossy[]` and `supportedMappings[]`. The two
lists are the same vocabulary in opposite directions: `ImportIssue` and
`ExportIssue` mirror each other field for field, and an issue names the node
(`nodeName`, `nodeId`), the original n8n `type` and `typeVersion`, the specific
`field` where there is one, and a `reason` written as a sentence
(internal/api/handlers/interop.go, internal/interop/n8n/n8n.go).

## The three severities

| Severity | Means | What to do |
| --- | --- | --- |
| `blocking` | the workflow cannot run as imported | fix it: the node is a placeholder or a foreign Code node |
| `lossy` | the element was carried, but differently | read it and decide; the workflow still activates |
| `dropped` | the element was not carried at all | decide whether you needed it — nothing about it survived |

`dropped` is deliberately not a soft `lossy`: saying a setting was carried in
some reduced form when it was ignored entirely would imply it was applied
(`IssueSeverity` and `ExportIssue` in internal/interop/n8n/n8n.go).

## What has no equivalent: node types

A node type outside the mapping list imports as `kilasflow.unsupported`. It is
designed behaviour, not a failure (internal/interop/n8n/n8n.go,
nodes/unsupported.go):

- the placeholder keeps the original `type`, the original `typeVersion` and a
  capsule holding the whole source node — its parameters, its credential
  reference, its notes, its retry policy;
- it renders where the original node was, so the graph keeps its shape;
- its validator always fails and compilation is what gates activation and
  running, so the draft saves, opens and can be edited and can never be
  activated or run until the node is replaced;
- it is registered at four arities — one, two, four and eight ports — and the
  import picks the smallest that covers the edges the source workflow drew,
  because the registry is keyed by type and version and one definition cannot
  have a variable port count (`unsupportedArities`, `placeholderFor`).

The honest answer for most of these is an HTTP Request node against the same
API, since that is what the missing node was doing.

## What has no equivalent: the Code node

An n8n Code node is JavaScript or Python, neither of which runs here, and it is
not translated — a one-line `items.map(…)` translates cleanly and the next body
translates into something that compiles and computes something else. It becomes
`kilasflow.foreignCode`, a distinct type from the generic placeholder: it keeps
your source verbatim in `jsCode` or `pythonCode`, refuses to compile, and names
a likely replacement based on what the body does — a `.reduce(` points at
Aggregate or Summarize, a `.sort(` at Sort, a `fetch(` at HTTP Request. Your two
ways forward are the native nodes (Filter, Switch, Set, Sort, Aggregate, Split
Out, Summarize, Remove Duplicates) or rewriting the body in `kilasflow.code`,
the Go Code node that compiles to WebAssembly and runs under a time and memory
limit (nodes/jscode.go, docs/src/content/docs/guides/n8n-migration.md).

## Document-level elements

Four things on the n8n document are read and then not carried, or carried only
in part (`documentIssues` and `importSettings` in internal/interop/n8n/n8n.go):

| Field | What happens |
| --- | --- |
| `settings` | beyond the timezone, the workflow's own `executionTimeout` and `errorWorkflow`, reported **dropped**: execution order, the save-data flags and the rest have no equivalent yet |
| `pinData` | reported **dropped**: pinned test data is an editor feature, and the nodes that had it pinned will run for real |
| `meta` | reported **dropped**: instance metadata about where the workflow came from means nothing in another installation |
| `staticData` | reported **dropped**: per-workflow scratch space its nodes persist between runs, with no equivalent here |

Three of those carried keys are **lossy** rather than clean: a timezone this
server cannot resolve is not carried (schedules run in UTC), n8n's `-1` "no
timeout" is left uncarried because this server always applies its own budget,
and an `errorWorkflow` is carried as the id n8n held — which resolves only if
that workflow was imported too.

## Node-level elements

On a **mapped** node, exactly two fields are reported **dropped**
(`nodeIssues` in internal/interop/n8n/n8n.go):

- `notes` — the node's note has no equivalent;
- `webhookId` — n8n's per-node webhook identity is meaningless here; this
  installation mints its own binding on activation.

Everything else on a mapped node crosses and is honoured: `disabled`, `onError`
(the legacy `continueOnFail` boolean kept beside it), `alwaysOutputData`,
`executeOnce`, `retryOnFail`, `maxTries` and `waitBetweenTries`. The two numbers
are **clamped rather than refused** — `maxTries` to `workflow.MaxRetryAttempts`
(8), `waitBetweenTries` to `workflow.MaxRetryWaitMilliseconds` (300000) — so a
workflow with a larger budget still imports and one typo cannot become thousands
of calls (`errorHandlingSettings`, internal/workflow/compiler.go). An
unsupported node keeps all of this in its capsule instead, and gets it back on
export.

## Going back out

`kilasflow workflow export <workflowId> --format n8n` (`export-workflow`)
reports what the return trip could not carry (internal/interop/n8n/n8n.go):

- **an unsupported placeholder** returns as the node it came from, whole, with
  a `lossy` note saying so — it came from n8n and belongs there;
- **a KilasFlow node with no n8n equivalent** is emitted under its own type so
  the graph keeps its shape and edges; n8n will not recognise it and will refuse
  to run the workflow, which is the honest outcome;
- **credential references are not exported**: they are KilasFlow identifiers,
  meaningless in an n8n instance, so they are named as lost and you reattach
  credentials there;
- **a node authored at an n8n typeVersion the translator does not write** is
  exported at the version whose parameter shape was translated: n8n does not
  publish the original, and throws on a version it does not publish.

## Expressions

n8n marks an expression by prefixing a string with `=`. KilasFlow marks one with
`{"mode":"expression","value":"…"}`, and the `{{ … }}` interpolation inside is
the same, so `{{ $json.email.trim() }}` and `$node["Fetch user"].json.name`
survive (internal/expression/expression.go). Two differences decide what
imports: an n8n expression that assigns, declares a variable or spans several
statements does not parse here — a parameter is an expression, not a program —
so it must become a node or a Go Code node, and `$('Name').item` fails loudly
rather than guessing when the paired-item lineage cannot be established
(docs/src/content/docs/guides/n8n-migration.md).

## Limits worth planning around

- **The Code node does not run**, in either language.
- **Anything outside the mapping list imports as a placeholder** that blocks
  activation. Import and read your diagnostics rather than guessing from a
  table: the subset is a measured claim, and `supportedMappings[]` on an export
  is the list this server will stand behind today.
- **n8n's execution order and the other global settings have no equivalent**:
  `settings` is read for the timezone, the workflow's own `executionTimeout` and
  `errorWorkflow`, and the rest is reported dropped rather than quietly applied.
- **A revision's report is frozen with it**: read diagnostics on the revision an
  import created, and export the latest revision rather than an unsaved draft.
