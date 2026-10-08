---
id: BUG-t9e3k5
title: n8n import reports no blocking issue for the other topology refusals run and activate make
status: done
priority: low
labels:
    - import
    - n8n
created: "2026-09-25T10:19:47Z"
updated: "2026-09-25T10:19:47Z"
---

# Description

Left by BUG-gk7mf5 (2026-09-25). n8n import now reports as blocking each cycle that run and activate refuse, through the compiler's own rule (`workflow.IllegalCycles`). The compiler's other `workflow.invalid_topology` refusals still import with nothing blocking, so the migration guide's promise, that no blocking entries means the workflow activates, can still fail for them:

- a node disconnected from every trigger
- no trigger root at all
- a missing required input edge
- a duplicate chat trigger or duplicate tool name

# Acceptance Criteria
- [x] Every compile refusal a clean import can produce is reported at import as blocking, from the compiler's own checks (not a copy of them), naming the nodes.
- [x] Tests cover each refusal class.

## Fix (2026-10-08)

`topologyIssues` (internal/interop/n8n/n8n.go) compiles the document the import saves with `workflow.Compile`, the authority run and activate use, and turns each graph-shape refusal into a blocking entry with `Field: "topology"`, naming the node. Graph-shape refusals are `workflow.invalid_topology` (except cycles, which `cycleIssues` already names by path) and `port.required` / `port.full` / `port.node_not_allowed` / `port.incompatible` / `port.unknown`. The compiler's node ids are rewritten to node names in the reason.

- The compiler checks shape only once every node's configuration passes, and an import never carries credentials. Compiled as saved, nearly every real import would stop at its missing credentials and report nothing about its shape. So the check compiles a copy with stand-in ids for the required credentials (`withStandInCredentials`). Nothing saved changes.
- `ImportFragment` (editor paste) drops topology entries. A pasted piece of a workflow has no trigger by nature; it lands in a workflow that has one.
- Tests: `internal/interop/n8n/topology_import_test.go` covers a node no trigger reaches, no trigger at all, a second chat trigger, an agent with no chat model (`port.required`), and two tools whose names normalise to one. Each failed before the change. It also covers a clean import with no topology entry and a pasted fragment with none. Three existing tests that import a lone node now ignore its topology entry, because they test the node's conversion.
- `go test ./internal/interop/... ./internal/api/` and `make corpus-check` pass.

