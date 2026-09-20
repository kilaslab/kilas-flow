# Agent surface: CLI, skills bundle and MCP adapter

Status: **draft for owner review** · Ticket: `FEAT-mha6a0` · Epic: `EPIC-bkj6yf`
Date: 2026-09-20 · Supersedes: nothing

An AI agent should be able to create, edit, trigger and debug KilasFlow workflows. This document
designs that surface: what an agent authenticates with, what it can and cannot do, the CLI it
drives, the agent skills that teach it how, and the MCP adapter that follows.

It is written against the source as it exists on 2026-09-20, not against the roadmap. Where a
capability the design needs does not exist yet, that is stated in the phase it belongs to and in
the skill text that ships before it.

---

## 1. Why this is not just "add a CLI"

Three facts from the 2026-09-20 audit shape every decision below.

1. **The HTTP surface is already complete and self-describing.** 73 operations live under
   `/api/v1`, the OpenAPI 3.1 document is generated from the handler types, and
   `docs/src/content/docs/reference/api-contract.md` declares the operation surface. A CLI adds
   ergonomics, not capability.
2. **Authority is the real problem.** There is no scoped credential: an API key is tenant-wide
   (`internal/auth/auth.go:7-10`; `api_keys` has no scope column in
   `migrations/*/000003_identity.up.sql`). Handing an agent a key hands it the tenant — including
   `activate` (which publishes a public endpoint), `delete`, and credential metadata. The scope
   vocabulary and the default-deny path gate already exist for embed sessions
   (`internal/embed/embed.go:31-41`, `internal/api/middleware/embed.go:89`); this design reuses
   them rather than inventing a second permission model.
3. **An agent with only an OpenAPI document will get the product wrong.** It will guess parameter
   names, activate workflows to test them, and put secrets in text fields. That is what the n8n
   skills pack exists to prevent, and it is why the skills bundle is a first-class deliverable of
   this ticket rather than documentation written afterwards.

---

## 2. Goals and non-goals

**Goals**

- An agent can authenticate with a credential that is narrower than a tenant, and the refusal is
  visible to it (a distinguishable exit code, not a generic 403).
- An agent can do the full loop without a browser: create, validate, run, observe, inspect a node
  run, patch, re-run.
- The knowledge needed to do that correctly ships **with the binary**, in a form the major agent
  harnesses read, and cannot silently drift from the product.
- Everything an agent can do, a human operator can do from the same command line.

**Non-goals**

- A Go library facade. The engine stays under `internal/`; this surface is HTTP and CLI.
- Letting an agent publish, delete, or widen its own authority. `activate`, `delete`, `import`,
  tenant administration and credential mutation stay out of reach at every scope.
- A second permission system. Scopes reuse the embed vocabulary; enforcement reuses the same
  middleware position.
- A GUI, a daemon, or a background agent runner. The CLI is one-shot per command.

---

## 3. Authority: the agent token

### 3.1 Shape

An agent token is an API key with a scope list, not a new credential kind.

