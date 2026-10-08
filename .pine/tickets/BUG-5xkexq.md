---
id: BUG-5xkexq
title: $('X') default branch walks edges into one input by node ID, not n8n's connection order
status: done
priority: low
created: "2026-09-25T10:25:28Z"
updated: "2026-10-08T16:30:00Z"
---

# Description

Found by the review of BUG-fthahg (2026-09-25). `connectedOutputs`
(internal/engine/node_branches.go) finds which output of X a node reads by
default for `$('X').all()`, `.first()` and `.last()`, walking upstream breadth
first as n8n does. Edges into one node are walked by input index (right), but
edges into the same input come in the prepared graph's order, which sorts them
by source node ID. n8n walks them in the order its connections object lists
them for that input, which follows how the workflow's connections were
written, not node IDs.

The answer can only differ when two different paths back to X enter the same
input of one node (for example IF true → A → C and IF false → B → C, both into
C's only input): n8n may meet the path through B first and read X's false
output where KilasFlow reads the true one.

# Expected

The walk meets edges into one input in the order n8n would, which needs the
document's connection order (or an import-time order index) carried into the
IR, since the importer and the compiler do not keep it today.

# Acceptance Criteria
- [x] Edges into one input are walked in the order the workflow lists them,
      not by node ID.
- [x] A test with two paths back to X through the same input reads the output
      n8n reads.

## Fix

The order was lost in two places, and both now keep it.

- **Import** (`internal/interop/n8n/n8n.go`): `importConnections` sorted the
  source node names, because `Connections` is a Go map. `listedSources` now
  reads the names from the payload's `connections` object in the order the
  JSON lists them, and the edges come out source by source in that order
  (sources it could not read follow by name). That is the order n8n's
  `mapConnectionsByDestination` puts the connections into one input in. The
  compiler already keeps document order for `ir.Edges`.
- **Walk** (`internal/engine/node_branches.go`, `runner.go`): the prepared
  graph records each edge's position in the compiled graph (`listed`), and
  `inputOrder` sorts a node's incoming edges by input position, then by that
  position, rather than by source node ID.
- Delivery order is unchanged on purpose. The prepared graph's `incoming`
  sort (port name, then source ID) stays, because
  `TestEveryAttachedToolReachesTheAgentInAStableOrder` (nodes/ai_test.go)
  guarantees that reordering the saved JSON cannot reorder an agent's tools.
  Making `incoming` follow document order globally broke that test.

Tests: `TestDollarNodeWalksEdgesIntoOneInputInTheOrderTheWorkflowListsThem`
(engine: Gate true → A → Join, Gate false → B → Join, B's edge listed first;
Join and After read Gate's false output) and
`TestImportKeepsTheOrderN8nListsConnectionsIn` (n8n: sources listed
Zeta, Start, If, Alpha come out in that order).
