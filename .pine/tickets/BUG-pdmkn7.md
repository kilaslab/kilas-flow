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


## Plan (2026-10-09)

Root cause. `refuseInactiveSubworkflows` (internal/repository/workflows.go:561) builds its refusal with `fmt.Errorf`, so the error that comes out of `publish` (workflows.go:513) is an untyped string. Both the activate and the publish handlers send it through `Workflows.problem` (internal/api/handlers/workflows.go:1027), which knows `ErrNotFound`, `WebhookConflictError`, `workflow.ValidationErrors` and `ErrNeverFires` and sends everything else to `serverProblem(ctx, "workflow operation failed", err)`: a 500 whose reason only reaches the log. The refusal already names the node and the child *id*, but never the child's *name*, and no caller can see it.

The change.
1. internal/repository/workflows.go: a typed `SubworkflowRefusalError` (node id and name, the path of the node's `workflowId` parameter, the child workflow id and name, and whether the child is missing or only inactive). `refuseInactiveSubworkflows` returns it; its `Error()` keeps today's wording ("node X (id) calls workflow ..., which is not active: activate that workflow first, then activate this one" / "does not exist in this workspace") and adds the child's name when it has one.
2. internal/api/handlers/workflows.go: `problem` maps it with `errors.As` to a 422, the status the compile refusals and the never-fires cron refusal already use for "the document you activated cannot go live". `detail` is the full sentence (so the CLI envelope's `error.message` and an MCP tool result carry it, because both read `detail`), and `errors[]` carries one entry at `body/nodes/<i>/parameters/workflowId` whose value is a `WorkflowValidationIssue{code: "config.invalid", nodeId}`, so the editor can still point at the node. There is no separate unknown-credential refusal on this path (a missing credential is the compiler's `config.required`, mapped by `compileProblem`); the new mapping reuses the same `WorkflowValidationIssue` value shape.
3. The CLI and MCP need no code: the CLI client turns any problem document into the envelope with `problem.detail` as the message, and `kilasflow mcp serve` returns the CLI envelope verbatim. They are proven by a test, not changed.
4. Docs: skills/kilasflow-triggers/SKILL.md line 66 says what the refusal is (422 `config.invalid`, node and child named) instead of implying it. `skills/kilasflow-flow-patterns/references/SUB_WORKFLOWS.md` does not exist on main (no `kilasflow-flow-patterns` skill yet), so there is nothing to fix there and I will say so in the report.

Proof (test first).
- internal/repository/subworkflow_activation_test.go: `errors.As` finds `*SubworkflowRefusalError` with the node, child id, child name and `Missing` set correctly for an inactive and for a missing target.
- A new internal/api test with the production `nodes.SubworkflowCalls` wired into the store: an inactive child gives 422 `application/problem+json`, a `detail` naming the node and the child's name and id, `errors[0].value.code == "config.invalid"`, `nodeId`, the workflow stays inactive, and activating the child first makes the parent activate (200). The same through `POST /workflows/{id}/versions/{versionId}/publish`.
- A CLI test that runs `workflow activate` against that real handler stack and asserts exit code, `error.status` 422 and `error.message` naming the node and child; the MCP tool result is that same envelope, asserted in the mcp-serve test if the harness allows it.
