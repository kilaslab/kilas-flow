---
id: BUG-gk7mf5
title: n8n import reports no blocking issue for a cycle that run and activate refuse
status: done
priority: medium
labels:
    - import
    - n8n
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T10:19:47Z"
---

# Description

Found by the 2026-09-25 Code-node self-test. Templates 2536 and 3655 import with no blocking diagnostic, but run and activate both fail with `workflow.invalid_topology` (2536 loops If → Wait → Code → Respond to Webhook → If). Refusing such cycles at run time is documented, but the migration guide promises that "no blocking entries means the workflow will activate".

# Expected

The import report carries a blocking diagnostic naming the cycle (the same check run and activate use), so the report and the run agree.

# Acceptance Criteria
- [x] Import reports a blocking diagnostic for any topology run/activate would refuse, naming the nodes in the cycle.
- [x] A test covers the 2536 shape.

# Notes

## Plan (2026-09-25)

Run (`repository/executions.go`) and activate (`repository/workflows.go`) both refuse through `workflow.Compile`. Compile's cycle rule removes the edges that close onto a `LoopEntry` node (Loop Over Items), then refuses any cycle left. It only runs that rule once nothing else in the workflow is wrong. So the import can't get the cycle by calling Compile: nearly every real import has a credential to re-bind, and that would hide the cycle.

Plan: expose the rule itself as `workflow.IllegalCycles(document, catalog)`. It builds the same graph Compile builds, from the same helpers (`resolveDefinition`, `resolveEdge`, extracted out of Compile), and returns one cycle per strongly connected group, as node IDs in run order, starting where the flow enters the group. Compile keeps its message and refuses when `illegalCycles` finds anything. The importer calls `IllegalCycles` on the document it saves and adds one `blocking` issue per cycle (`field: connections`), attached to the entry node, with the loop named by node name.

Decisions:
- One issue per strongly connected group, not one for the whole workflow. Template 3655 has six separate polling loops, and naming only the first would leave the author to find the rest one failed activation at a time. It is also not one issue per elementary cycle, because a dense group can have exponentially many.
- The path named is the shortest cycle through the entry node (breadth first), so it reads as the plainest loop.
- Scope: cycles only. The other `workflow.invalid_topology` refusals (a node disconnected from the trigger, a trigger with an incoming edge, a duplicate chat trigger or tool name) are not reported at import yet. See the concern in the report.
- The compiler's own refusal message is unchanged. It still does not name the nodes. Only the import report does.

## Progress (2026-09-25)

Done, awaiting review.

- `internal/workflow/cycle.go`: `IllegalCycles`, `cyclesAmong` (Tarjan SCC, entry node, BFS shortest cycle).
- `internal/workflow/compiler.go`: `resolveDefinition` and `resolveEdge` are extracted from Compile, whose refusals are unchanged. `hasIllegalCycle`/`hasAnyCycle` became `illegalCycles`/`cyclesAmong`.
- `internal/interop/n8n/n8n.go`: `cycleIssues`, called from `Import` when a catalogue is given.
- Tests: `internal/workflow/cycle_test.go` covers the 2536 shape, a Loop Over Items back edge (not reported), a cycle inside a loop body, two separate cycles and no cycle. Every case checks that `IllegalCycles` agrees with `Compile`. A further test shows the cycle is named while a missing parameter would hide it from Compile. `internal/interop/n8n/cycle_import_test.go` covers the 2536 shape by hand (one blocking issue naming "If All Finished" → … → "If All Finished", and Compile refuses the same document), a Loop Over Items loop (not reported, and it compiles), and a polling loop reported beside a credential issue. RED: no cycle issue was reported. GREEN now.
- On the real repros: 2536 reports its one loop. 3655 reports six, three image polls and three video polls.
- Docs: migration guide, "Loops that do not go through Loop Over Items" and the blocking row. CHANGELOG: Fixed.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `4ee3d1a9` (last commit at or before ticket created 2026-09-25)
- Commits (1):
  - `cb44dc73` — BUG-gk7mf5: n8n import reports each loop that run and activate refuse as blocking, using the compiler's own cycle rule
- Files changed (the ticket's own commits, 4d76999..cb44dc7):

```
 .pine/tickets/BUG-gk7mf5.md                   |  34 +++++++-
 CHANGELOG.md                                  |   7 ++
 docs/src/content/docs/guides/n8n-migration.md |  26 ++++++-
 internal/interop/n8n/cycle_import_test.go     | 178 ++++++++++++++++++++++++++++++++++++++++++
 internal/interop/n8n/n8n.go                   |  67 ++++++++++++++--
 internal/workflow/compiler.go                 | 156 +++++++++++++++++++------------------
 internal/workflow/cycle.go                    | 192 ++++++++++++++++++++++++++++++++++++++++++++++
 internal/workflow/cycle_test.go               | 161 ++++++++++++++++++++++++++++++++++++++
 8 files changed, 735 insertions(+), 86 deletions(-)
```
