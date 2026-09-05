---
id: FEAT-chxkvq
title: Add constrained n8n workflow import and export
status: done
priority: medium
labels:
    - interop
    - n8n
    - import
    - export
deps:
    - FEAT-6nh0wm
    - FEAT-vwzd6r
    - FEAT-6msqy3
    - FEAT-w3s12y
    - FEAT-900msn
parent: EPIC-c7gbdp
phase: p7
created: "2026-08-29T15:42:28Z"
updated: "2026-09-05T03:20:00Z"
---

## Scope

Implement interoperability as a boundary adapter after KilasFlow's canonical contract and supported native nodes are proven. Never execute arbitrary n8n JSON or acquire a runtime dependency on n8n packages.

## Acceptance criteria

- Import accepts only documented n8n workflow shape and maps supported nodes/connections into canonical KilasFlow JSON through a dedicated adapter.
- Supported mapping covers exactly the advertised node/version subset; unsupported nodes remain visible with actionable unsupported metadata and cannot silently execute as a different node.
- Import validates/compiles the mapped KilasFlow workflow before it can be activated or run; malformed/untrusted input cannot create executable unsafe behaviour.
- Export produces documented n8n-compatible JSON for supported canonical nodes. Round-trip fixtures identify intentional lossy fields and preserve supported graph semantics.
- Fixture tests cover normal linear, IF branching, webhook/HTTP, SQL, and unsupported-node workflows. User-facing import/export errors explain the exact unsupported element.

## References

- PRD: §§19–24, 42, 57; Milestone 7; Definition of Done item 17.
- Workflow shape references: `12-canvas-wired-manual-set-http.png`, `14-canvas-if-branching.png`, and `design-refs/n8n/INDEX.md` entries 12/14.

## Relevant documentation

- Use `find-docs` or official n8n documentation to verify the currently supported workflow JSON fields and node mapping assumptions. Record source URL, observed version, and every intentional compatibility limitation.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.

## Implementation Plan

- `internal/interop/n8n` is a boundary adapter and nothing more: it translates n8n JSON into canonical nodes and back. KilasFlow never executes n8n JSON and takes no runtime dependency on any n8n package.
- Import saves through the ordinary `SaveDraft` path, so the existing compiler stays the single validation authority rather than the adapter growing a second, weaker one.
- An unmappable node becomes `kilasflow.unsupported`, a registered node that preserves the original type and parameters and whose `Validate` always fails.

## Advertised subset and compatibility limits

Ten mappings, listed by `SupportedMappings()` and returned in every export response so the claim is visible rather than inferred: manualTrigger, set, if, merge, httpRequest, webhook, respondToWebhook, scheduleTrigger, postgres, mySql.

Intentional limitations, each surfaced as a named import or export issue rather than silently applied:

- n8n marks an expression with a `=` prefix; KilasFlow uses an explicit marker. The prefix is translated in both directions. A fixed n8n string that genuinely begins with `=` is unrepresentable in n8n itself — that ambiguity is precisely why KilasFlow's marker is explicit.
- IF supports one condition and four operators; extra conditions and any other operator are named, not dropped quietly.
- n8n's "keep only set fields", non-append Merge modes, disabled nodes, non-cron Schedule intervals, and non-`main` connection kinds have no equivalent and are reported.
- Credentials are never imported or exported: an n8n credential ID means nothing here and a KilasFlow one means nothing there.
- KilasFlow's SQLite, Code, and AI nodes have no n8n equivalent and are omitted from an export with the node named.

## Work Evidence

- Fixture tests cover every shape the ticket asks for: linear, IF branching, webhook + HTTP + Respond, schedule + SQL, and a workflow containing an unsupported node.
- The unsupported node satisfies all three clauses at once. It is *visible* (present in the document with `originalType` and the whole original JSON preserved), it *cannot silently execute as a different node* (compilation fails), and the message *names the exact element*. Verified live: a workflow containing `n8n-nodes-base.slack` imported fine, and activation was refused with `this node was imported from n8n-nodes-base.slack, which KilasFlow does not support`.
- Port mapping is node-aware, not a positional guess. n8n identifies outputs by index; KilasFlow names them, and IF's are `true`/`false` where everything else is `main`. An earlier index-only mapping produced a graph that would not compile, which is what caught it. `outputIndexesFor` is now the exact inverse of `outputPortsFor`, so a branch round-trips to the branch it started from — confirmed live: `If → [[Gold], [Notify]]` survived export.
- Round-trip fixtures assert that node types and the edges between them survive, and separately assert what is intentionally lossy. An imported-then-exported unsupported node returns as its original n8n node with its original parameters, because it came from n8n and belongs there.
- Malformed and untrusted input cannot create unsafe behaviour: empty, non-JSON, node-less, and wrong-typed documents are all refused, and duplicate node names are refused outright because n8n keys connections by name and importing an ambiguously routed graph would be worse than refusing.
- `POST /api/v1/workflows/import` and `GET /api/v1/workflows/{id}/export` are wired, an embed session can export but never import, and the import goes through the ordinary draft path — the imported workflow reads back and activates like any other.
- `go test ./...`, `go test ./... -race`, `go vet ./...`, `pnpm test`, `pnpm check`, `pnpm generate:api:check`, `pnpm build`, SDK `pnpm test`/`check`/`build`, `make smoke-sqlite`.
