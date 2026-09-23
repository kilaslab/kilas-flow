---
id: BUG-ppvyzr
title: Workflow Tool calling its own workflow ({{ $workflow.id }}) imports into a shape validate rejects
status: todo
priority: medium
labels:
    - importer
    - ai
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-09-23T01:35:50Z"
---

# Description

The recursive-agent pattern appears in 10 of the top 198 templates, including 2006 (211K views).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A Workflow Tool whose `workflowId` locator is `={{ $workflow.id }}` calls the workflow it sits in, which is the usual recursive-agent pattern. It appears in 10 of the top 198 templates: 2006 (211K views), 2026, 2085, 3514, 3770, 2094, 2328, 3135, 3025 and 2035.

# Steps to Reproduce

1. Run `minimal_repros.py`, case `workflow_tool_self` (toolWorkflow v2 with `workflowId: {"__rl":true,"mode":"id","value":"={{ $workflow.id }}"}`).
2. Import template 2006.

# Expected

The self-reference imports as a locator the validator accepts, so the recursive agent is runnable. The code comment says that is the intent.

# Actual

- The import reports no issue for the tool, and stores `workflowId: {"mode":"expression","value":"{{ $workflow.id }}"}`: a bare expression, not a resource locator.
- `validate` fails with `node "w" configuration is invalid: workflowId is required`. The same happens in 2006.
- Re-validating the same document with the locator shape `{"__rl":true,"mode":"id","value":{"mode":"expression","value":"{{ $workflow.id }}"}}` passes.

# Acceptance Criteria
- [ ] The `$workflow.id` self-reference imports as a resource locator the validator accepts (or `LocatorIsSet` accepts an expression)
- [ ] Template 2006's workflow tool validates and runs

# Implementation Plan

Wrap the self-reference in `property.WriteLocator(property.Locator{Mode:"id", Value: expressionValue(...)})`, as the Execute Workflow translator does. Alternatively, make `LocatorIsSet` accept an expression.

# Notes

Related tickets: FEAT-j5s2n4

Related (from the audit): FEAT-j5s2n4 (done) listed "`$workflow.id` workflow tools 7" among its blockers.

# Related Files

- `internal/interop/n8n/parameters.go:5220-5227` writes `expressionValue(n8nSelfWorkflowTemplate)` directly.
- `nodes/ai.go:2541-2546`: `validateWorkflowToolConfiguration` requires `property.LocatorIsSet`, and `ReadLocator` fails on a bare expression.
- `executeWorkflowToKilas` (parameters.go:2479-2482) wraps the same self-reference in `property.WriteLocator` correctly.
- `minimal_repros.json`, `import/results.json["2006"].validate`.

# Attachments
