---
id: BUG-5xkexq
title: $('X') default branch walks edges into one input by node ID, not n8n's connection order
status: todo
priority: low
created: "2026-09-25T10:25:28Z"
updated: "2026-09-25T10:25:28Z"
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
- [ ] Edges into one input are walked in the order the workflow lists them,
      not by node ID.
- [ ] A test with two paths back to X through the same input reads the output
      n8n reads.
