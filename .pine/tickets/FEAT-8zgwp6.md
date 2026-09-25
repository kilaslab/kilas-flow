---
id: FEAT-8zgwp6
title: Usage metering API
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - metering
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

The host bills its orgs per plan and credits (executions, AI tokens), and KilasFlow exposes no usage data.

# Acceptance Criteria
- [ ] `GET /api/v1/tenants/{id}/usage?from&to&granularity` for the operator, and `GET /api/v1/usage` for a tenant.
- [ ] Returns execution counts by status and trigger, execution time, node runs, and LLM tokens per model and credential.
- [ ] The format is stable and documented.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. No `/usage` route. LLM tokens live only in node output JSON (nodes/ai.go:1447) and ephemeral `ai.Event` usage (internal/ai/agent.go:146) — no per-model/credential ledger. Executions have status, trigger, StartedAt, FinishedAt (internal/repository/models.go:313-329).
- `execution.retention` (config.go:511-520) deletes old executions, so usage needs a rollup table, not queries over execution rows.

# Related Files

# Attachments
