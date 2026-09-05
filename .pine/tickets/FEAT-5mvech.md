---
id: FEAT-5mvech
title: Write the architecture and concepts documentation
status: done
priority: high
labels:
    - docs
deps:
    - FEAT-nxxbs5
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:54:02Z"
updated: "2026-09-06T00:00:00Z"
---

## Scope

The best explanatory writing in this repository is in Go package doc comments, and none of it is readable by anyone who has not cloned the repository and opened the right file. `internal/routing/doc.go` explains why routing metadata is data rather than code. `internal/nodepack/nodepack.go` opens by explaining why a pack is committed JSON and not generated Go and not an OpenAPI document parsed at boot. `internal/embed/embed.go` documents the token format and why the signature is verified before the payload is parsed. `internal/api/docs.go`, `internal/webhook/doc.go`, `internal/web/embed.go` and `internal/database/database.go` each carry the same quality of reasoning.

A reader deciding whether to build on KilasFlow needs that reasoning and cannot get it. The `README.md` "Layout" section names each package and its milestone in one line apiece; the PRD describes an intended design under a former project name; neither explains how the running system actually behaves.

The concepts that have to be written down, because a host integrator hits every one of them:

- **The execution model** — how a workflow becomes an IR, how the runner claims and runs it, `execution.max_concurrent`, the lease-based durable queue in `GORMExecutionStore.ClaimNext`, and the nine SSE event types that report it.
- **Items and lineage** — the item contract, binary references, and pairedItem provenance, which V2-p1-2 introduces and which 32% of real n8n expressions depend on.
- **Expressions** — the two dialects that must stay explicit: n8n marks an expression with a leading `=` on a plain string, KilasFlow uses `{"mode":"expression","value":…}`. Plus the allowlisted roots, and `$env` being deliberately narrowed to `KILASFLOW_WORKFLOW_ENV_*` so a workflow can never read the DSN or the master key.
- **The node registry** — `Definition`, the closed `PropertyKind` set, `displayOptions` visibility, downward version resolution (`Resolve` picks the highest registered version at or below the requested one, which is what lets an imported n8n `typeVersion` of 3.4 run against a v1 node), and the three sources `builtin` / `pack` / `sidecar` with their namespacing rules.
- **Credentials** — sealed payloads against public fields, `Split`, `AllowedDomains`, and the declarative authentication placement.
- **Tenancy and the embed boundary** — `TenantScope`, the `kfe1.` token, scopes and their implication rule, exact-origin matching, and `permits()` default-denying.
- **Webhooks** — the opaque per-tenant route segment, why an unknown, inactive and wrong-method request all return an identical 404, and the body and timeout bounds.
- **Safety boundaries** — `internal/safehttp`'s dial-time and per-redirect address checks, the internal-database guard, and the WASM Code node's zero-capability guest.

This is a documentation ticket. It writes down what the system does today; it does not change behaviour, and where a doc comment is stale it says so on this ticket rather than editing code.

## Acceptance criteria

- [x] A reader who has never opened this repository can explain, after the Concepts section, how a workflow gets from a trigger to a node run to a recorded execution.
- [x] The two expression dialects are documented explicitly, including which one appears in an imported n8n workflow and which one KilasFlow stores.
- [x] Node versioning is documented with its downward-resolution rule and a worked example of an imported `typeVersion` that no registered version matches exactly.
- [x] The three registry sources are documented with their namespacing rules, including that only built-in registration may claim the `kilasflow.` prefix.
- [x] The embed boundary is documented as a security boundary: token format, scope implication, exact-origin matching, the fifteen-minute default and thirty-minute cap, and the fact that `permits()` denies anything it does not recognise.
- [x] The safety boundaries are documented together in one place, so an operator can enumerate what a workflow can and cannot reach without reading Go.
- [x] Every concept page links to the API reference operations and to the source package it describes, so the documentation is a route into the code rather than a replacement for it.
- [x] Any doc comment found to be stale while writing is listed on this ticket with its file and the correction, for a follow-up rather than a silent edit.

## Implementation Plan

Write from the source, not from the PRD. `gflow-prd-v1.md` describes an intended system under a former name and has demonstrably drifted — its recommended Dockerfile names different base images than the one that ships. Treat it as historical context and the packages as the authority.

