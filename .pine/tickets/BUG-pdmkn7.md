---
id: BUG-pdmkn7
title: Activating a parent whose sub-workflow is inactive answers 500 instead of a refusal naming the node and the child
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:25:15Z"
updated: "2026-10-09T12:25:15Z"
---

# Description

Activating a parent workflow whose Execute Workflow / Workflow Tool target is inactive is refused (`refuseInactiveSubworkflows` in internal/repository/workflows.go), but the API answers a generic **500 "workflow operation failed"**. Only the server log names the child workflow. The caller (dashboard, CLI, an agent) gets no actionable reason, and the triggers skill's claim that the refusal names the node is false.

Reported by the Track H2 coder in the 2026-10-09 parallel wave while validating skill examples against a real server.

# Steps to Reproduce

1. Create a child workflow starting with an Execute Workflow Trigger; leave it inactive.
2. Create a parent with an Execute Workflow node pointing at the child.
3. `kilasflow workflow activate <parent> --yes` (or POST /workflows/{id}/activate).

# Expected

A 4xx (422 `config.invalid` or 409) whose error names the parent node, and the child workflow id/name, and says to activate the child first.

# Actual

500 "workflow operation failed"; the reason only appears in the server log.

# Acceptance Criteria
- [ ] Activation of a parent with an inactive sub-workflow answers a 4xx naming the node and the child workflow, through the API, the CLI and MCP.
- [ ] A test pins the status code and message; the 500 path is gone.
- [ ] The triggers skill / SUB_WORKFLOWS.md describe the real behaviour.
