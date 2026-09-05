# KilasFlow V2 — "n8n-first" roadmap: Pine epic + phase tickets

## Context

KilasFlow V1 is complete (`EPIC-c7gbdp`, 19/19 tickets, Milestones 0–7) and the editor
revamp shipped (`FEAT-ezeap5`, commits `b80890e` + `8d531d3`). The owner now wants a
post-V1 direction that is explicitly **n8n-first**: a customer's existing n8n workflows —
above all ones built on WAHA (WhatsApp HTTP API) — should import into KilasFlow and
actually run, so client automations can be replicated on his own platform.

**This plan produces planning artifacts only.** Executing it means creating one Pine epic
plus the phase tickets below, each with an implementation plan written into its ticket
body. No product code is written while executing this plan.

### What research changed about the original idea

Three parallel research workflows (24 agents: KilasFlow source, the local n8n 2.34.0
reference checkout, the owner's own published `n8n-nodes-mitrachat` package, and official
docs) overturned four assumptions:

1. **WAHA needs no JavaScript.** `@devlikeapro/n8n-nodes-waha`'s action node has **no
   `execute()`** — it is 124 OpenAPI-derived operations of declarative `routing:
   {request:{…}}` metadata generated from WAHA's own MIT `openapi.json`. A Go
   declarative-routing interpreter plus a generated node pack replicates the whole package.
2. **A JS sidecar would run 0.46% of what n8n users run.** In the 100 most-viewed n8n.io
   templates (2,377 node instances): 1,894 are `n8n-nodes-base`, 472 are
   `@n8n/n8n-nodes-langchain`, and only 11 are third-party `n8n-nodes-*`.
3. **The licence boundary is real.** `n8n-workflow`, `n8n-core`, `n8n-nodes-base` and the
   LangChain pack are all Sustainable Use License. KilasFlow is Apache-2.0 and its ICP is
   white-label, multi-tenant, embedded — the configuration n8n's licensing FAQ names as not
   allowed. Even MIT-licensed WAHA imports `VersionedNodeType`/`NodeConnectionType` from
   `n8n-workflow` as runtime values, so executing the npm package drags SUL code in.
4. **The engine, not the node catalogue, is the bottleneck.** The compiler rejects cycles
   and demands exactly one trigger root, which fails 52 of those 100 templates before node
   types are even considered; and 511 of 1,617 expressions use `$('Node').item`, which needs
   pairedItem lineage the runner does not track.

### Decisions locked by the owner