| Property | Value |
|---|---|
| Format | unchanged: `kfa1_<prefix>_<secret>` (`internal/auth/keys.go`), so every existing path — hashing, prefix lookup, constant-time compare, revocation — is reused |
| Storage | `api_keys` gains `scopes text`, `workflow_id text`, `expires_at timestamp` (new migration, both dialects). `NULL` scopes means the legacy tenant-wide key, so nothing existing changes behaviour |
| Scope vocabulary | exactly the embed set: `workflow:read`, `workflow:write`, `workflow:run`, `datastore:read`, `datastore:write` (`internal/embed/embed.go:31-41`), with the same implication rules (write/run imply read inside one family) |
| Binding | optional `workflow_id` (or `datastore_id`) narrows every request to one subject, the way an embed session does |
| TTL | optional `expires_at`; absent means no expiry (today's behaviour) |
| Minting | `POST /api/v1/api-keys` gains the three optional fields. Only a principal that may mint keys for the tenant may mint one, and **a scoped key can never mint another key** |
| Revocation | existing `DELETE /api-keys/{id}` |

### 3.2 Refused at every scope

These are refused before the handler runs, with a named reason (the embed middleware already
answers this way, and a route added later is denied by its default arm). The refusals apply to
**scoped tokens**; a legacy tenant-wide key is unaffected, which is what keeps an operator's
existing automation working. The CLI's guarded verbs (§4.7) are how a human deliberately
exercises the wider authority, and they are the only path an agent has to these operations —
through a human's explicit instruction:

- `activate-workflow`, `deactivate-workflow` — activation publishes a public endpoint.
- `delete-workflow`, `import-workflow`.
- Everything under `/tenants`.
- `create-api-key` / `revoke-api-key`.
- `create-embed-session` — minting a browser session is a different authority than acting.
- Credential mutation (`create`/`update`/`delete`/`test`); `list` and `get` are allowed on
  `workflow:read` because a node must reference a credential by id, and they never return secret
  values (`internal/repository/credentials.go:363-382` returns the public half only).
- Datastore schema changes (`create`, `rename`, `delete`, `columns`, `clear`); row operations are
  allowed on the datastore scopes.
- Pack installation.

### 3.3 Where enforcement lives

One middleware arm, in the same chain position as `EmbedAuth` (`internal/api/server.go:133-167`),
so the rule is about *what the request targets* rather than which handler runs. A scoped key and
an embed session may share the `permits` switch: both answer the same question about the same
path shapes. The difference is only where the subject comes from.

### 3.4 Audit

An agent's actions must be attributable after the fact.

- `workflow_versions` and `workflow_publish_events` gain `actor_kind`, `actor_label`,
  `actor_key_id` (new migration). Existing rows read as `actor_kind = 'user'` where a session
  wrote them and `'key'` where a key did.
- The CLI sends `X-KilasFlow-Skills-Used: <comma-separated skill names>` on mutating calls; the
  server stores it on the version row as `actor_meta`. This is the KilasFlow analogue of the n8n
  pack's `skillsUsed` argument, and it is how we learn which skills actually get used.
- `GET /workflows/{id}/versions` and `/publish-events` expose the actor fields.

---

## 4. The CLI

### 4.1 Coexistence with the server binary

The binary keeps its current meaning: **`kilasflow` with no subcommand serves**. The container
entrypoint and every Compose file depend on it, and `-config` / `-version` keep working.

`bin/kflow` in the tree is a stale build of the server binary, not a CLI (its only flags are
`-config` and `-version`). It is deleted in phase 1; the command is `kilasflow <verb>`, and a
`kflow` alias is explicitly rejected so there is one name to document.

### 4.2 Command tree

Phases are in §7. `guarded` means the verb refuses without `--yes`. Scope is the minimum an agent
token needs.

```
kilasflow                       serve (unchanged)                         [phase 1]
kilasflow serve                 explicit form, for scripts                [phase 1]
kilasflow version                                                         [phase 1]
kilasflow health | ready                                                  [phase 1]
kilasflow context               read-only briefing for an agent's first turn [phase 1]
kilasflow auth login|logout|whoami                                        [phase 1]
kilasflow skills list|show|install|check|export                           [phase 1]
kilasflow api <operation-id>    generic escape hatch (see 4.3)            [phase 1]

kilasflow workflow list|get|create|update|duplicate|validate              [phase 1/3]
kilasflow workflow versions|get-version|restore|publish-events            [phase 1]
kilasflow workflow export|import|diagnostics                              [phase 1]
kilasflow workflow activate|deactivate          guarded                   [phase 2]
kilasflow workflow delete                       guarded                   [phase 2]

kilasflow run <workflowId>      [--input|--input-file] [--trigger <nodeId>]
                                [--wait] [--revision <versionId>]
                                [--idempotency-key <key>]                 [phase 1/2/3]
kilasflow exec list|get|trace|cancel                                      [phase 1]
kilasflow exec tail <executionId>                                         [phase 1]
kilasflow exec retry <executionId>                                        [phase 3]
kilasflow debug eval <expression> [--execution|--node|--input-file]       [phase 3]

kilasflow node list|describe <type>|options <type> --property <key>       [phase 1]
kilasflow credential list|get|test                                        [phase 1]
kilasflow credential create|update|delete       guarded                   [phase 2]
kilasflow datastore list|get|rows|insert|update|delete|upsert|export|import [phase 1]
kilasflow datastore create|rename|delete|columns|clear  guarded           [phase 2]
kilasflow schedule list|create|update|delete                              [phase 1]
kilasflow webhook list <workflowId>                                       [blocked: FEAT-77rveq]
kilasflow embed session create                                            [phase 1]
kilasflow pack validate                                                   [phase 1]
kilasflow pack install <dir>                    guarded                   [phase 2]
kilasflow tenant list|get|create|users|api-keys                           [phase 1, operator key]
kilasflow tenant delete                         guarded                   [blocked: FEAT-fpqvwx]
```

Two rules keep this tree honest:

- **One verb per operation, no aliases.** An alias is a second thing to document and a second
  thing to break. `run` is the only way to start a run; `workflow run` does not exist.
- **Every verb maps to one operation id**, recorded in the command's own metadata so the coverage
  gate (§5.6) can check it mechanically.

### 4.3 `kilasflow api <operation-id>` — the escape hatch

A generic verb that takes an operation id from the OpenAPI document, a body, query and headers,
and prints the response. This exists for three reasons:

- **Day-one completeness.** 73 operations are reachable before a single ergonomic verb is
  written, and the coverage gate can pass immediately.
- **No dead ends.** When a host or an agent needs an operation nobody wrote a verb for, the
  answer is not "wait for a release".
- **Documentation that cannot rot.** `kilasflow api --list` prints the operation ids the running
  server actually serves, from `/api/openapi.json`.

`kilasflow api` refuses the same operations a scoped token refuses — the escape hatch is not an
authority bypass, it is a naming bypass.

### 4.4 Output contract

- Human-readable by default on a TTY; JSON when stdout is not a TTY or `--json` is passed.
- JSON envelope, always:

  ```json
  { "ok": true,  "data": { }, "meta": { "operation": "run-workflow", "durationMs": 42 } }
  { "ok": false, "error": { "code": "scope_denied", "message": "…", "status": 403,
                            "detail": { "problem": { } } } }
  ```

  RFC 9457 problem documents from the API are carried verbatim under `error.detail.problem`, the
  way `sdk/src/http.ts` already surfaces them.
- `--quiet` prints only the primary identifier (an id, a status) for shell pipelines.
- `--verbose` logs requests with `Authorization` and any credential field redacted.

### 4.5 Exit codes

| Code | Meaning | Agent response |
|---|---|---|
| 0 | success | continue |
| 1 | error (5xx, network, unexpected shape) | report, do not retry blindly |
| 2 | usage error | fix the invocation |
| 3 | **refused by authority** (`403`, or a guarded verb without `--yes`) | do not retry; ask the user or drop the step |
| 4 | not found (`404`) | the resource does not exist for this tenant |
| 5 | conflict (`409`) — optimistic concurrency, idempotency mismatch | re-read, then decide |
| 6 | not ready (`503`) — migrations outstanding, subsystem unconfigured | wait, then retry |

Code 3 is the important one: it is the difference between "the agent cannot do this" and "the
agent did it wrong", and it is what makes the skills' guardrails teachable.

### 4.6 Configuration and token handling

Precedence: `--url` / `--token` flags → `KILASFLOW_URL` / `KILASFLOW_TOKEN` → `--config <path>` →
`~/.config/kilasflow/config.toml` (created `0600`).

- `kilasflow auth login --token -` reads the token from stdin so it never lands in shell history
  or a process listing.
- The token is never printed. `auth whoami` prints tenant, scopes, binding and expiry only.
- A `--token-file` variant exists for CI, and is refused if the file is group- or
  world-readable.
- Nothing about a token is written to logs, including in `--verbose`.

### 4.7 Guardrails

- Guarded verbs require `--yes`; without it they exit 3 with `code: "confirmation_required"` and
  a message naming what the verb does (for `activate`: *publishes a public endpoint*).
- `--yes` is never implied by `--json`, `--quiet`, or a non-TTY. Automation that means it says
  so.
- The skills state, in the router's non-negotiables, that `--yes` is only ever passed on an
  explicit instruction from the user.

---

## 5. The skills bundle

### 5.1 What it is and why it ships in the binary

A set of `SKILL.md` files that teach an agent how to drive KilasFlow correctly, modelled on the
n8n pack (`~/.agents/skills/using-n8n-skills-official` and its siblings). One always-on router
skill plus one skill per domain, each with `references/*.md` for depth.

They ship **inside the binary**, embedded the way the SPA is (`internal/web/embed.go` embeds
`dist/`), so `kilasflow skills install` works from the distroless image with no checkout and no
network.

- Source of truth: `skills/` at the repository root — browsable, reviewable, diffable.
- Build step: the `Makefile` syncs `skills/` into `internal/skills/bundle/` before `go build`,
  exactly as the web build copies into `internal/web/dist`.
- `skills/index.json` is **generated** from the frontmatter, never hand-written, so a harness can
  read the index without parsing markdown.

### 5.2 Frontmatter contract (machine-checkable)

Prose is not checkable; declarations are. Every skill declares what it teaches, and CI verifies
the declarations against the product.

```yaml
---
name: kilasflow-triggers
description: Use when adding or debugging a workflow trigger — webhook, form, schedule,
  manual or sub-workflow. Triggers on "webhook", "cron", "schedule", "form", "trigger".
kilasflow_skills_version: 1
kilasflow_commands:                  # every CLI path this skill teaches
  - kilasflow webhook list
  - kilasflow run
kilasflow_operations:                # every API operation this skill teaches
  - run-workflow
  - activate-workflow
kilasflow_nodes:                     # node types this skill presents as available
  - kilasflow.webhook
kilasflow_not_shipped:               # claims this skill must explicitly deny
  - per-tenant node packs
---
```

### 5.3 Body conventions

Every skill uses the same section order, so an agent can skim any of them the same way:

```
## Non-negotiables       2-4 rules with no exceptions
## Strong defaults       what to do unless the user says otherwise
## Decision tree         a fenced tree for the branches that matter
## Not shipped yet       capabilities this skill must NOT present as available
## Anti-patterns         symptom → what goes wrong → fix
## Reference files       table: file → read when
```

`## Not shipped yet` is KilasFlow-specific and mandatory whenever
`kilasflow_not_shipped` is non-empty. It exists because the 2026-09-20 audit found
`docs/guides/community-nodes.md` presenting WASM packs and the JS sidecar as working paths; the
skills bundle is the last place that mistake should be repeated.

### 5.4 Inventory (13 skills: one always-on router + 12 domain skills)

| Skill | Trigger | References |
|---|---|---|
| `using-kilasflow-skills` | Always-on router. Loaded at session start by whatever hook the harness provides | — |
| `kilasflow-workflow-lifecycle` | Creating, editing, publishing, activating, versioning, restoring | `VALIDATION_CHECKLIST.md`, `NAMING_AND_DESCRIPTIONS.md` |
| `kilasflow-expressions` | Any `{{ }}`, `$json`, `$node`, `$now`, Luxon, or an expression error | `EXPRESSION_ROOTS.md` |
| `kilasflow-node-configuration` | Configuring any node; "never guess a parameter" | `PROPERTY_KINDS.md`, `LOAD_OPTIONS.md` |
| `kilasflow-triggers` | Webhook, form, schedule, manual, sub-workflow; activation; inbound auth | `WEBHOOK_DELIVERY.md` |
| `kilasflow-debugging` | Errors, unexpected output, "it's not working" | `TRACE_READING.md` |
| `kilasflow-error-handling` | Error workflow, per-node retry, response modes, timeouts | — |
| `kilasflow-credentials` | Any secret, token, auth mention | `CREDENTIAL_TYPES.md` |
| `kilasflow-datastore` | Tables, columns, rows, filters, CSV, limits | `FILTERS.md` |
| `kilasflow-import-export` | n8n migration, `Lossy[]` reports | `MAPPING_LIMITS.md` |
| `kilasflow-node-packs` | Authoring or installing a declarative pack | `PACK_FORMAT.md` |
| `kilasflow-embedding` | Host SaaS integration: tenants, keys, embed sessions, branding, shared database | `EMBED_HANDSHAKE.md` |
| `kilasflow-operations` | Roles, readiness, migrations, backups, upgrade order | `UPGRADE_ORDER.md` |

### 5.5 The router skill

`using-kilasflow-skills` is the only skill that must be loaded every session. Its sections:

1. **Non-negotiables** — invoke the matching skill before acting; validate then verify after
   every mutation; secrets never in a document, a flag or chat; `--yes` only on explicit user
   instruction; anything unverifiable is fetched from the instance, never recalled.
2. **Lean on skills, not training data** — the node catalogue changes per version; a remembered
   parameter name is a silent failure.
3. **Strong defaults** — expression before Code node; a reusable step becomes a sub-workflow;
   describe a node before configuring it.
4. **Red-flag table** — the rationalisations that precede mistakes:

   | Thought | Action |
   |---|---|
   | "The workflow is simple, I'll just build it" | Invoke `kilasflow-workflow-lifecycle` |
   | "I know this node's parameters" | Invoke `kilasflow-node-configuration`; `kilasflow node describe` first |
   | "I'll activate it so I can test it" | **STOP.** Activation publishes a public endpoint. Ask the user |
   | "I'll put the token in a Set node" | Invoke `kilasflow-credentials`; credentials are referenced by id |
   | "Validation passed, so it's fine" | Validation ≠ verification: `kilasflow workflow get` and check connections |
   | "The error is obvious, I'll patch it" | Invoke `kilasflow-debugging`; `kilasflow exec trace` first |
   | "A Code node is faster here" | Invoke `kilasflow-expressions` |

5. **Skill index** — the table in §5.4 with triggers.
6. **Compact command reference** — the tree in §4.2, so an agent has working knowledge of the
   surface from turn one without running `--help` on everything.
7. **Protocol, in order** — `kilasflow context` → invoke the matching skill → `node describe`
   before configuring → validate → save → verify by re-reading → run and trace.
8. **Reporting skills used** — pass `--skills-used` on mutating commands so adoption is
   measurable.

### 5.6 Install targets and versioning

```
kilasflow skills list
kilasflow skills show <name> [--reference <file>]
kilasflow skills install [--target claude|agents|codex|cursor|dir:<path>]
                         [--scope user|project] [--force] [--dry-run]
kilasflow skills check
kilasflow skills export --format json
```

- Targets map to the harness directories (`claude` → `~/.claude/skills/<name>/SKILL.md`,
  `agents` → `~/.agents/skills/…`, `--scope project` → `./.agents/skills/…`, matching where this
  repository already keeps its `pine` skill). `dir:<path>` is the escape hatch for anything
  else.
- `kilasflow skills check` compares the installed bundle's `kilasflow_skills_version` stamps and
  the binary's version, and reports drift; it does not silently update anything.

### 5.7 CI gates — the part that keeps the bundle true

The n8n pack relies on drift reports from agents. KilasFlow can do better, and the repository
already has the precedent: `sdk/test/operation-coverage.test.mjs` asserts every operation id is
covered by a client method, and `docs/.../api-contract.md` declares the operation surface.

| Gate | Assertion | Where |
|---|---|---|
| G1 commands | every `kilasflow_commands` entry, and every `kilasflow …` inside a fenced block, resolves in the real command tree | `internal/skills/skills_test.go` |
| G2 operations | every `kilasflow_operations` entry exists as an `OperationID` in `internal/api/handlers/` | same |
| G3 nodes | every `kilasflow_nodes` entry is registered by `nodes.RegisterAll` | same |
| G4 expression roots | every `kilasflow_expression_roots` entry exists in `internal/expression` | same |
| G5 freshness | `kilasflow skills check` passes against the binary built from this commit | `Makefile: skills-check` |

G1–G4 are ordinary Go tests over the embedded bundle, so they run in the normal test suite and
cannot be skipped by forgetting a Makefile target.

### 5.8 What the bundle must present as not shipped

The 2026-09-20 audit found documentation promising capabilities the code does not have, because
that documentation was written against tickets closed `done` with every acceptance criterion
unticked. The bundle must not repeat it. Each row is a claim a skill has to deny, in a
`## Not shipped yet` section, until the named ticket closes.

| Claim the skill must deny | Skill | Until |
|---|---|---|
| WASM node packs: the guest SDK exists, the host-side loader, executor and host ABI do not | `kilasflow-node-packs` | `FEAT-48hreg` |
| JavaScript sidecar: the library exists, nothing imports it and there is no config key | `kilasflow-node-packs` | `FEAT-7cg0cd` |
| Per-tenant node packs: the catalogue is deployment-global | `kilasflow-node-packs`, `kilasflow-embedding` | `FEAT-emf6k5` |
| Scoped agent tokens: before phase 2 an API key is tenant-wide | `using-kilasflow-skills` | phase 2 of this ticket |
| Idempotent runs: a retry is a second execution | `using-kilasflow-skills`, `kilasflow-workflow-lifecycle` | `FEAT-hj8pyx` |
| Tenant deletion: a customer cannot be offboarded | `kilasflow-embedding`, `kilasflow-operations` | `FEAT-fpqvwx` |
| Webhook URL discovery: `GET /workflows/{id}/webhooks` does not exist | `kilasflow-triggers` | `FEAT-77rveq` |
| A Go library: embedding is HTTP plus the iframe editor, never an import | `kilasflow-embedding` | by design, permanently |
| Datastore storage substitution: one shared database, no interface to implement | `kilasflow-datastore` | by design, permanently |

The honesty test in §8 makes the left column mechanical: a skill that declares a
`kilasflow_not_shipped` entry without a matching body section fails the build.

---

## 6. MCP adapter (phase 4)

Not designed in detail here, because its shape follows from the CLI.

- **Tools map to CLI verbs**, and tool descriptions are generated from `skills/index.json`, so
  the skills remain the single source of truth and the adapter cannot describe a capability the
  CLI does not have.
- **Read-only tools are unguarded**; guarded verbs become tools that require an explicit
  `confirm: true` argument, mirroring `--yes`.
- **Transport**: stdio first, streamable HTTP second.
- **Dependency decision is open** (§9). The repository hand-rolls an MCP *client* today
  (`nodes/ai.go:3025`), which is evidence both that hand-rolling is acceptable here and that the
  wire surface is already understood in this codebase.

---

## 7. Phasing

| Phase | Contents | Blocked by |
|---|---|---|
| **1** | Command tree, `api` escape hatch, `context`/`health`/`version`, `auth`, `skills` verbs, read-only verbs, bundle v1 (13 skills, honest `Not shipped yet`), G1–G5 gates, `bin/kflow` removal | nothing — works today with tenant API keys |
| **2** | Scoped agent tokens (`api_keys.scopes`/`expires_at` + middleware arm), guarded verbs, audit columns, `--skills-used` plumbing, idempotency pass-through | `FEAT-hj8pyx` (idempotency) |
| **3** | Debug primitives: `workflow validate`, `run --revision`, `workflow duplicate`, `exec retry`, then `debug eval` | phase 2 for the token; the first three need only new operations |
| **4** | MCP adapter over the settled CLI verbs | phase 3 |

Phase 1 is deliberately usable before phase 2: an agent driving the CLI with a tenant API key
gets the ergonomics and the skills immediately, and the skills say plainly that the key is
tenant-wide and that `--yes` is a human decision.

---

## 8. Verification

- **Gates** G1–G5 in the normal test suite and in `make skills-check`.
- **Honesty test**: for every skill with a non-empty `kilasflow_not_shipped`, assert the body
  contains a `## Not shipped yet` section naming each entry. Cheap, and it is the specific
  defence against the drift the audit found.
- **CLI smoke** (`make smoke-cli`): against a booted server, `context` → `workflow create` →
  `workflow validate` → `run --wait` → `exec trace` → `workflow deactivate`, asserting exit codes
  and the JSON envelope.
- **Authority tests**: a scoped token is refused `activate`/`delete`/`import`/`/tenants` with exit
  code 3 and `code: "scope_denied"`; a workflow-bound token cannot read another workflow (404, not
  403, matching the repository's existing not-found convention).
- **Skills install round-trip**: install into a temp directory, assert the file layout and that
  `skills check` reports in-sync, then mutate a stamp and assert it reports drift.

---

## 9. Open questions

1. **`debug eval`** — evaluate an expression server-side against a live execution's context. It
   is the most useful debugging primitive and the largest new attack surface (a read-only
   evaluator over tenant data). Keep it, or replace it with "re-run with a patched node" only?
2. **MCP dependency** — hand-rolled server versus `modelcontextprotocol/go-sdk`. Deferred to
   phase 4, but the licence boundary check in this repository applies either way.
3. **Skill distribution beyond the binary** — should the bundle also be installable as a
   standalone plugin/npm package (the way the n8n pack is a plugin), or is `skills install` from
   the binary enough?
4. **`context` under a scoped token** — should `kilasflow context` show only the bound workflow
   (consistent) or the whole tenant (useful, but it leaks the shape of the tenant)?
5. **Spec home** — this document lives under `docs/superpowers/specs/`, which is inert for the
   Astro site. If design specs should be part of the documentation site, they need a content
   collection and a nav entry.

---

## Appendix: what an agent session looks like

```text
$ kilasflow skills install --target claude --scope project
installed 13 skills into ./.agents/skills (bundle v1)

$ kilasflow context
tenant: acme            (key: ci-agent, scopes: workflow:read,workflow:write,workflow:run)
node types: 214          workflows: 12          datastores: 3
recent executions: exec_01J… completed 2m ago · exec_01J… failed 9m ago

$ kilasflow workflow get wf_01J… --json | jq '.data.nodes | length'
7

$ kilasflow node describe kilasflow.http --json | jq '.data.parameters[].key' | head -3
"method"
"url"
"authentication"

$ kilasflow workflow validate --file patch.json          # exit 0, diagnostics: []
$ kilasflow workflow update wf_01J… --file patch.json    # exit 0
$ kilasflow run wf_01J… --input-file payload.json --wait --skills-used kilasflow-debugging
execution: exec_01K… status: failed node: HTTP Request error: 401 from api.acme.test

$ kilasflow exec trace exec_01K… --node "HTTP Request" --json | jq '.data.nodeRuns[0].error'
{ "code": "http_error", "message": "401 from api.acme.test" }

$ kilasflow credential list --type httpHeaderAuth --json | jq -r '.data[].id'
cred_01J…

$ kilasflow workflow update wf_01J… --file patch.json     # bind cred_01J…
$ kilasflow run wf_01J… --wait                            # exit 0
```

Every command above maps to an operation that exists today except `workflow validate`,
`workflow duplicate`, `run --revision`, `exec retry` and `debug eval` (phase 3), and
`--skills-used` (phase 2, stored server-side).
