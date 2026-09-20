---
id: FEAT-mha6a0
title: 'Agent surface: CLI plus an agent skills bundle first, MCP adapter to follow'
status: doing
priority: medium
labels:
    - api
    - sdk
    - agent
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T07:42:30Z"
---

## Decision already taken (2026-09-20)

An AI agent must be able to create, edit, trigger and debug workflows. The chosen shape is:
**one API + one token model, a CLI as the primary surface, and an MCP server as a thin adapter
over the same API afterwards.** The CLI comes first because it is where the debug verbs belong
(`exec trace`, `debug eval`, `run --wait`) and because MCP tools map naturally onto the same
operations once the CLI has settled them.

## Why this is a design ticket, not an implementation ticket

The blocking question is not the transport, it is authority. There is no scoped credential
today: an API key is tenant-wide (`internal/auth/auth.go:7-10`; `api_keys` has no scope
column), so handing an agent a key hands it the whole tenant — including `activate` (which
publishes a public endpoint), `delete`, and credential metadata. The scope vocabulary already
exists in the embed session (`internal/embed/embed.go`: `workflow:read|write|run`,
`datastore:read|write`) and the default-deny path gate already exists in
`internal/api/middleware/embed.go`; the design has to say how an agent token reuses them.

## What the design must specify

- [ ] The agent token: format, scopes, optional workflow/datastore binding, TTL, minting
      authority, revocation, and which operations are refused at any scope (activate, delete,
      import, credential read beyond names).
- [ ] Audit: where the agent's identity is recorded (executions, publish events, workflow
      versions) and what an operator can query afterwards.
- [ ] The debug loop: which primitives are missing today and how they are added —
      validate-without-saving, run a pinned revision, replay from a node, evaluate an
      expression against an execution's context, tail the event stream.
- [ ] The CLI surface: command tree, `--json` output contract, exit codes, token storage and
      precedence, and how it coexists with the existing binary (`kilasflow` with no
      subcommand must keep meaning "serve", because the container entrypoint depends on it).
      Note `bin/kflow` in the tree is a stale build of the server binary, not a CLI.
- [ ] The MCP adapter: tool list, which tools are read-only vs guarded, and how tool calls map
      onto the same operations rather than a second implementation.
- [ ] Dependencies on other work: idempotent runs, per-tenant node visibility for
      `node_list`, and the existing SSE stream for `tail`.

## Deliverable

A design document committed to the repository (default location
`docs/superpowers/specs/2026-09-20-agent-surface-design.md`, adjustable), reviewed and
approved by the owner, plus the implementation tickets it decomposes into. No code lands
under this ticket.

## Out of scope

Implementation. Also out of scope: giving an agent the ability to publish or delete anything,
and any token that is not tenant-bound.

## Skills bundle (added 2026-09-20, at the owner's direction)

The CLI is not the whole deliverable. It ships with an **agent skills bundle**, modelled on the
n8n skills pack the owner keeps in `~/.agents/skills/` (`using-n8n-skills-official` as the
always-on router plus one skill per domain, each with `references/*.md` for depth).

### Why skills are part of the design, not documentation

A workflow engine is a large, opinionated surface: 73 operations, a node catalogue that changes
shape per version, an expression language, a trigger lifecycle, a tenant boundary. An agent with
only an OpenAPI document will guess parameter names, publish workflows by accident, and put
secrets in text fields. The n8n pack solves this with a router skill that names the invariants
and routes to a domain skill at the moment of decision, plus a compact tool reference that
compensates for deferred tool descriptions. KilasFlow needs the same, with one difference: our
skills must also state what is **not shipped** (WASM packs, sidecar, agent tokens), because the
2026-09-20 audit found the docs claiming those paths as working.

### Shape

- Source of truth: `skills/<skill-name>/SKILL.md` in this repository, versioned with the CLI.
- Embedded in the binary (`//go:embed skills`), the way `internal/web` embeds the SPA, so
  `kilasflow skills install` works from the distroless image with no checkout.
- Frontmatter per skill: `name`, `description` ("Use when … triggers on …"), and a
  `kilasflow_skills_version` stamp the CLI can check.
- `skills/index.json` generated (not hand-written) so harnesses can read the index without
  parsing markdown.