Order the pages by what a reader needs first, which is not the order the system is built in. Execution model, then items and expressions, then the node registry, then credentials, then tenancy and embedding, then safety. A reader arrives wanting to know what happens when a workflow runs; the registry is only interesting once they know why it matters.

Use diagrams sparingly and only where the prose genuinely fails — the execution lifecycle and the embed handshake are the two that earn one. Starlight renders Mermaid, so a diagram is text in the repository and diffable, which matters for something that must stay true as p1 through p9 change the engine underneath it.

The largest risk to this ticket is that it documents a moving target. p1 changes branch pruning, pairedItem lineage, trigger roots, loops, error handling and the expression engine; p2 changes the registry's property kinds and port descriptors. Two mitigations, and the ticket should adopt both. Write each page against what ships today and date it. And for a concept a p1 or p2 ticket is about to change, say what is true now and link the ticket that changes it, rather than either omitting it or describing an unbuilt future as present tense.

One thing to resist: do not write a page per Go package. The package layout is an implementation fact and a reader does not have it. Write a page per concept, and let a concept draw on several packages — the safety boundaries page pulls from `internal/safehttp`, `internal/sqlnode`, `internal/runcode` and `cmd/kilasflow/main.go`, and it is one idea.

## References

- Roadmap plan, p10 section, entry V2-p10-11: `.pine/roadmap.md`.
- `internal/routing/doc.go` — why routing metadata is data, and what the interpreter refuses.
- `internal/nodepack/nodepack.go` — the opening comment on why a pack is committed JSON.
- `internal/embed/embed.go` and `internal/embed/doc.go` — the `kfe1.` format, `Session`, `Scope`, `Allows`, lifetimes and origin matching.
- `internal/api/middleware/embed.go` — `permits()` and its default-deny arm.
- `internal/node/registry.go` — `Definition`, `Source`, `BuiltinPrefix`, `validateDefinition` and `Resolve`.
- `internal/property/property.go` — the closed `PropertyKind` set and `TypeOptions`.
- `internal/expression/doc.go` — the expression grammar and roots.
- `internal/engine/runner.go` — `Executor`, `Request`, and the run loop.
- `internal/repository/executions.go` — `ClaimNext` and the keyset cursor.
- `internal/webhook/webhook.go`, `internal/repository/webhooks.go` — `mintWebhookRoute` and the uniform 404.
- `internal/safehttp/safehttp.go`, `internal/sqlnode/sqlnode.go`, `internal/runcode/` — the safety boundaries.
- `cmd/kilasflow/main.go` — `workflowEnvironment`, `databaseGuard`, `outboundPolicy`, and the composition order the registry depends on.

## Work evidence

Ten pages written, all under `docs/src/content/docs/`. `make docs-build` passes
and `starlight-links-validator` reports **"All internal links are valid."**
26 pages built.

### Pages

| Page | Lines | Covers |
| --- | --- | --- |
| `concepts/architecture.md` (rewritten from the stub) | 168 | one process, the layer diagram and the engine's import rules, boot-time composition and its checks, persistence, why the document format is ours |
| `concepts/execution-model.md` | 322 | versions and pinning, the two validation stages and all ten compiler error codes, the durable queue, `ClaimNext` and the lease, the run loop, branch pruning, loops, retries and tolerance, what is recorded, keyset pagination, the event stream, sub-workflows |
| `concepts/items-and-lineage.md` | 155 | the item contract, `NodeInput`/`NodeOutput` asymmetry, `BinaryRef` and its bounds, `PairedItem`, the positional-inference rule, `Lost` |
| `concepts/expressions.md` | 203 | both dialects side by side, the closed grammar, the roots, the three routing-only roots, undefined semantics, `$env` narrowing, the functions |
| `concepts/node-registry.md` | 307 | `Definition` split three ways, computed ports, `TypeVersion` as fixed-point, `Get`/`Resolve`/`Lookup`, a worked downward-resolution example, the fifteen property kinds, `displayOptions`, the three sources and `BuiltinPrefix`, what a pack is, boot checks |
| `concepts/credentials.md` | 219 | `Split`, AES-256-GCM and the key's provenance, `AllowedDomains` and its four check sites, the five placements, declarative tests, the ten built-in types |
| `concepts/tenancy-and-embedding.md` | 273 | `TenantScope`, the resolver's precedence and its two fallbacks, opt-in auth and its three credential forms, the `kfe1.` token, scopes and implication, lifetimes, exact-origin matching, the handshake, the `permits()` table and its default-deny arm |
| `concepts/webhooks.md` | 199 | route minting and permanence, the uniform 404 and why it is structural, bounds, the four response modes, inbound auth, item shapes, `WebhookDeclaration`, no test URL |
| `concepts/safety-boundaries.md` | 269 | the three `safehttp` checks, the refused ranges, the two independent levers, database separation and the SQLite path guard, the wazero sandbox, expressions, payloads, inbound bounds, and an explicit "what is not defended" list |
| `reference/expression-grammar.md` | 183 | every root and all nineteen functions with arity and receiver type |

