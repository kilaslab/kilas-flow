---
id: BUG-t9e3k5
title: n8n import reports no blocking issue for the other topology refusals run and activate make
status: todo
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
- [ ] Every compile refusal a clean import can produce is reported at import as blocking, from the compiler's own checks (not a copy of them), naming the nodes.
- [ ] Tests cover each refusal class.
