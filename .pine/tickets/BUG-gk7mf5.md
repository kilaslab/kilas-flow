---
id: BUG-gk7mf5
title: n8n import reports no blocking issue for a cycle that run and activate refuse
status: todo
priority: medium
labels:
    - import
    - n8n
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Found by the 2026-09-25 Code-node self-test. Templates 2536 and 3655 import with no blocking diagnostic, but run and activate both fail with `workflow.invalid_topology` (2536 loops If → Wait → Code → Respond to Webhook → If). Refusing such cycles at run time is documented, but the migration guide promises that "no blocking entries means the workflow will activate".

# Expected

The import report carries a blocking diagnostic naming the cycle (the same check run and activate use), so the report and the run agree.

# Acceptance Criteria
- [ ] Import reports a blocking diagnostic for any topology run/activate would refuse, naming the nodes in the cycle.
- [ ] A test covers the 2536 shape.
