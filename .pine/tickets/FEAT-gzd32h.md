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
- [ ] `GET /workflows/{id}/contract` returns the input JSON Schema (from the trigger fields) and the declared output.
- [ ] Aligned with FEAT-nq1vsx (MCP Server Trigger) and per-tenant authentication.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. No `/workflows/{id}/contract` route. Source data exists: `kilasflow.executeWorkflowTrigger` `inputSource` + `workflowInputs` (nodes/subworkflow.go:118-147); the only JSON-Schema derivation today is `$fromAI` (nodes/ai.go:1984-1990).
- Underspecified: nothing declares a workflow's output today — define where "the declared output" comes from before implementing. FEAT-nq1vsx is still todo.

# Related Files

# Attachments