| Decision | Answer |
|---|---|
| JS sidecar | **Deferred** to the long tail. WAHA goes native via an OpenAPI→node generator. |
| First phase | **Engine correctness + import fidelity**, before node metadata. |
| Licence posture | **Native-first, no n8n bytes** in repo or image, no contact with n8n. |
| `n8n-nodes-base` coverage | **Top-30 by real usage data.** |
| Workflow versioning | **DB-stored workflow history** (n8n's own model), not Git source control. |
| PostgreSQL | Optional, but **unlocks extra features**; shares a customer DB under a `kflow_` prefix. |
| AI nodes | **Native Go**, mapped on import. Microsoft Agent Framework stays an optional adapter behind `ai.AgentRuntime`, evaluated by spike — it is **not** currently implemented (`internal/ai/maf/` is a doc.go stub, no dependency in `go.mod`). |
| Datastore storage (p9) | **One physical table per datastore**, created by runtime DDL under the `kflow_` prefix, plus a catalogue — matching n8n. Chosen by the owner. Byte quotas are explicitly not a requirement; the three remaining costs become acceptance criteria. See the p9 section. |
| Database node scope (p4) | **Full n8n operation parity** — Delete, Execute Query, Insert, Insert or Update, Select, Update. |

## Structure to create in Pine

One epic, `phase: p0…p9`, mirroring how V1 was tracked:

```
EPIC  KilasFlow V2 — n8n-first workflow compatibility
 ├─ p0  Reference and guardrails            (3 tickets)
 ├─ p1  Engine correctness + import fidelity (13 tickets)  ← starts here
 ├─ p2  Node metadata foundation             (9 tickets)
 ├─ p3  Declarative node packs + WAHA + Telegram (8 tickets)
 ├─ p4  n8n-core node parity, top-30         (5 tickets)
 ├─ p5  AI parity, native Go                 (9 tickets)
 ├─ p6  PostgreSQL capability tier           (6 tickets)
 ├─ p7  Workflow history                     (2 tickets)
 ├─ p8  Platform and long tail               (7 tickets)
 └─ p9  Datastore                            (new — see below)
```

Each ticket gets: `## Scope`, `## Acceptance criteria`, `## Implementation Plan` with the
file paths below, and `deps` wiring the order.

**Acceptance scenario for the whole epic** (write it into the epic body): the official WAHA
"chatting" template imports, opens in the editor with correct icons and parameter panels,
activates, receives a real WhatsApp webhook, and replies — with the same template imported
twice for two different clients. A Telegram bot proves the same path earlier and without
WhatsApp infrastructure: a Telegram Trigger that registers its own webhook, an AI Agent, and
a Send Message reply.

---

## p0 — Reference and guardrails

**V2-p0-1 · Widen the n8n reference checkout and vendor the WAHA spec.**
`/Users/izzadev/projects/mitrachat/n8n` is a real but narrowed clone of n8n 2.34.0
(`--depth 1 --filter=blob:none` + cone sparse checkout, 911 of 26,341 files). Widen in
place — no re-clone, a few MB:
`git -C … sparse-checkout add packages/@n8n/nodes-langchain packages/core/src/nodes-loader
packages/cli/src/modules/community-packages packages/@n8n/node-cli
packages/@n8n/eslint-plugin-community-nodes packages/frontend/editor-ui/src/components`.
Two existing patterns are broken (`ParameterInput.vue` / `ParameterInputList.vue` were
written as cone-mode directory patterns, so `src/components/` is absent). Separately clone
`github.com/devlikeapro/n8n-nodes-waha` (MIT) for its `openapi.json`, both `202409` and
`202502`. Deliberately exclude `packages/@n8n/ai-workflow-builder.ee` and every `.ee` file —
those are outside the SUL. Everything stays outside the repo and gitignored.

**V2-p0-2 · Importer corpus fixtures.** Collect the official WAHA templates, the owner's own
client workflows, and the authentic workflow JSON fixtures already on disk in the reference
checkout (`nodes-base/{HttpRequest,Set,If}/test/**`) into a **gitignored, digest-pinned**
corpus — never committed, because the WAHA templates repository carries no licence at all.
These become the regression corpus every p1–p4 ticket measures itself against:
`imported / activatable / executable` counts.

**V2-p0-3 · Record the licence boundary.** `pine learn` into `.pine/memory/licensing.md`:
never vendor n8n source, never add an n8n npm package to any manifest, never ship n8n bytes
in an artifact; what we copy is the *interoperability format* (connection-type strings,
workflow JSON shape, codex conventions), which is format fact, not code. Note the `.ee`
carve-out and that KilasFlow's own LICENSE stays Apache-2.0.

---

## p1 — Engine correctness and import fidelity ← **first phase**

Every ticket here is measured against the p0 corpus.

**V2-p1-1 · Branch pruning.** `internal/engine/runner.go` runs every node unconditionally;
`dependenciesComplete` only checks that the upstream node *ran*, not that the connecting
port carried items. Combined with the synthetic-empty-item substitution in `nodes/http.go`,
`nodes/ai.go`, `internal/sqlnode`, and the respond executor, a not-taken IF branch still
fires one HTTP call, one LLM call and one webhook response. A node must run only when at
least one incoming `main` edge delivered items. Also fixes the nondeterministic response
when two Respond-to-Webhook nodes sit on opposite branches (`findResponse` iterates a Go map).

**V2-p1-2 · pairedItem lineage and runIndex.** Track item provenance through the runner so
`$('Node').item` resolves the *paired* item rather than the first item of the first
non-empty port (`runner.go` ~257-264). This is 32% of expressions in the corpus.

**V2-p1-3 · Multiple trigger roots.** Relax `internal/workflow/compiler.go`'s
`len(roots) != 1` rule so a workflow may carry a webhook *and* a schedule trigger, each
starting its own item flow.

**V2-p1-4 · Bounded loops.** `hasCycle` rejects any cycle, so n8n's Split-In-Batches
feedback pattern is unrepresentable. Allow a cycle through a designated loop node with an
explicit iteration bound and per-iteration evidence.

**V2-p1-5 · Honour the error-handling settings.** `continueOnFail`, `retryOnFail` and
`maxTries` are declared in `nodes/core.go` (~121-131) and rendered by the editor, but no Go
code reads them — only `timeoutSeconds` is implemented. Implement them, then let the
importer map n8n's equivalents (mapping them before this lands would ship a lie).

**V2-p1-6 · Make redaction non-destructive.** `internal/execution/redact.go` runs at webhook
ingest and on every executions write, and the runner rehydrates the trigger item from the
redacted record. `session` and `sessionid` are on the sensitive-key list, so WAHA's
`$json.session` becomes `[redacted]` permanently and n8n-style AI memory keyed on
`sessionId` collapses every user into one bucket. `looksLikeCredential` also destroys any
string starting with `basic `, so a WhatsApp message reading "basic plan pricing?" never
reaches the agent. Move redaction to the read boundary, or shrink the key list — decide in
the ticket, but the running item must carry real values.

**V2-p1-7 · Per-trigger webhook payload shaping and raw-body capture.**
`internal/webhook/webhook.go` (~196-250) hardcodes one item shape
`{method,path,headers,query,body}` for every trigger and re-marshals the body through
`map[string]any`, so raw bytes are gone. n8n core emits `{body, headers, params, query}`;
WAHA templates read the envelope at `$json` top level. Add node-type dispatch for the
payload shape and retain raw bytes so HMAC verification becomes possible.

**V2-p1-8 · Per-tenant webhook paths and delivery dedupe.**
`uidx_webhook_bindings_route` in `internal/repository/models.go` is unique on
`(method, path)` across all tenants, while n8n templates ship a hardcoded path — so
importing the same WAHA template for a second client fails at activation, which is exactly
the stated business goal. Scope the index by tenant and have the importer mint a fresh path,
surfacing the new URL. Add dedupe on `X-Webhook-Request-Id` (WAHA retries 15× at 2s).

**V2-p1-9 · Sticky Note and a lossless unsupported capsule.** Sticky Note is the single
most-deployed n8n node and today becomes `kilasflow.unsupported`, whose validator always
fails — so *every* real imported workflow is unactivatable. Add a zero-port annotation node,
and upgrade the placeholder (`internal/interop/n8n/n8n.go` ~241-243) from
`{type, typeVersion, parameters}` to a lossless capsule carrying `credentials`, `disabled`,
`notes`, `webhookId`, the error-handling fields and real port arity. Also fix the silent
multi-port collapse on placeholder export (the `continue` that skips `portIndex`).

**V2-p1-10 · Expression engine v2.** Today the grammar is root + `.field` + `[index|"key"]`
and nothing else; a missing path is a hard error where n8n returns undefined;
`$node["X"].json.y` does not work because node outputs map a name straight to the item JSON;
there is no `$now`, no functions, no `$fromAI`. Add soft-undefined, the `json` wrapper,
`$('Node').item`, `$now`/`$today`, a function allowlist, and `$workflow`/`$execution`. Keep
the two dialects explicit: n8n marks expressions with a leading `=` on a plain string,
KilasFlow uses `{"mode":"expression","value":…}`. Update the stale package doc in
`internal/expression/doc.go` and the duplicated root allowlist in
`web/src/lib/components/workflow-editor/property-field.svelte`.

**V2-p1-11 · Float typeVersion.** `node.Definition.Version` is an `int` and the registry key
is `{type, version int}`, while n8n uses floats (`4.2`, `3.4`) and WAHA uses YYYYMM
integers (`202409`, `202502`). `internal/interop/n8n/n8n.go` currently truncates with
`int(node.TypeVersion)` and collapses every import to version 1. Widen the type end to end
and add `defaultVersion` plus version dispatch.

**V2-p1-12 · Import diagnostics.** `settings`, `pinData`, `meta`, `staticData`, node
`notes`, `webhookId` and every error-handling field are dropped with no report. Every drop
must produce a diagnostic the import screen can show. Fix the export bug that writes
`responseMode: "immediate"`, which is not in n8n's enum (`parameters.go` ~475).

**V2-p1-13 · Stop dropping AI edges on import.** `n8n.go` (~327-336) discards
`ai_languageModel`/`ai_memory`/`ai_tool` connections behind a stale comment claiming the
node family is unsupported — KilasFlow has had those kinds since V1. Map them onto the
canonical kinds so p5 can land against real imported graphs.

---

## p2 — Node metadata foundation

**V2-p2-1 · Presentation metadata on `Definition`.** Add `Icon` (with light/dark variants),
accent colour, `Group` (trigger/action/transform — behavioural, distinct from panel
category), `Subtitle` template, `DocumentationURL`, and codex-style
categories/subcategories/alias. `internal/node/registry.go`'s `Definition` is serialized
*directly* as the `/api/v1/node-types` payload (`internal/api/handlers/nodes.go`), so every
field change is an OpenAPI change requiring `pnpm generate:api` and the SDK's
`generate:types` or the drift checks fail.

**V2-p2-2 · Property kinds.** Six kinds exist (string, number, boolean, select, keyValue,
conditions). Add the ones WAHA and the AI nodes actually need: `options`/`multiOptions`,
`collection`, `fixedCollection`, `notice`, `json`, `dateTime`, and a `typeOptions` bag
(password, multiline/rows, min/max, multipleValues). Note that with `multipleValues`, n8n's
`default` describes one element, not the collection. This ticket explicitly **defers**
`resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` — V2-p2-10 and
V2-p2-11 pick the first two up, and V2-p4-13 picks up `assignmentCollection`.

**V2-p2-3 · displayOptions parity.** `VisibilityCondition{Key, Equals}` is single-value AND
equality. n8n's semantics: `show` requires all listed keys to match, `hide` hides if any
key matches, values are arrays, plus `@version` gating and one escape hatch that is easy to
miss — if a controlling parameter holds an expression, the dependent parameter is always
shown. Also decide explicitly that hidden parents do **not** suppress children, because
imported nodes depend on that answer.

**V2-p2-4 · Dynamic options endpoint.** `POST /api/v1/node-types/{type}/load-options`
evaluated against a partially configured node, for "list my WAHA sessions" and "list
OpenRouter models". Keep the loader declarative (`{endpoint, method, itemsPath,
labelTemplate, valueField}`) so it never runs user code. **Amendment required for p9:** the
loader as written is an *outbound HTTP* descriptor routed through `internal/safehttp`, and a
datastore list or an `information_schema` table list is an *internal* lookup to which none
of its seven SSRF acceptance criteria apply. Add an internal-source kind, and bound an embed
session's returned option values by its `WorkflowID` — `permits()` at
`internal/api/middleware/embed.go:94-97` currently allows the whole `/node-types/` subtree on
read scope, so an internal loader would otherwise let a workflow-scoped embed session
enumerate every datastore and every table reachable by any credential in the tenant.

**V2-p2-5 · Serve icons and delete the hardcoded frontend maps.** Node identity is
hardcoded in three files the API cannot reach: `ICONS` and the subtitle switch in
`web/src/lib/workflow-editor/node-visual.ts`, and `BY_NODE_TYPE` in
`web/src/lib/workflow-editor/credentials.ts` — a node with no entry renders as a grey `Box`
with no credential picker at all. Add a Go icon route plus an XSS-safe render path, and make
all three maps server-driven.

**V2-p2-6 · Credential type registry.** Credential types are a closed package-level map in
`internal/credentials/`, and `Definition` cannot declare which types a node accepts. Open
registration, let a node declare `Credentials []CredentialRequirement`, describe credential
fields in the same property language, and add a declarative test request.

**V2-p2-7 · Source-tagged registry.** Keep `internal/node.Registry` immutable per load but
make it composite, tagging each definition `builtin | pack | sidecar` with namespacing rules
and conflict precedence. Both registries are already string-keyed and source-agnostic, so
this is mostly a loading-order change. Add the missing test that every registered
`ExecutorID` actually has an executor — forgetting one compiles, passes every test, and
fails only at run time.

**V2-p2-8 · Port descriptors and wider connection kinds.** Ports carry only `{Name, Kind}`.
Add `displayName`, `required`, `maxConnections` and a node filter, and widen
`ConnectionKind` toward n8n's 13 values (`ai_outputParser`, `ai_embedding`,
`ai_vectorStore`, `ai_chain`, `ai_document`, `ai_textSplitter`, `ai_retriever`,
`ai_reranker`, `ai_agent`). Note the exact casing — `ai_languageModel`, not
`ai_language_model` — and that `workflow.Port` has no JSON tags today, so it serializes with
capital keys the SPA reads; adding tags is a breaking client change. Decide here whether
ports may be computed from the node's own parameters, which n8n needs for the Agent's
conditional slots.

**V2-p2-9 · Registry-driven webhook bindings and trigger lifecycle hooks.**
`internal/webhook/webhook.go` extracts webhook bindings for exactly **one hardcoded node
type** (~393-397, wired at `cmd/kilasflow/main.go` ~138), so no third-party or generated
trigger — Telegram, WAHA, or anything from a pack — can bind a path. Make extraction
registry-driven, and add activate/deactivate lifecycle hooks equivalent to n8n's
`webhookMethods.checkExists / create / delete` so a trigger can register and unregister a
webhook with a remote service when a workflow is activated. Telegram requires this
(`setWebhook`); WAHA's optional auto-registration in p3-4 reuses it.

**V2-p2-10 · The `resourceLocator` property kind and an internal list-search seam.** *(new;
deps V2-p2-2, V2-p2-4)* n8n's Postgres node expresses Schema and Table as resource locators
with From list / By Name / By ID modes, and the Datastore node needs the same control to pick
a datastore. V2-p2-2 defers this kind and, until now, **no ticket picked it up** — a grep of
the whole corpus finds `resourceLocator` in exactly one place, the sentence deferring it.
Without it, every table and datastore picker degrades to free text.

**V2-p2-11 · The `resourceMapper` property kind.** *(new; deps V2-p2-10)* The column-mapping
control n8n's Postgres node has carried since typeVersion 2.2: `mappingMode` of
`autoMapInputData` or `defineBelow`, per-column type and required metadata fed by a schema
loader, and multi-key `matchingColumns`. Without it, insert / update / upsert degrade to an
untyped key-value bag with no matching-column selection — a form strictly worse than the raw
SQL box it would replace.

---

## p3 — Declarative node packs, WAHA and Telegram

**V2-p3-1 · Declarative routing interpreter.** Implement the subset of n8n's routing model
in Go: `requestDefaults` (baseURL, headers), `routing.request` (method, url, body, qs),
`routing.send` (property → parameter placement), `routing.output.postReceive` (rootProperty,
setKeyValue, limit), and pagination. Every call goes through `internal/safehttp` and the
credential store, so SSRF policy and per-credential `AllowedDomains` still apply — something
a JS sidecar could not have preserved.

**V2-p3-2 · OpenAPI → node pack generator.** A build-time generator that turns an OpenAPI
document into a node pack. It must reproduce `@devlikeapro/n8n-openapi-node`'s naming
exactly or imported workflows will not match: `resource` = `lodash.startCase(tag stripped of
non-alphanumerics)` and `operation` = `startCase(operationId minus its first "_" segment)` —
so the values in real workflow JSON are human-readable strings with spaces (`"Chatting"`,
`"Send Text"`), not slugs. Only first-level request-body properties become structured
parameters; nested objects arrive as JSON-string expressions.

**V2-p3-3 · WAHA node pack.** Generate from the vendored MIT `openapi.json` for both
`202409` and `202502`, with the `wahaApi` credential type (base URL + `X-Api-Key`). Carry
the generator's hidden custom defaults — `session` defaults to `={{ $json.session }}` and
`chatId` to `={{ $json.payload.from }}` — because real templates omit those parameters
entirely and treating absent as empty produces silently broken workflows.

**V2-p3-4 · WAHA Trigger.** A fan-out trigger with one output per WAHA event, routed by
`body.event`: 20 outputs in `202409`, 26 in `202502`, with orderings that diverge from index
5, so the index→event table is per typeVersion. The n8n node registers nothing with WAHA and
verifies no signature — decide in the ticket whether KilasFlow does better by optionally
installing its own webhook URL via `PUT /api/sessions/{session}` and verifying
`X-Webhook-Hmac` (sha512 over the raw body, which p1-7 makes possible). Surface an
activation notice when the URL still has to be pasted into WAHA's session config, otherwise
an imported workflow looks correct and receives nothing.

**V2-p3-5 · WAHA import mapping.** Map `@devlikeapro/n8n-nodes-waha.WAHA` and
`.wahaTrigger` — note the different capitalisation inside one package, and never
case-normalise a type string. Handle the YYYYMM typeVersion, and rebind the credential
reference (`{id, name}` is instance-local and meaningless here) rather than trusting or
dropping it. Also support the legacy unscoped `n8n-nodes-waha.*` package.

**V2-p3-6 · Telegram Trigger at n8n parity.** The owner's fast test path: a Telegram bot
should exercise trigger → agent → reply end to end without WhatsApp infrastructure. Match
`n8n-nodes-base.telegramtrigger`: a **Trigger On** multi-select carrying n8n's exact update
list — `*` (all updates except Chat Member, Message Reaction and Message Reaction Count),
Message, Edited Message, Channel Post, Edited Channel Post, Callback Query, Inline Query,
Chosen Inline Result, My Chat Member, Chat Member, Chat Join Request, Poll, Poll Answer,
Pre-Checkout Query, Shipping Query, Message Reaction, Message Reaction Count, Chat Boost,
Removed Chat Boost, Business Connection, Business Message, Edited Business Message, Deleted
Business Messages, Purchased Paid Media — plus the additional fields **Download
Images/Files** (with its Image Size sub-option), **Restrict to Chat IDs** and **Restrict to
User IDs**. Credential type `telegramApi` holding the BotFather token (confirm the exact
`name` against the widened reference checkout). Unlike WAHA, this trigger **registers
itself**: `setWebhook` on activation and `deleteWebhook` on deactivation via p2-9's
lifecycle hooks, with a `secret_token` verified on every delivery. Two things to decide in
the ticket: Telegram requires a public HTTPS URL, so document the tunnel workflow; and
consider an optional `getUpdates` long-polling mode for local development — a KilasFlow
extra n8n does not offer, and the thing that actually makes "test cepat" true on a laptop.
Download Images/Files needs p3-8's binary store.

**V2-p3-7 · Telegram action node at n8n parity.** Match `n8n-nodes-base.telegram`'s
resource/operation surface so imported workflows map 1:1 — **Message**: Send Message, Send
Photo, Send Document, Send Animation, Send Audio, Send Video, Send Sticker, Send Media
Group, Send Location, Send Chat Action, Edit Message Text, Delete Chat Message, Pin Chat
Message, Unpin Chat Message; **Chat**: Get, Get Administrators, Get Member, Leave, Set
Title, Set Description; **Callback**: Answer Query, Answer Inline Query; **File**: Get File.
Send Chat Action is what gives a bot its typing indicator, so it belongs in the first cut
alongside Send Message. Add the importer mapping for both Telegram types, and reuse
p3-1/p3-2 wherever the Bot API is regular enough to be expressed as routing metadata rather
than hand-written Go.

**V2-p3-8 · Binary data storage.** `workflow.BinaryRef` is metadata with nothing behind it
(`internal/workflow/document.go` says payload storage is out of scope), and `nodes/code.go`
drops binary silently. Two features force it: WhatsApp media, and the Telegram trigger's
Download Images/Files option. Implement a binary store and plumb it through items, the HTTP
node, the Code node, WAHA's send-image/send-file operations and Telegram's Send
Photo/Document/Get File.

---

## p4 — n8n-core node parity, top-30

Five tickets grouped by family, each carrying a parity checklist measured against the p0
corpus rather than a node count: **flow control** (Switch, Filter, Merge modes, Split In
Batches, Limit, No Op), **data shaping** (Set/Edit Fields v3 semantics, Aggregate, Sort,
Split Out, Summarize, Remove Duplicates), **time and control** (DateTime, Wait, Schedule
parity), **workflow composition** (Execute Workflow, Execute Sub-workflow, Respond to
Webhook response modes), and **code** (decide whether the JS Code node is emulated, refused
with a clear diagnostic, or mapped onto the existing Go WASM Code node — do not let this
decision leak into the other four tickets).

The database family is the **fifth** node family, and it belongs here rather than in the long
tail: it is the one family with a proven, cited import defect
(`internal/interop/n8n/parameters.go:541-548` imports every n8n operation other than
`executeQuery` as an *empty query*), and the epic's own ordering rationale puts import
fidelity ahead of the long tail.

**V2-p4-6 · Close the five real gaps in the SQL node.** *(no new property kind required)*
Raw SQL already works — `Connection.Query` binds parameters through `QueryContext`
(`internal/sqlnode/sqlnode.go:280`) and fans out one item per row. Exactly five things are
genuinely missing or wrong: bulk writes cost N serial round trips, because the executor loops
per input item (`nodes/database.go:203-214`) against a pool pinned by `SetMaxOpenConns(1)`
(`sqlnode.go:99-101`); `Transaction` cannot return rows, using `tx.ExecContext` exclusively
(`sqlnode.go:367`); the `parameters` property has no `VisibleWhen` (`nodes/database.go:71-74`)
so it is always shown and silently ignored for `transaction`; `maxRows` and `timeoutSeconds`
are expression-capable with no server-side ceiling (`nodes/database.go:218-221`), so
`maxRows: {{ 500000000 }}` from a webhook body is an OOM against the shared process; and
`timeoutSeconds` is declared **twice** with different meanings and defaults — Parameters at 30
(`nodes/database.go:75`) and SharedSettings at 0 (`nodes/core.go:124`) — which
`validateProperties` permits because its `seen` map is per group. Ranked by what a user is
blocked on, this ticket delivers more than the whole builder programme and needs nothing new.

**V2-p4-7 · Credential test-connection.** Nothing in `internal/credentials` or
`internal/api` ever pings a database target, so a credential can only be validated by running
a workflow. n8n ships `postgresConnectionTest` and `mysqlConnectionTest`. Independently
valuable, independently testable, and it serves all three existing database nodes.

**V2-p4-8 · Schema introspection loaders.** *(deps V2-p2-10, V2-p2-11, V2-p2-4 amendment)*
`information_schema` loaders for Postgres and MySQL — schema search, table search, columns,
columns-for-matching, mapping columns with type / nullable / default — feeding the resource
locators and the resource mapper.

**V2-p4-9 · Postgres node operation set.** *(deps V2-p4-8, V2-p1-11, V2-p2-3)* The six n8n
operations with **matching value strings** — `deleteTable`, `executeQuery`, `insert`,
`upsert`, `select`, `update` — and SQL builders reproducing n8n's emitted SQL, with
identifier escaping and value binding equivalent to pg-promise's `:name` and `:csv` filters
under fuzz tests. The version policy is explicit: `kilasflow.postgres` v1 keeps
query/execute/transaction, v2 is the n8n operation set, and the interop `kilasVersion` moves
with it — which is why this needs fractional type versions (V2-p1-11) and `@version` visibility
gating (V2-p2-3), the mechanism n8n itself uses to serve its pre-2.2 and post-2.2 shapes from
one definition.

**V2-p4-10 · MySQL dialect.** *(deps V2-p4-9)* The same operation set as a separate dialect:
backtick identifier escaping, `?` positional binds, no schema qualifier, and MySQL's own
option set. Also settles whether `kilasflow.sqlite` forks from the shared `databaseNode()`
factory — n8n has no SQLite node, so there is no parity target.

**V2-p4-11 · Options collection and query batching.** *(deps V2-p4-9)* The ten Postgres
options with n8n's exact names and defaults, and the three batching modes: `single`
(concatenated, one round trip), `independently` (per item, continue past errors),
`transaction` (per item in one tx, roll back and stop on first error). `largeNumbersOutput`
and `replaceEmptyStrings` both change row payload shape, so both need item-level tests.

**V2-p4-12 · n8n SQL import/export fidelity.** *(deps V2-p4-9, V2-p4-10, V2-p4-11)* Bump the
export type versions the interop table pins at 2.4 to Postgres **2.7** and MySQL **2.5**,
split the shared converters into per-operation ones, translate `options.queryReplacement`
into the bound `parameters` array instead of reporting it unsupported, and add the missing
`kilasflow.sqlite` mapping or document it as export-lossy. Cross-reference V2-p1-12, whose
import diagnostics for the SQL path this ticket partly retires, so its author does not
over-invest there.

**V2-p4-13 · `assignmentCollection` for Set v3.** *(deps V2-p2-2)* n8n's Set node carries an
assignments array that the data-shaping parity ticket needs and that V2-p2-2 defers to
nobody. Currently orphaned in both plans.

---

## p5 — AI parity, native Go

**V2-p5-1 · Cluster-node model.** Adopt n8n's root/sub-node shape on top of p2-8's
connection kinds, with n8n's slot rules: `maxConnections: 1` on `ai_languageModel`,
`ai_memory` and `ai_outputParser`, uncapped `ai_tool`. Note the direction trap — in n8n JSON
the *sub-node* is the source key and the agent is the target.

**V2-p5-2 · AI Agent parity.** Tools-Agent only (n8n deprecated the agent-type selector in
1.82.0 and removes v1 in 3.0, but imported workflows still carry `parameters.agent`).
Parameters: `systemMessage`, `maxIterations` (10), `returnIntermediateSteps`,
`passthroughBinaryImages`, `enableStreaming`, batching. Tool names come from the canvas node
name, and duplicate tool names must fail loudly.

**V2-p5-3 · Basic LLM Chain.** Model plus optional output parser, a `messages.messageValues`
fixedCollection of System/AI/Human templates — and **no memory slot**, matching n8n.

**V2-p5-4 · Chat model nodes.** OpenAI and OpenRouter as distinct node types so imports map
1:1, over the existing `ai.OpenAICompatible` adapter (OpenRouter is a base-URL change).
Shared option names (`temperature`, `maxTokens`, `topP`, penalties, `timeout`,
`maxRetries`), model lists via p2-4. Fix three live bugs while here: streaming defaults on
but `stream_options.include_usage` is never sent so token usage reports zero;
`temperature: 0` is unrepresentable because 0 means unset; and the model call is capped at
the 30s outbound timeout with no node-level override. Decide the outbound policy for model
endpoints and for self-hosted Ollama explicitly.

**V2-p5-5 · Memory.** Buffer window with n8n's session semantics (`fromInput` | `customKey`,
and the v1.4 auto-scoping suffix `__<node name>`). Today `maxMessages`/`maxAgeMinutes` are
collected into the descriptor and never read, and memory is one process-wide store shared
across tenants — fix both. Postgres chat memory lands in p6 as a tier unlock.

**V2-p5-6 · Tools.** HTTP Request Tool, Workflow Tool, and `usableAsTool` for ordinary
nodes, plus `$fromAI`.

**V2-p5-7 · Structured output parser**, using n8n's synthetic
`format_final_json_response` tool approach so imported workflows behave the same.

**V2-p5-8 · Import mapping for `@n8n/n8n-nodes-langchain.*`** onto the native nodes, with a
diagnostic for every option that has no equivalent.

**V2-p5-9 · Spike: Microsoft Agent Framework for Go.** `internal/ai/maf/` is a doc.go stub
whose comment ("publishes no tagged releases") is now stale — `agent-framework-go` reached
v0.1.0 on 2026-09-01 and ships tool calling, MCP, approvals, multi-agent routing and
OpenTelemetry. Evaluate it as an optional `ai.AgentRuntime` behind the existing boundary
with explicit pass criteria: it must accept KilasFlow's injected `http.Client` (or SSRF
policy and credential domain scoping are lost), expose streaming deltas, and report token
usage. If it fails any criterion it stays out and `LoopRuntime` remains the default. Update
the stale doc.go either way.

---

## p6 — PostgreSQL capability tier

**V2-p6-1 · Versioned migrations** replacing AutoMigrate, as PRD §54 already requires —
prerequisite for every schema change below, and non-negotiable before anyone shares a
customer database.

**V2-p6-2 · `kflow_` table prefix.** `NamingStrategy.TablePrefix` is **silently ignored**
today: all seven models implement `TableName() string`, which GORM resolves before the
namer. Convert to `TablerWithNamer`, and rename the literal index names in the struct
tags — index names are schema-global in Postgres, so `idx_executions_tenant_started` will
collide with a host app's. **Amendment required for p9:** cap `table_prefix` length, and add
an acceptance criterion computing the worst-case identifier budget at the maximum configured
prefix, including every generated index and constraint name.

**V2-p6-3 · Make the Postgres tier real.** `database.max_open_conns` defaults to **1** for
Postgres too, while `execution.max_concurrent` defaults to 10 — every "Postgres unlocks
concurrency" claim is a no-op until this is fixed. Add the missing index on
`executions.status` (polled 10×/100ms by `ClaimNext`), and add execution retention/pruning,
which does not exist at all.

**V2-p6-4 · Queue semantics.** `GORMExecutionStore.ClaimNext` is already a correct
lease-based durable queue, so do not adopt a queue library — River has no table-prefix
support and would install 7+ unprefixable tables alongside a second source of truth. Add
`FOR UPDATE SKIP LOCKED` (SQLite silently drops the clause) and Postgres `LISTEN/NOTIFY`
wake. NOTIFY payloads cap at ~8000 bytes, so carry IDs only; and the scheduler's advisory
lock must be keyed by prefix, since advisory locks are per-database, not per-schema.

**V2-p6-5 · Shared-database safety.** The internal-DB guard only covers SQLite files — for
any other driver `main.go` passes an empty guard, so a `kilasflow.postgres` node pointed at
the shared database can read `credentials`, `workflows` and every execution payload. SQL
nodes also do no host validation whatsoever (`safehttp` governs HTTP only). Add a guard and
a host allowlist, and document the dedicated-schema-plus-role deployment as the only
configuration that truly isolates. **Amendment required for p9:** name datastore tables
explicitly in the guard's scope, and record the user-visible consequence — a SQL node can
never read a datastore; the Datastore node is the only path.

**V2-p6-6 · pgvector.** Vector store and embeddings nodes as a Postgres unlock — and do
better than n8n, whose `n8n_vectors` table has an unbounded `vector` column and no
HNSW/IVFFlat index. **Amendment required for p9:** its acceptance criterion says tables are
"created by a migration, not at run time", which p9's runtime-DDL decision contradicts for
datastores. Record the carve-out here and in V2-p6-1's rationale, naming exactly which tables
may be created at run time and which may not — otherwise two tickets in adjacent phases hold
opposite rules for the same database and whichever lands second silently wins.

**V2-p6-7 · Close the SQL node's expression-injection and statement-guard defects.** *(new)*
Three shipped security defects. First, `nodes/database.go:58` tells the user "never build SQL
from an expression" while `nodes/database.go:204` runs `expression.Resolve` over the entire
parameter map, which includes `statement`, `executeStatement` and `statements` — so an
expression-built statement is interpolated before it reaches `QueryContext`. Either reject the
expression marker on those keys in `validateDatabaseConfiguration`, or delete the copy and
document the exposure; shipping the copy and not the control is the one option that is wrong.
Second, `ATTACH DATABASE` defeats the SQLite path guard entirely — nothing parses or allowlists
a statement, so any valid SQLite credential can attach KilasFlow's own database and read the
credentials table. Third, the guard silently no-ops on a `file:`-prefixed operator DSN: 
`cmd/kilasflow/main.go:206` passes `cfg.DSN` raw, `internal/database/database.go:140-143`
explicitly supports that form, and `filepath.Abs("file:./data/kilasflow.db")` can never match
the real file — while `sqlitePath` *rejects* the same `file:` form on a credential path, so
the asymmetry is visible in one file.

**V2-p6-8 · Network policy and credential domain scoping for database targets.** *(new)*
`safehttp` governs HTTP only. `postgresDSN`/`mysqlDSN` take no guard and do no host check, so
a tenant creating a credential pointed at `127.0.0.1:5432/kilasflow` reads every credential
blob, workflow and execution in the installation. `Credential.AllowedDomains` is enforced in
exactly one place — `nodes/http.go:322` — and `nodes/database.go` never reads it, so domain
scoping operators reasonably believe applies to database credentials does not. Reuse
`safehttp.Policy.CheckAddress` at dial time.

---

## p7 — Workflow history

**V2-p7-1 · Versioned history.** Copy n8n 2.x's shape: a history table of snapshots, a
published-version pin, and a publish audit trail. n8n caps free retention at 24h by licence;
KilasFlow's retention is a config knob, which is a genuine differentiator. Note n8n's own
gap worth closing: its history stores only nodes and connections, so restoring does not
restore settings.

**V2-p7-2 · Editor surface.** Version list, diff, restore and publish, plus an explicit
decision on whether the embed surface and host SDK gain publish events.

---

## p8 — Platform and long tail

**V2-p8-1 · Authentication and tenancy on the main API.** There is none today — only embed
tokens exist, and every request resolves to tenant `default` regardless. This is the single
largest gap between the product and its stated ICP. **p9 promotes this**: the Datastore
management API is host-driven by decision, and a host-driven API over a single shared
`default` tenant is a cross-customer data pool. Raise its priority accordingly.

**V2-p8-2 · JS sidecar for programmatic community nodes** (the deferred decision).
Node 24 LTS, not Bun: `n8n-workflow@2.x` depends on `isolated-vm`, a V8 C++ addon that
cannot load on Bun's JavaScriptCore, and `bun build --compile` statically links LGPL-2
JavaScriptCore into a proprietary binary. NDJSON over a Unix socket, never stdout — WAHA
pulls in pino and the first banner corrupts the stream. One tenant per process, since
decrypted credentials live inside third-party JS. Revisit the clean-room question before any
code: this ticket stays blocked until p1–p4 land.

**V2-p8-3 · MCP Client tool node** via `modelcontextprotocol/go-sdk`.

**V2-p8-4 · Human approval / Wait node** with durable resume.

**V2-p8-5 · Native community module SDK.** WASM packs on wazero directly (not Extism, not
`buildmode=plugin`, which needs cgo and kills `CGO_ENABLED=0`). Three prerequisites first:
the Code node cannot compile in the shipped distroless image (no Go toolchain), a new wazero
runtime is built per call with the compilation cache unused, and the guest has zero host
functions so HTTP, credentials and binary data are all impossible.

**V2-p8-6 · Secrets manager integration.**

**V2-p8-7 · Queue/worker mode** across processes, depending on p6.

---

## p9 — Datastore

KilasFlow's answer to n8n Data Tables, renamed **Datastore** by the owner. Nothing
equivalent exists — a repo-wide search for `datastore|data store|data table` across Go,
TypeScript, Svelte, Markdown and the whole `.pine/` corpus returns **zero hits**.

### What n8n actually built

n8n stores each data table as a **real physical table**, `${tablePrefix}data_table_user_${id}`
(`packages/cli/src/modules/data-table/utils/sql-utils.ts:386`), created by dynamic DDL, with a
two-table catalogue (`data_table`, `data_table_column` carrying an explicit integer `index` to
preserve column order). System columns are `id`, `createdAt`, `updatedAt`, plus the reserved
`dryRunState`. Column types are string, number, boolean, date. It is **not** an Enterprise
feature: the module directory carries no `.ee` suffix while its siblings `external-secrets.ee`
and `source-control.ee` do, and `license.ts` has no data-table entry. It shipped beta in
v1.113.1 (23 Sep 2025), capped at 200 MiB instance-wide with a warn at 80%.

The node is `n8n-nodes-base.dataTable`, `version: [1, 1.1]`, `usableAsTool: true` — so the
`dataTableTool` type is **derived**, not a second node file. Two resources: **Table** (Create /
List / Update / Delete) and **Row** (Insert / Get / Update / Upsert / Delete / If Row Exists /
If Row Does Not Exist). Table selection is a resource locator with From list / By Name / By ID.
Filtering is **Must Match** (Any / All) plus repeatable **Conditions** of Column / Condition /
Value. Writes use **Mapping Column Mode**: Map Each Column Manually or Map Automatically.

### The storage decision — one physical table per datastore

**Owner's decision: physical tables, matching n8n.** A datastore becomes a real table under the
`kflow_` prefix, created by runtime DDL, with a two-table catalogue. Byte quotas are explicitly
**not** a requirement, which removes the objection that would otherwise have been decisive.

What this buys is real and is why it was chosen: native column types with database-enforced
constraints, a query planner that sees real columns so filtering and sorting are naturally
indexable, `DROP TABLE` for deleting a large datastore, and — the one that matters most for an
embeddable product — a host application can read a datastore with ordinary SQL, which is
exactly the shared-customer-PostgreSQL posture this roadmap already commits to.

Three costs survive the quota decision. None is fatal; each becomes an acceptance criterion
rather than a footnote, because all three fail **silently** in production if left implicit.

1. **PostgreSQL truncates over-length identifiers without erroring.** `workflow.NewID` yields a
   prefix plus a UUIDv7 string, so `kflow_` (6) + `datastore_user_` (15) + 39 = 60 bytes of the
   63-byte limit, leaving three for an index suffix. Two indexed columns on one datastore would
   collapse to the same index name, `CREATE INDEX IF NOT EXISTS` would skip the second without
   complaint, and the code would believe an index exists that does not. **The fix is to never
   put the public datastore id in a physical identifier**: derive the table suffix from a short
   opaque surrogate (a per-tenant sequence, or 16 hex characters of a hash), never let
   PostgreSQL auto-name an index, and test that every identifier the DDL path can emit stays
   inside 63 bytes and stays pairwise unique after truncation. `kflow_ds_<16 hex>` is 25 bytes
   and leaves the budget wide open.
2. **SQLite cannot drop an indexed column.** `DROP COLUMN` fails when the column is indexed,
   unique, part of a primary key, named in a CHECK constraint, or referenced by a generated
   column, trigger or view; the only escape is the twelve-step table rebuild. On a driver pinned
   to `SetMaxOpenConns(1)` for the whole process (`internal/database/database.go:57-63`), that
   rebuild blocks the API, the scheduler, the webhook server and every running workflow for its
   duration. SQLite 3.41.2 also has no `ALTER COLUMN` at all. **The recommended answer is that a
   column delete is a catalogue operation, not immediate DDL**: mark the column removed so it
   disappears from the API, the editor and the node, and reclaim the physical column during an
   explicit maintenance action rather than inside a user's click. That keeps the common path
   O(1) on both drivers and confines the rebuild to a moment an operator chose.
3. **A second migration engine is now required, and it must be visible.** V2-p6-1 delivers goose
   over numbered `.sql` files — a static, dialect-split set. That cannot express "for each of N
   tenant-created tables discovered at runtime, add a column". The first time the datastore row
   shape must evolve, KilasFlow needs a runner that iterates an unbounded table set idempotently
   and resumably, with a per-table schema version, and a defined state when it fails halfway.
   This is V2-p9-4 and it is not free.

Two consequences to record rather than discover. Quota is not gone so much as **tiered**: row,
column and datastore *counts* are cheap on both drivers and stay mandatory, while per-datastore
*bytes* are available on PostgreSQL through `pg_total_relation_size` and unavailable on the
pinned SQLite, whose driver omits `ENABLE_DBSTAT_VTAB` (verified in
`modernc.org/sqlite@v1.23.1`; present in v1.54.0). That asymmetry fits the epic's existing
"PostgreSQL unlocks extra features" posture exactly, so it is stated as a tier difference, not
as a gap. And runtime DDL against the internal database now needs explicit sanction, because
V2-p6-6 (`FEAT-k65hqv`) carries the opposite acceptance criterion — tables "created by a
migration, not at run time" — and V2-p6-1's rationale is that an operator must be able to stage
DDL before it runs. The epic's decisions table records the carve-out, and both tickets get an
amendment naming it, so two tickets in adjacent phases do not hold opposite rules for the same
database.

### Tickets

**V2-p9-1 · Storage engine: dialect-aware DDL and identifier safety.** *(deps V2-p6-1, V2-p6-2)*
A new `internal/datastore` package owning the catalogue models — `datastores`, and
`datastore_columns` with an explicit integer `index` preserving column order — and a DDL
service: create table with columns, drop table, add / rename / drop column, table exists.
Physical types match n8n so behaviour is portable: PostgreSQL `TEXT` / `DOUBLE PRECISION` /
`BOOLEAN` / `TIMESTAMPTZ(3)`, SQLite `TEXT` / `REAL` / `BOOLEAN` as 0/1 / `DATETIME(3)`, with
SQLite's date serialization and boolean normalisation on read. Identifier handling is the
security boundary and the correctness boundary at once, so it gets its own tests: the physical
table name comes from a short opaque surrogate and never from the public datastore id, every
index is named deterministically rather than by PostgreSQL, column names match
`^[a-zA-Z][a-zA-Z0-9_]*$` within 63 bytes, `id` / `createdAt` / `updatedAt` / `dryRunState` are
reserved case-insensitively, and quoting doubles embedded quotes. One test enumerates every
identifier the DDL path can emit and asserts the 63-byte bound plus pairwise uniqueness after
truncation. Metadata write and DDL run in one transaction, metadata first.

**V2-p9-2 · Catalogue and row store.** *(deps V2-p9-1)* Row CRUD with n8n's filter shape —
`{type: and|or, filters: [{columnName, condition, value}]}` over `eq`, `neq`, `like`, `ilike`,
`gt`, `gte`, `lt`, `lte`, plus the `isEmpty` / `isNotEmpty` the n8n UI exposes. Operators map
through a Go `switch` to compile-time SQL fragments; an unrecognised operator is an error, never
a passthrough, or the operator slot becomes a predicate-bypass hole. Keyset pagination reuses
the executions cursor pattern, and the cursor helpers are **extracted into a shared helper
first** (`internal/repository/executions.go:33-36,335-353`), because a second copy is how two
cursor formats drift apart. Dry run on update, upsert and delete returns paired before/after
rows tagged `dryRunState`, resolving n8n's own asymmetry where `deleteRows` accepts `dryRun` but
declares no tagged return type.

**V2-p9-3 · Schema evolution.** *(deps V2-p9-1)* Add, rename and delete a column, and settle
retype — which n8n forbids outright, so supporting it is a genuine differentiator but adds a
data-migration path. The load-bearing decision is the one above: a column delete is a catalogue
operation that hides the column immediately, with physical reclamation deferred to an explicit
maintenance action, so a user's click never triggers SQLite's twelve-step rebuild against the
single shared connection. State what an operator sees and how they trigger reclamation.

**V2-p9-4 · Per-datastore-table migration runner.** *(deps V2-p6-1, V2-p9-1)* The second
migration engine that physical tables force. A per-table schema-version column, idempotent and
resumable iteration over an unbounded tenant-created table set, a defined state when it fails
halfway with datastores at mixed versions, and a bound on how long it may hold the single SQLite
connection. This ticket exists so the cost is scheduled rather than discovered.

**V2-p9-5 · Limits and retention.** *(deps V2-p9-2)* Counts are mandatory on both drivers and
need no measurement mechanism: maximum datastores per tenant, columns per datastore, rows per
datastore, and bytes per single value. Per-datastore byte accounting is a **PostgreSQL-tier
capability** via `pg_total_relation_size`, absent on SQLite because the pinned driver omits
`ENABLE_DBSTAT_VTAB` — state that as a tier difference in the same words the epic uses
elsewhere. Also an acceptance criterion bounding how long any Datastore operation may hold the
SQLite connection.

**V2-p9-6 · Concurrency semantics.** *(deps V2-p9-2)* `execution.max_concurrent` defaults to
10, so ten executions can race one row — and the most popular real use case for this feature is
a cross-run key-value store, which is a read-modify-write race. n8n documents **nothing** here.
Settle whether upsert is one atomic statement (`ON CONFLICT` / `INSERT OR REPLACE`) or a
check-then-write, whether filtered updates take row locks, and whether an optimistic-locking
precondition on `updatedAt` is offered. This is a place to beat n8n rather than match it.

**V2-p9-7 · Datastore management API.** *(deps V2-p9-2, V2-p8-1)* Three resource groups —
datastores, columns, rows — following the established Huma handler shape. Row delete
**requires** a filter, so a full-table wipe is never one forgotten parameter away. The
dependency on V2-p8-1 is deliberate and load-bearing: without it every host customer's rows
land in one shared `default` tenant, and V2-p9-12's cross-tenant test is unwritable through the
API because no second tenant exists on that surface.

**V2-p9-8 · Shared list-page shell and a table primitive.** *(no deps)* The loading skeleton,
error card, empty state and `message(error)` helper are already copy-pasted verbatim across
four route files, and the app contains exactly one `<table>`, no pagination component, no
inline-edit pattern, no delete confirmation and no Svelte component test setup. This lands
before the Datastore grid so the fifth copy is never written.

**V2-p9-9 · Datastore editor surface.** *(deps V2-p9-7, V2-p9-8)* List, column editor, and
row grid with cursor paging and per-column filtering, registered in **both** hard-coded nav
places. Decide explicitly whether slice one has inline cell editing or is read-only with a
modal editor — read-only plus modal is far cheaper and matches every existing pattern in the
app.

**V2-p9-10 · Datastore node.** *(deps V2-p9-2, V2-p2-10, V2-p1-5, V2-p1-2)* The Table and Row
resources at n8n's operation surface. Column names are literal-only, re-validated in the
executor after `expression.Resolve`, because `internal/webhook/webhook.go:249` puts
attacker-controlled JSON into `$json` on every inbound request — so an expression-capable column
slot is identifier injection sourced from a webhook body, needing no workflow-edit rights. The
deps on V2-p1-5 and V2-p1-2 are what make a partial multi-item write recoverable: without
honoured `continueOnFail`/`retryOnFail` and paired-item lineage, a ten-row write failing on the
seventh aborts the execution with seven rows committed and no link to the offending item.

**V2-p9-11 · Datastore agent tool.** *(deps V2-p9-10, V2-p5-6)* A **closed**
`ai.ToolDefinition` schema — column names enumerated from the stored schema, operators
enumerated, model input restricted to value leaves — bound to a specific datastore at
construction, never accepting a datastore identifier from the model. The datastore's own row
contents are the likeliest prompt-injection source, and unlike the HTTP tool there is no
`safehttp` backstop to catch a bad value.

**V2-p9-12 · Isolation, redaction carve-out and trace policy.** *(deps V2-p9-2, V2-p1-6,
V2-p6-5, V2-p6-7)* Three things meet here. Redaction rewrites by key match — `token`, `secret`,
`session`, `pin`, `otp`, `signature` among others, with `normalizeKey` stripping separators and
a leading `x` — at `internal/repository/executions.go:597-605`, on the live stream at
`internal/events/events.go:139`, **and at webhook ingest**, where `webhook.go:249` returns
`Redact(payload)` as the item that *enters* the workflow, so `{"pin":"482913"}` reaches the
first node already destroyed. `looksLikeCredential` additionally rewrites any string beginning
`basic `, so a cell reading "Basic understanding of Postgres" is stored as `[redacted]`. Second,
writing a datastore row copies its contents into the execution trace, which nothing prunes — so
a tenant's datastore delete does not delete the data; record row counts and identifiers in
node-run payloads, not row contents. Third, a cross-tenant read must fail, with a test that
proves it — and because a physical table carries no `tenant_id` column, tenant purge and
isolation must be expressed through the catalogue, which this ticket settles.

**V2-p9-13 · n8n Data Table import and export.** *(deps V2-p9-10)* Map
`n8n-nodes-base.dataTable` onto `kilasflow.datastore`, including what happens when an imported
workflow references a data table id with no KilasFlow counterpart, and decide the
`dataTableTool` variant's treatment.

**V2-p9-14 · CSV import and export.** *(deps V2-p9-7, V2-p9-9)* Create-from-CSV and download,
with an explicit include/exclude toggle for system columns — which n8n added only after launch.

**V2-p9-15 · Embed scope, host SDK and documentation.** *(deps V2-p9-7, V2-p9-14, V2-p8-1)*
**The last ticket, by the owner's instruction.** `permits()` at
`internal/api/middleware/embed.go:147-151` default-denies anything it does not recognise;
`embed.Session` carries one mandatory `WorkflowID`; and `normalizeScopes` rejects any scope
outside `workflow:read|write|run`, so a datastore-scoped token cannot be minted today. Scope:
datastore scopes in the embed vocabulary, a `permits` entry, hand-written `KilasFlowClient`
methods — orval regenerates types automatically but the client's seventeen methods are all
hand-typed — and the technical documentation.

### Housekeeping this phase forces

**V2-p9-0 · Move the roadmap into the repository.** Every one of the 62 V2 tickets opens its
References with `Roadmap plan, pN section, entry V2-pN-M:` followed by this file's path — a
path outside the repository, outside version control, and rewritten by any plan-mode session.
It was in fact overwritten during this planning session and had to be reconstructed. Move it to
a tracked path such as `.pine/roadmap.md` and repoint all 62 first-reference bullets, so the
tickets and the roadmap are committed together.

Two smaller corrections belong to whichever ticket opens the file first: the PostgreSQL
integration-test cleanup at `internal/database/database_test.go:146-150` drops only
`models[0..3]`, leaking `credentials`, `webhook_bindings` and `schedules`, and its CASCADE drop
of `workflows` silently strips the schedules foreign key so a re-run migrates onto a subtly
wrong schema — which is precisely the integration run a Datastore ticket needs to trust. And no
CI configuration exists anywhere (no `.github`, no `.gitlab-ci.yml`), so no p9 ticket may claim
a check "runs in CI"; several existing tickets already assert this untruthfully.

---

## What executing this plan creates

The p0–p8 sections above are already tracked as 62 tickets under `EPIC-m42s3g` and are
reproduced here as the roadmap of record. **Only the entries marked new below get written.**

**28 new tickets**, all `parent: EPIC-m42s3g`:

| Phase | Tickets | Labels | Priority |
|---|---|---|---|
| p2 | V2-p2-10, V2-p2-11 (2) | `registry`, `metadata`, `editor` | high |
| p4 | V2-p4-6 … V2-p4-13 (8) | `nodes`, `parity`, `sql` | high |
| p6 | V2-p6-7, V2-p6-8 (2) | `persistence`, `security`, `tier` | high |
| p9 | V2-p9-0 … V2-p9-15 (16) | `datastore`, `storage`, `api` | medium |

Labels and priority are uniform per phase, matching how every existing phase is tagged — p6 is
`medium`/`persistence`+`postgres`+`tier`, p5 is `high`/`ai`+`parity`. The two p6 additions carry
`high` rather than the phase's `medium` because they are shipped security defects, not tier
work.

**Five amendments to committed tickets**, each a small edit to an existing body:

- `FEAT-5s1w0t` (V2-p2-2) — name which ticket picks up each deferred kind. Today
  `resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` are deferred to
  nobody: a grep of the whole corpus finds those identifiers in that one paragraph and nowhere
  else.
- `FEAT-whn5vb` (V2-p2-4) — add an internal-source loader kind, and bound an embed session's
  returned option values by its `WorkflowID`.
- `FEAT-r6xhnp` (V2-p6-2) — cap `table_prefix` length and add the identifier-budget criterion
  covering runtime-generated datastore table and index names.
- `FEAT-a94c8y` (V2-p6-5) — name datastore tables in the guard's scope, and record that a SQL
  node can never read a datastore.
- `FEAT-k65hqv` (V2-p6-6) — record the runtime-DDL carve-out so it does not contradict p9.

**Three edits to `EPIC-m42s3g`**: two new rows in `## Decisions that shape every child ticket`
(Datastore storage model; database-node scope), `p9` added to `## Phase order` with a sentence
saying phase numbers express dependency depth rather than a serial queue, and the Datastore
proof added to `## Acceptance scenario`.

