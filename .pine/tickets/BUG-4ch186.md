---
id: BUG-4ch186
title: alwaysOutputData runs a node that got no input, which loops a Loop Over Items forever
status: todo
priority: high
labels:
    - engine
    - n8n
    - parity
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Found by the 2026-09-25 Code-node self-test (importing real n8n templates). A node with `alwaysOutputData: true` runs even when its parent sent it nothing, and emits an empty `{}` item. In n8n a node whose parent emitted no items is not executed at all; `alwaysOutputData` only applies to a node that did run and produced no output. On a Loop Over Items (`splitInBatches`) back edge the stray `{}` item restarts the loop, so the execution runs ~6,000 node runs until the 2-minute execution timeout.

# Steps to Reproduce

1. Manual Trigger → Code (`return []`) → Code with `alwaysOutputData: true` (`return [{json:{ran:true}}]`): the second Code node runs and outputs `{ran:true}`. In n8n it is skipped.
2. Template 2896 (reduced): a Loop whose body contains a node with `alwaysOutputData` never finishes; the same workflow without the flag does.

Repros (scratchpad of the self-test): wf/12b-empty-alwaysOutputData.json, wf/12-loop-alwaysOutputData.json, wf/09-loop.json, wf/11-loop-2896-reduced.json.

# Expected

A node that receives no items is not run, whatever its alwaysOutputData; the loop ends.

# Actual

The node runs and emits `{}`; the loop never ends.

# Acceptance Criteria
- [ ] A node that gets no input items is not run even with alwaysOutputData (match n8n's exact rule, including nodes with several inputs and the Merge node).
- [ ] alwaysOutputData still emits one empty item when the node ran and returned nothing.
- [ ] The template-2896 shape finishes.