No file outside `docs/src/content/docs/concepts/`,
`docs/src/content/docs/reference/expression-grammar.md` and this ticket was
touched. `docs/astro.config.mjs` was not modified — both sections autogenerate
from their directories, and `sidebar.order` in each page's frontmatter gives the
reading order the plan asked for.

### Build output

```
 generating static routes
   ├─ /concepts/architecture/index.html
   ├─ /concepts/credentials/index.html
   ├─ /concepts/execution-model/index.html
   ├─ /concepts/expressions/index.html
   ├─ /concepts/items-and-lineage/index.html
   ├─ /concepts/node-registry/index.html
   ├─ /concepts/safety-boundaries/index.html
   ├─ /concepts/tenancy-and-embedding/index.html
   ├─ /concepts/webhooks/index.html
   ├─ /reference/expression-grammar/index.html
   …
 validating links

╭─                             ─╮
· All internal links are valid. ·
╰─                             ─╯

[build] 26 page(s) built in 2.42s
[build] Complete!
```

### Counts verified against the source

Every headline number was counted rather than carried over.

- **39 distinct node types / 46 `(type, version)` pairs.** 36 types and 41 pairs
  built in (37 entries in `nodes.RegisterAll`, of which `postgres` and `mysql`
  each appear at v1 and v2, plus four arities of `kilasflow.unsupported`); 3
  types and 5 pairs from packs (`pack.telegram` ×1, `pack.waha` ×2,
  `pack.wahaTrigger` ×2).
- **46 API operations in 10 tag groups.** `grep -c "OperationID:"` over
  `internal/api/handlers/*.go`.
- **15 property kinds**, **19 expression functions**, **10 credential types**,
  **5 authentication placements**, **13 connection channels**,
  **7 execution statuses**, **9 declared SSE event types**.

## Ticket premises that were false

Recorded because the plan section prescribed mitigations for risks that no
longer exist.

1. **"Starlight renders Mermaid."** It does not, in this project. `docs/` has no
   Mermaid dependency, plugin or configuration — a fenced `mermaid` block would
   render as a plain code block. The two diagrams the plan asked for (the
   execution lifecycle and the embed handshake) are drawn as ASCII inside fenced
   blocks instead, which keeps the diffable-text property the plan actually
   wanted. Adding Mermaid would have meant editing `docs/astro.config.mjs` and
   `docs/package.json`, both out of scope.

2. **"The largest risk is that it documents a moving target. p1 changes … p2
   changes …"** Every p1 and p2 ticket is `status: done`. Branch pruning
   (`FEAT-k3grr5`), paired-item lineage and run index (`FEAT-9knk67`), multiple
   trigger roots (`FEAT-fw0m2q`), bounded loops (`FEAT-sar60r`), error handling
   (`FEAT-a6yg3n`) and the expression engine (`FEAT-v8k1tc`) are all implemented
   and tested. There was nothing to describe as forthcoming and no ticket to
   link forward to, so the pages describe settled behaviour in the present tense
   and `concepts/architecture.md` says so with a date.

3. **"pairedItem provenance, which V2-p1-2 introduces."** It is already in
   `internal/workflow/document.go` and stamped by
   `internal/engine/runner.go:stampProvenance`.

4. **"the nine SSE event types that report it."** Nine names are *declared* in
   `internal/events/events.go` and all nine are bound to distinct Go types in
   the SSE registration, but only **six** are ever published:
   `node.started`, `node.output` and `workflow.saved` have no producer anywhere.
   The page says so rather than repeating the nine.

5. **"the opaque per-tenant route segment."** A route is minted per **trigger
   node**, keyed by `(tenant, workflow, node)` — not per tenant.

