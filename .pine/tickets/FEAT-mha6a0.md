---
id: FEAT-mha6a0
title: 'Agent surface: design the CLI first, with an MCP adapter to follow'
status: todo
priority: medium
labels:
    - api
    - sdk
    - agent
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T05:26:52Z"
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