**One housekeeping ticket, V2-p9-0**, which should be written first: move this roadmap into the
repository and repoint all 62 first-reference bullets. It is currently at
`~/.claude/plans/distributed-worker-nats-crispy-finch.md` — outside the repo, outside version
control, and rewritten by any plan-mode session. It was in fact overwritten during this
planning session and had to be reconstructed by hand.

## Verification

Executing this plan is verified by inspection, not tests:

1. `pine doctor` passes and `git status` shows changes confined to `.pine/`.
2. `pine list --phase p9` returns 16 tickets, all `parent: EPIC-m42s3g`; p2 has 11, p4 has 13,
   p6 has 8.
3. `pine ready` still lists only p0 and p1 tickets — every new ticket is blocked, because
   nothing in p2, p4, p6 or p9 can start before the p1 work it depends on.
4. Every new ticket body has exactly `## Scope`, `## Acceptance criteria`, `## Implementation
   Plan` and `## References`, in that order, with 6–8 acceptance criteria written as full
   sentences asserting an observable outcome. Match the corpus, not `.pine/templates/feature.md`,
   whose H1 headings contradict every V2 ticket.
5. No ticket claims any check "runs in CI". There is no `.github` directory and no CI
   configuration of any kind; several existing tickets already assert this untruthfully, and the
   new ones must not add sixteen more.
6. Every `file.go:NNN` citation is re-verified at write time rather than copied from this plan —
   the corpus convention is to correct the roadmap in place when it is wrong
   (`FEAT-r6xhnp.md:22`: "The roadmap said eleven; there are thirteen.").

The epic's own acceptance test, for later: a Telegram bot answering through an AI Agent
after p3, then the official WAHA chatting template imported twice for two different tenants,
both activated, a real webhook delivered to each, and two correct WhatsApp replies with
execution evidence — with no Node.js process anywhere.

## Open items for the owner

- **OpenRouter API key** for p5 testing: put it in `.env` (already gitignored).
- **Telegram bot token** from BotFather for p3-6/p3-7, in the same `.env`.
- **Column type change**: n8n forbids it outright. Under the rows-plus-JSON design it becomes
  cheap — a catalogue write plus a lazy coercion pass — so it is a genuine differentiator
  rather than the migration nightmare it is under physical tables. Currently planned as
  supported, unlike n8n.
- Four defects were found in the owner's own `n8n-nodes-mitrachat` package while reading it
  as reference — most seriously, `credentials.signingSecret` is read by two nodes but never
  declared, so `MitraChatWebhookTrigger` throws a 500 on every request. Out of scope here, but
  worth its own ticket in that repo.
