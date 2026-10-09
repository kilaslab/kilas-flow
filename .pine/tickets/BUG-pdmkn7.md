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
- [x] Activation of a parent with an inactive sub-workflow answers a 4xx naming the node and the child workflow, through the API, the CLI and MCP. Evidence: `TestActivatingAParentWhoseSubworkflowIsInactiveAnswers422NamingTheNodeAndTheChild` (internal/api), `TestActivatingAParentWithTwoBadSubworkflowCallsNamesBothNodes` (one `errors[]` entry per bad node), `TestWorkflowActivateSaysWhichNodeNeedsWhichInactiveChild` and `TestMCPWorkflowActivateReturnsTheSameRefusal` (internal/cli, real API + stores).
- [x] A test pins the status code and message; the 500 path is gone. Evidence: the API and CLI tests above assert 422, `application/problem+json`, the full sentence (node, child name and id, "not active", "activate that workflow first"), `errors[]` entries of `config.invalid` on each `nodeId` at `body/nodes/<i>/parameters/workflowId`, and that the detail is not "workflow operation failed"; they failed with the 500 before the change. `TestPublishingAParentWhoseSubworkflowIsInactiveAnswersTheSame422` covers the publish route, `TestActivatingAParentWhoseSubworkflowIsGoneAnswers422SayingItDoesNotExist` the missing-target variant, `TestActivationRefusesEveryBadSubWorkflowCallAtOnce` (internal/repository) three bad calls and one live one.
- [x] The triggers skill / SUB_WORKFLOWS.md describe the real behaviour. Evidence on the merged tree: skills/kilasflow-triggers/SKILL.md (sub-workflow bullet) and skills/kilasflow-flow-patterns/references/SUB_WORKFLOWS.md ("The child must be active") state the 422 `config.invalid`, the node and the child named by name and id, and the separate "does not exist" sentence, citation-free as main's source-citations rule requires; `go test ./internal/skills/...` passes.


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


## Progress (2026-10-09)

Root cause as planned: `refuseInactiveSubworkflows` returned a `fmt.Errorf`, `Workflows.problem` had no case for it and answered `serverProblem(..."workflow operation failed")`.

What changed.
- internal/repository/workflows.go: `SubworkflowRefusalError` (node id/name, JSON-pointer path of the `workflowId` parameter, child id and name, `Missing`). The inactive sentence now names the child as `"Billing" (wf_...)`; the missing-target sentence is unchanged. `errors.As`-able, so the editor and every API caller can tell the two apart.
- internal/api/handlers/workflows.go: `problem` maps it through `subworkflowProblem` to a 422 whose `detail` is the whole sentence (the CLI envelope's `error.message` and the MCP tool result are built from `detail`, so neither needed code) plus one `errors[]` entry at `body/nodes/<i>/parameters/workflowId` carrying a `WorkflowValidationIssue{code: config.invalid, nodeId}`. There is no unknown-credential activation refusal on main (a missing credential is the compiler's `config.required` via `compileProblem`), so I matched the compile refusals' shape: 422 plus that value.
- Tests: internal/repository/subworkflow_activation_test.go (typed error, fields, path), internal/api/subworkflow_activation_test.go (new file, own `newSubworkflowAPI` helper because the shared helper leaves the extractor off, which is why this went unnoticed), internal/cli/verbs_workflow_subworkflow_test.go (CLI exit 2 / `bad_request` / status 422 and MCP `workflow_activate`, against real stores).
- skills/kilasflow-triggers/SKILL.md: sentence corrected; `make generate-skills-index-check` still clean.

Deviations. The ticket mentions SUB_WORKFLOWS.md: the `kilasflow-flow-patterns` skill is not on main, so no edit there. The CLI exit for a 422 is the existing usage-class exit (`bad_request`); I did not add a new CLI ErrCode.

Left: nothing.

## Review round (2026-10-09)

Merged main (Track H2's skills) into the branch. The three skills conflicts (kilasflow-triggers, kilasflow-node-configuration SKILL.md and references/LOAD_OPTIONS.md) were resolved to main's side, because mine cited repository source paths, which main's `source-citations` rule forbids, and used `api activate-workflow` where main uses `workflow activate --yes`. The reviewer's follow-up patch was then applied and updated triggers SKILL.md and flow-patterns references/SUB_WORKFLOWS.md to the 422 behaviour, so the Progress note above that says SUB_WORKFLOWS.md "does not exist on main" is superseded: it exists now and is correct.

Review fixes.
- `refuseInactiveSubworkflows` no longer returns on the first bad call: `SubworkflowRefusalError` now holds `Refusals []SubworkflowRefusal` (one per bad node, in document order, a live child left out), and `subworkflowProblem` answers one `errors[]` entry per node with the sentences joined in `detail`. Tests: `TestActivationRefusesEveryBadSubWorkflowCallAtOnce`, `TestActivatingAParentWithTwoBadSubworkflowCallsNamesBothNodes`.
- web/src/lib/workflow-editor/validation.ts `withNodeNames` replaced a node id as a bare substring, so an id like `call` corrupted `calls workflow` and `Run billings`. It now replaces only occurrences that stand alone (no word, dot or dash character on either side), reading the id literally. Three vitest cases in validation.test.ts; `pnpm check` 0 errors and `pnpm test` 895 passed.
- CHANGELOG.md [Unreleased] has the Fixed entry.
- The main-side `refuseUnknownCredentials` refusal returns a `workflow.ValidationErrors` (message "workflow validation failed"); this refusal keeps its own typed error so that `detail` carries the sentence itself, which is what the CLI envelope and the MCP result print. Both are 422 `config.invalid` with one entry per node.