- Each skill states the **CLI verb first, the HTTP operation second**, so it is usable by an
  agent that has no CLI and talks to `/api/v1` directly — which is also what makes the MCP
  adapter a mapping rather than a rewrite.

### Inventory (13 skills: one always-on router + 12 domain skills)

| Skill | Trigger |
|---|---|
| `using-kilasflow-skills` | Always-on router: non-negotiables, red-flag rationalisations, skill index, compact command/tool reference, protocol order |
| `kilasflow-workflow-lifecycle` | Creating, editing, publishing, activating, versioning, restoring a workflow |
| `kilasflow-expressions` | Any `{{ }}`, `$json`, `$node`, `$now`, Luxon, or expression error |
| `kilasflow-node-configuration` | Configuring any node: describe first, never guess parameters; property kinds, displayOptions, loadOptions, ports, typeVersion |
| `kilasflow-triggers` | Webhook, form, schedule, manual, sub-workflow triggers; activation lifecycle; inbound auth modes |
| `kilasflow-debugging` | Errors, unexpected output, "it's not working": trace, node runs, event tail, structured errors |
| `kilasflow-error-handling` | Error workflow, per-node retry, response modes, timeouts |
| `kilasflow-credentials` | Any secret, token, or auth mention; credential types; allowed domains; never paste secrets |
| `kilasflow-datastore` | Tables, columns, rows, filters, CSV, limits, tenant scoping |
| `kilasflow-import-export` | n8n migration, `Lossy[]` reports, what the mapping cannot carry |
| `kilasflow-node-packs` | Declarative packs: scaffold, validate, checksum, install — and what packs cannot do |
| `kilasflow-embedding` | Host SaaS integration: tenants, API keys, embed sessions, scopes, branding, shared database |
| `kilasflow-operations` | Running the product: roles, readiness, migrations, backups, upgrade order |

### How the skills stay true (the part that matters)

A skills pack that drifts is worse than none, so each claim is gated in CI, mirroring
`sdk/test/operation-coverage.test.mjs`:

- [ ] Every `kilasflow <verb> <sub>` inside a fenced block in any SKILL.md resolves in the real
      command tree (a Go test that builds the command tree and walks the skills).
- [ ] Every operation id in `docs/src/content/docs/reference/api-contract.md` is reachable as
      `kilasflow api <operation-id>`, and every operation named in a skill exists in the spec.
- [ ] Every node type named in a skill exists in the registry, and every expression root named
      in `kilasflow-expressions` exists in `internal/expression`.
- [ ] `kilasflow skills check` compares the installed bundle's version stamp with the binary and
      refuses silently-stale skills.
- [ ] A `Makefile` target (`skills-check`) runs all of the above, next to `sdk-check`.

### CLI surface for skills

```
kilasflow skills list                       # names, versions, triggers
kilasflow skills show <name>                # print one SKILL.md (and its references on request)
kilasflow skills install [--target claude|codex|agents|dir:<path>] [--scope user|project]
kilasflow skills check                      # drift between installed bundle and this binary
kilasflow skills export --format json       # machine-readable index
kilasflow context                           # read-only briefing: tenant, node types, workflows, recent runs
```

### Guardrails the router skill must state

- `activate` publishes a public endpoint; `delete` and `import` create or destroy authority.
  Never run them without an explicit instruction from the user.
- A run is not idempotent until `FEAT-hj8pyx` lands; pass `Idempotency-Key` once it does.
- Secrets never appear in a workflow document, a CLI argument, or chat: credentials are
  referenced by id.
- An embed session can never activate, delete, import, or manage datastores — that is by design,
  not a bug to work around.
- Anything the skills cannot verify (node type, parameter name, credential type) is fetched from
  the instance, never recalled from training data.

### Added acceptance criteria

- [ ] The design document specifies the bundle layout, the inventory above, the install targets,
      the version-stamp rule, and the four CI gates.
- [ ] The design states explicitly which capabilities the skills must present as **not shipped**
      today (WASM packs, sidecar, agent tokens, tenant deletion, idempotency) so the bundle
      cannot repeat the drift the audit found.
- [ ] Implementation tickets are cut for: command tree + `api` escape hatch, skills bundle,
      coverage gates, install/check, and the router skill.
