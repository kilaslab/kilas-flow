---
id: FEAT-gzd32h
title: Input and output contract for workflows used as tools
status: todo
priority: low
labels:
    - saas
    - api
    - ai-tools
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A host's AI agent calls workflows as synchronous tools and needs a machine-readable input schema. The Execute Workflow Trigger's "Expected fields" are not exposed as JSON Schema.

# Acceptance Criteria
- [x] `GET /workflows/{id}/contract` returns the input JSON Schema (from the trigger fields) and the declared output.
- [x] Aligned with FEAT-nq1vsx (MCP Server Trigger) and per-tenant authentication.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. No `/workflows/{id}/contract` route. Source data exists: `kilasflow.executeWorkflowTrigger` `inputSource` + `workflowInputs` (nodes/subworkflow.go:118-147); the only JSON-Schema derivation today is `$fromAI` (nodes/ai.go:1984-1990).
- Underspecified: nothing declares a workflow's output today — define where "the declared output" comes from before implementing. FEAT-nq1vsx is still todo.

# Related Files

# Attachments

## Progress

2026-11-27, on `feat/epic-sweep-2026-11-27`:

- `get-workflow-contract` (internal/api/handlers/workflow_contract.go) describes the active revision, or the latest draft when the workflow is inactive (`source`). `entry` names the sub-workflow trigger a call starts from (null without one).
- Input: the trigger's declared fields through `nodes.WorkflowInputsSchema`. This is the same declaration the AI Agent's workflow tool offers a model, so the contract and the in-product tool cannot drift. The schema is `{"type":"object","properties":…,"additionalProperties":true}`, because declared fields are a check, never a filter. A passthrough trigger is `declared: false` with `{"type":"object"}`.
- Output: the document's `settings.outputSchema` when it is a JSON object (`declared: true`); otherwise `declared: false`. Nothing else in a document states an output.
- Auth: tenant-scoped like every workflow read; a scoped key needs `workflow:read`; an embed session or a bound key may read its own workflow's contract (scope test row added). Alignment with FEAT-nq1vsx: the MCP Server Trigger, when built, should publish tools from `nodes.WorkflowInputsSchema` too.
- Docs: saas-playbook.md "Reading a declared contract", api-contract.md, and the generated reference. Tests: handlers unit tests (active vs draft, typed properties, undeclared sides, no trigger) and an API route test (404 across tenants).
- Not done: no editor UI for `settings.outputSchema`.
