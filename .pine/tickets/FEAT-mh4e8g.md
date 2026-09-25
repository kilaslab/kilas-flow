---
id: FEAT-mh4e8g
title: Test and dry-run protocol for host-API nodes
status: todo
priority: medium
labels:
    - saas
    - packs
    - testing
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

The host needs editor test runs not to send real customer WhatsApp messages or change live CRM data. Pack requests cannot tell whether they belong to an editor test. `$execution.mode` exists in expressions (`internal/expression/roots.go:229-235`), but whether pack request templates can use it is undocumented.

# Acceptance Criteria
- [ ] Every pack request carries `X-KilasFlow-Execution-Mode` and `X-KilasFlow-Execution-Id`.
- [ ] The editor and SDK can flag a manual run `test: true`. The flag is exposed as `$execution.test` and forwarded as a header.
- [ ] Documented as the dry-run contract for host APIs.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Stale refs, gap real. roots.go:229-235 is now the root allowlist; the `$execution` map (id, mode, resumeUrl, approvalUrl) is roots.go:263-269, mode from `record.Trigger` (internal/engine/eval.go:209); no `test` field.
- Pack routing resolves through `request.ExpressionContext` (internal/routing/executor.go:110), so `$execution.mode` likely already works in pack parameters (inferred, not tested); lifecycle templates only have Parameter/ParameterJSON/Captured (request_lifecycle.go:523).
- No `X-KilasFlow-Execution-*` header exists. "Every pack request" = routing (executor.go:242-245), wasmpack, sidecarnode and lifecycle `call`.

# Related Files

# Attachments
