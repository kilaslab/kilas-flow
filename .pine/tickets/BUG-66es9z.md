---
id: BUG-66es9z
title: Datastore ifExists/ifNotExists double-output port defect (canvas + executor)
status: testing
priority: medium
created: "2026-09-20T00:50:48Z"
updated: "2026-09-20T01:15:39Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Work (PortsDiagnostics 2026-09-20, nodes/datastore.go + canvas port mirror)

Commit `db8831e` — nodes/datastore.go, nodes/datastore_test.go, web/src/lib/workflow-editor/ports.ts, web/src/lib/workflow-editor/ports.test.ts.

**The defect.** `datastorePortsFor` returned two outputs both named `main`. A connection's port is resolved *by name* (`internal/workflow/compiler.go` `outputPort`, first match wins; the n8n importer's `resolvePort`/`outputIndexesFor` do the same), so every wire — drawn in the canvas or imported — landed on output index 0 and the second output could not be addressed at all: a connection naming it was refused by the compiler (`port.unknown`, "connection must reference declared source and target ports"), and the canvas's two handles shared one id.

**Fix.**
- The two ports are now the two outcomes of the operation's own test, named the way the IF node names its branches (`nodes/core.go`): `true` for the outcome tested for, `false` for the one it does not. Identity holds while the label follows the configuration — If Exists: `Row found` / `No row`; If Not Exists: `No row` / `Row found`. This is the Switch rule (the name is the identity, the label moves) applied to a fixed fork; per-operation names (`rowFound`) would have swapped the ports' identity between the two operations.
- `web/src/lib/workflow-editor/ports.ts` `datastoreOutputs` mirrors the same two names and labels, so the canvas handle id, `canConnect` and the saved connection agree with the server.
- **Behaviour change in the executor (deliberate, flagged):** `runBranch` sent the tested item to the *second* port for If Not Exists whichever way the test went, so its first port could never carry anything (`emit([])` — a fork with a permanently dead arm). The item now leaves on the first port when the table holds no match, mirroring If Exists' "test held → first port". The old test pinned the dead arm; it was rewritten to the mirror rule with both outcomes asserted.

**Evidence** (scoped, HEAD = `db8831e`):
```
go test ./nodes/ -run 'Datastore|IfExists' -count=1            # ok
web: vitest run src/lib/workflow-editor/ports.test.ts          # 17 passed
```
Pre-fix, against the reverted file, the new tests fail for the right reasons:
- `ifExists outputs are both named "main", so one of them can never be addressed`
- `Compile() error = connection must reference declared source and target ports` (the second port was unreachable)
- `if-not-exists on an absent row = [0 1] items per port, want 1 and 0` (the dead first port)

What the tests prove: (1) both branch operations declare two outputs with distinct names and labels; (2) a document wiring `false` compiles with `SourceOutputIndex == 1`, and the retired `main` name is refused with the port named instead of being silently rewired onto the first branch; (3) in a compiled run, an absent row puts the tested item on the node wired to the second port while the first port's node is recorded skipped, and a matching row does the reverse.

**Not verified:** the canvas was not rendered in a browser for this change (no server session was used; other agents hold uncommitted server/frontend work). The derivation the canvas consumes (`resolvedPorts` → handle id `port.name`, label `portLabel`) is pinned by the vitest file.

**For anyone holding a document written against the old shape:** a connection whose source port is `main` on a branch node is now refused with `port.unknown` (previously it silently ran the first branch). No migration exists — `schemaVersion` is a gate, not a migration — and none is needed for this repository's data (the local store holds no workflows); it is called out here because the refusal is user-visible.

### Documented bound (export to n8n)

n8n's Data Table node answers `rowExists`/`rowNotExists` with a **single** pass-through output (its docs; the KilasFlow node deliberately forks instead). A KilasFlow-authored wire on the second branch therefore exports to `main[1]` — `internal/interop/n8n/n8n.go` `Export` writes the slot the source node's own port index maps to (`outputIndexesFor`, "false" → 1) — and n8n, which walks only the outputs its node declares, will not run it. The wire is not *rewired* (index 1 ≠ 0) and nothing else in the file is affected, but the second branch does not survive an n8n round trip.

Not fixed here: an honest diagnostic needs "how many item outputs does n8n's equivalent declare" as data on the mapping table (`mapping` in `internal/interop/n8n/n8n.go`), which is a wider change than this ticket. Worth a ticket of its own if the n8n round trip is to stay lossless-by-report.

### Canvas note

`canvas-node.svelte` renders outputs as `{#each mainOutputs as port, index (port.name)}` — **keyed by the port name**. Two ports named `main` were therefore a duplicate key as well as a collapse, so the fix is what makes the second handle exist at all; the add-step button is wired to `port.name`, which is the same string the connection and the compiler use.
