---
id: EPIC-c7gbdp
title: KilasFlow V1 — API-first workflow platform
status: done
priority: high
labels:
    - roadmap
    - v1
    - api-first
phase: p0
created: "2026-08-29T15:39:05Z"
updated: "2026-09-05T03:30:00Z"
---

## Objective

Deliver the V1 workflow platform through thin, executable vertical slices: server-owned workflow state, a deterministic Go runtime, a generic Svelte Flow editor, and a safe embeddable surface.

## Delivery rules

- API first: the editor is a client of the REST API; it never owns authoritative workflow state or executes workflows.
- A draft graph may be incomplete. Activation and execution must compile and validate it.
- Workflow JSON contains credential references only; it never contains plaintext secrets.
- Persist execution and node-run evidence from the first runnable slice. Redact sensitive inbound and outbound data before persistence.
- Implement the standalone editor before the full embed session experience; preserve the embed boundary in all routes and API choices.
- Do not make n8n JSON or an AI framework the runtime contract.

## Roadmap order

Foundation → domain contract → workflow CRUD → registry → graph runtime → dashboard shell → first editor slice → execution inspector → credentials/expressions/HTTP → webhook/scheduler → SQL → AI → Go Code → embed → n8n interop.

## Product references

- `gflow-prd-v1.md` §§3–5, 17–25, 33–42, 49–60, 65.
- `design-refs/n8n/INDEX.md`: interaction and information-architecture reference only; do not copy n8n branding or visual style.

## Agent workflow

- Use the `pine` skill and update this epic/child ticket with decisions and verification evidence.
- Use `find-docs` before relying on library-specific APIs or configuration; record the exact official documentation consulted in the child ticket.
- Use implementation, testing, debugging, and verification skills only when their trigger applies; do not invoke generic Superpowers by default.

## Outcome

All nineteen child tickets are done, delivered in the roadmap order this epic set out. Each landed as one commit carrying its own code, tests, and evidence.

| Phase | Ticket | Commit |
| --- | --- | --- |
| p0 | FEAT-209rxk foundation | earlier |
| p0 | FEAT-kab4t5 rename to KilasFlow | earlier |
| p1 | FEAT-kk9h5y workflow contract and persistence | earlier |
| p1 | FEAT-rcm205 CRUD and lifecycle versioning | `13d9d09`, `883bd8f` |
| p1 | FEAT-6yef51 node registry and property metadata | `13d9d09` |
| p1 | FEAT-7j7c84 UI theme and OpenAPI query foundation | `372031c` |
| p1 | FEAT-3mady6 deterministic execution runtime | `a51e8dc` |
| p1 | FEAT-19f1ny dashboard shell and workflow list | `f69fa06` |
| p1 | FEAT-f681vt generic canvas editor | `71101bb` |
| p1 | FEAT-0j7r5s execution history and inspector | `147f782` |
| p2 | FEAT-pn3dtq credentials, expressions, HTTP Request | `92ff1ab` |
| p2 | FEAT-9ns8cr streamed execution events | `72aa907` |
| p2 | FEAT-6nh0wm webhook and schedule triggers | `4e89d5f` |
| p3 | FEAT-vwzd6r PostgreSQL, MySQL, SQLite nodes | `2512c74` |
| p4 | FEAT-6msqy3 AI adapter, agent graph, memory, tools | `3cb5a61` |
| p5 | FEAT-w3s12y Go Code node on WASM | `3c2d0a3` |
| p6 | FEAT-900msn embedded editor sessions | `796a774`, `d964d3c` |
| p6 | FEAT-zgm5s6 host SDK | `948eb87` |
| p7 | FEAT-chxkvq n8n import and export | `b8e6850` |

## Delivery rules, as actually built

- **API first.** The editor holds no authoritative state: every save, activation, and run is a REST call, and the generated client is checked against the live OpenAPI document in CI (`pnpm generate:api:check`). The embed surface is a client of the same API under a narrower session.
- **A draft may be incomplete; activation must compile.** `SaveDraft` accepts a partial graph; the compiler is the single validation authority, and both activation and execution go through it. The n8n placeholder node proves the boundary holds from the outside: an imported unsupported node saves and opens but can never activate.
- **No plaintext secrets in workflow JSON.** Credentials are AES-256-GCM at rest with a split between secret and public fields; documents carry references only, and `execution.Redact` sits at the repository payload boundary and the API resource mappers rather than at each call site.
- **Evidence from the first runnable slice.** Execution and node-run records were persisted by `a51e8dc`, before any node that could produce interesting data existed.
- **Standalone editor before embed.** The editor shipped in `71101bb`; embed sessions in `796a774`, and the boundary they added was then tightened by `d964d3c` after review found a session could read another workflow's executions.
- **n8n is not the runtime contract.** `internal/interop/n8n` is a boundary adapter with no runtime dependency in either direction, and it is the last thing built rather than the thing everything else was shaped around.

## Verification

Every ticket was verified before its commit with `go test ./...`, `go test ./... -race`, `go vet ./...`, the web `pnpm check` / `test` / `generate:api:check` / `build` set, and `make smoke-sqlite`. User-facing slices were additionally proved against a running binary rather than only in tests: a manual run end to end, HTTP with expressions and credentials plus a denied SSRF target, an SSE stream with resume after disconnect, webhook 404/401/201, a schedule firing exactly once, the embed iframe under read-only and write scopes and denied from a wrong origin, the SDK driving create → session → run → events, and an n8n workflow importing, refusing activation, and exporting back.

## Learnings carried forward

Three findings changed the design rather than just the code, and are recorded in `.pine/memory/`:

- Redaction belongs at a boundary, not at call sites. Putting it in `payload()` meant no future writer can forget it.
- A framework's default optimisation can be a correctness bug. TanStack Query's property tracking silently stopped notifying the embedded editor; `notifyOnChangeProps: 'all'` trades a little re-rendering for behaviour that does not depend on which fields a template happened to read first.
- Refusing to guess is a feature. The unsupported node, the explicit expression marker, and the refusal to import duplicate node names all cost convenience and buy a system that never silently does something other than what the document says.