6. **The task brief** (not this ticket) referred to "the new
   `internal/sqlguard/`". No such package exists anywhere in the repository.
   The internal-database guard is `sqlnode.Guard` in
   `internal/sqlnode/sqlnode.go`, built by `databaseGuard` in
   `cmd/kilasflow/main.go`. This ticket's own References section is correct.

## Stale doc comments found (not edited, per the ticket's own rule)

1. **`nodes/doc.go`** — describes a layout that no longer exists:

   > Each node lives in its own subpackage … nodes/core/ manual, set, ifnode,
   > merge, code / nodes/http/ HTTP Request / nodes/webhook/ … / nodes/database/
   > … / nodes/ai/ …

   All five of those directories exist and are **empty**. Every built-in node is
   a file directly in package `nodes`. Correction: the comment should describe
   the flat layout, or the directories should be removed.

2. **`internal/api/middleware/embed.go:150`** — the comment names
   `handlers.RequireEmbedWorkflow`, which does not exist. The real per-execution
   ownership check is `(*Executions).ownsExecution` in
   `internal/api/handlers/executions.go`, plus an inline check in
   `StreamEvents`.

3. **`internal/embed/embed.go`, `Allows`** — the comment says "Write implies
   read: a session that can save must be able to load", but the code grants read
   for `ScopeWrite` **and** `ScopeRun`. The comment is narrower than the
   behaviour it explains.

4. **Duplicate package comments.** `internal/embed/doc.go` and
   `internal/embed/embed.go` both carry a `// Package embed` comment with
   different wording; so do `internal/runcode/doc.go` and
   `internal/runcode/runcode.go`. One of each should win.

5. **`internal/routing/doc.go` and `internal/nodepack/nodepack.go` both claim a
   check that does not exist.** `routing/doc.go`: "The pairing is checked at
   registration, so a definition bound to the routing executor with no routing
   description fails at startup rather than at run time." `nodepack.Register`'s
   comment: "A definition bound to the routing executor with no routing
   description registers cleanly and fails on its first run … Both are startup
   failures here instead." `Register` checks only that the named **executor** is
   installed; a nil description falls through `if description != nil` and skips
   routing registration silently. The pairing is guaranteed structurally —
   `Load` builds both together — not asserted. The failure would surface at run
   time in `internal/routing/executor.go` as "no routing description is
   registered for %s v%s". Correction: say the invariant is structural, or add
   the check the comments describe.

6. **`internal/expression/doc.go` and `internal/engine/runner.go` overstate
   `.item`.** The doc comment says "`$('Name').item` reads the paired-item
   lineage the runner tracks" and the root table glosses it as "the item of that
   node this one descends from". `nodeItemFor` does not walk from the current
   item at all: it requires every one of the named node's output items to carry
   non-nil, non-`Lost` provenance and then resolves `.item` only when that node
   produced exactly one item, otherwise returning "produced %d items; use
   .all(), .first() or .last() to choose one". The runner's own inline comment
   is honest about it ("no current-item context to choose between them"), but
   the package doc a reader meets first is not.

7. **`internal/credentials/registry.go:102-106`** — "It is assembled at
   composition and read-only afterwards, in the shape of the node registry". It
   is a package-level singleton built in a `var` initialiser (`defaultRegistry`,
   `:278`); nothing in `cmd/kilasflow/main.go` registers a credential type. The
   node registry genuinely is assembled at composition; this one is not.

8. **`cmd/kilasflow/main.go:115-118`** — "It reaches a customer's service through
   the same egress policy an HTTP node uses" — the line below it passes
   `safehttp.DefaultPolicy()`, so this is only true when `outbound.*` is
   unconfigured. Same class of problem as code finding 2 below, recorded here
   because the comment is the thing that misleads.

9. **Milestone annotations that read as status** — `internal/engine/doc.go`
   ("Milestone 1."), `internal/workflow/doc.go` ("Milestone 1."),
   `internal/webhook/doc.go` ("Milestone 2."), `internal/embed/doc.go`
   ("Milestone 6."), `nodes/doc.go` ("Milestone 1 onward."). This is
   `FEAT-sfy1tq`'s territory and is listed here only for completeness.

## Review pass

A second agent fact-checked all ten finished pages against the source and found
twelve errors and eight imprecisions. Every one was verified independently
against the code and then fixed before the commit. The substantive ones, kept
here because they are the claims a reader would most likely have relied on:

- `.item` was described as walking lineage from the current item. It does not;
  see stale doc comment 6 above. All three pages that said so were rewritten to
  state the actual rule and to say plainly that selecting among several items is
  not something `.item` can do yet.
- "Every repository operation takes a `TenantScope`… a query that forgets the
  tenant does not compile" was false. `ClaimNext`, `ClaimDue`, webhook
  `Resolve`, `ClaimDelivery`, `RecordDeliveryExecution`, `PruneAllVersions`,
  `EnsureTenant`, `GetTenant` and `CountUsers` all take none, several on the hot
  path.
- "A tenant is a string, not a record" was false: `migrations/*/000003_identity`
  creates a `tenants` table, and `users` and `api_keys` reference it
  `ON DELETE RESTRICT`.
- `POST /embed-sessions` was shown taking a `tenantId` in the body. It does not;
  the tenant comes from the authenticated caller, which is a materially
  different security story.
- Sub-workflow recursion was described as a stack "rather than a depth counter".
  Both exist — `MaxWorkflowCallDepth` is 16, deliberately beside the cycle check.
- A `Wait` node was described as bounded at one hour. On a stock install
  `execution.default_timeout` (60 s) ends the run long first.
- `ClaimNext` was described as selecting and updating "in the same statement".
  It is a select followed by a compare-and-set update inside one transaction.
- "Secrets are recoverable by anyone who can read the API" was wrong about the
  privilege required: reads are redacted, and exfiltration needs *write* access
  to author a workflow. The page now says that instead.
- The embed signing key was described as "at least 32 bytes"; the only path that
  reaches the issuer accepts exactly 32 and rejects anything else at boot.

## Code findings for follow-up (no code changed)

1. **`embed.session_ttl` is dead configuration.** `config.Embed.SessionTTL`
   (default 15m) is declared and defaulted but never read outside tests: only
   `SigningKeyEnv` and `AllowedOrigins` reach `embed.NewIssuer` in
   `cmd/kilasflow/main.go`. The effective default is `embed.DefaultLifetime`,
   which happens to be the same value — so the key silently does nothing.

2. **Two call sites bypass the configured outbound policy.**
   `cmd/kilasflow/main.go` builds the edit-time option-loading resolver and
   `webhook.NewCoordinator` from `safehttp.DefaultPolicy()` rather than from
   `outboundPolicy(cfg.Outbound)`. The address, redirect and size guards still
   apply at their defaults, but a configured `outbound.allowed_hosts`,
   `outbound.timeout` or `outbound.max_response_bytes` does not reach them —
   while the comment above the first one claims "the same egress policy an HTTP
   node uses". `concepts/safety-boundaries.md` documents this as a caution
   rather than hiding it.

3. **AI agent events are emitted as unnamed SSE frames.**
   `internal/engine/service.go` publishes nested node events with
   `Type: events.Type(event.Name)`, and `nodes/ai.go` passes one of eight
   `ai.*` kinds. Those are not in the SSE type map in
   `internal/api/handlers/executions.go`, so huma takes its unknown-type path:
   the frame is written with **no `event:` line** and a stack trace is logged to
   stderr for every one. A client listening for named events never sees them,
   and a busy agent run produces a lot of stderr noise.

## Stale documentation of record found elsewhere (not edited — other sections)

Three other sessions are writing in `docs/` concurrently, so none of these were
touched.

- `docs/src/content/docs/reference/api.md` and `docs/src/content/docs/index.mdx`
  both say **"Thirty-five operations … in eight groups"**. It is now **46
  operations in 10 groups** — `Auth` (7 operations) landed since.
- `docs/src/content/docs/start/what-kilasflow-is.md` says "There is no
  authentication on the API" and "nothing resolves a real tenant from a request,
  so at runtime there is exactly one tenant, named `default`". Both were true
  before `internal/auth/` landed. Authentication now exists and is enforced on
  `/api/v1` when `auth.enabled` is set; it defaults to false, which is what
  makes the *effect* still broadly true, but the mechanism description is wrong.
  `handlers.PrincipalTenants` is a real resolver with a three-step precedence.
- `docs/src/content/docs/operate/security.md` carries the same "The API is not
  authenticated" framing.
- `docs/src/content/docs/operate/configuration.md` says "the code defines
  twelve" sections. `config.Config` has **14**.
