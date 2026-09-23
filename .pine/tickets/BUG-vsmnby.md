---
id: BUG-vsmnby
title: "Pack-trigger lifecycle hooks: values substituted without JSON escaping (injection), scalar-only params, no response capture"
status: todo
priority: high
labels:
    - saas
    - packs
    - webhooks
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

- Lifecycle templates receive only scalar parameters (`internal/webhook/request_lifecycle.go:119-130`), so multi-select events, collections and conditions cannot be registered with the remote service.
- Values are substituted without JSON escaping (`request_lifecycle.go:211-222`).
- Response data such as a subscription id or a generated secret cannot be captured for later `remove` or verification.

# Acceptance Criteria
- [ ] Structured parameters are available JSON-encoded.
- [ ] Substitution in a JSON context escapes values.
- [ ] `set` may declare `capture: { key: jsonPath }`. The captured values persist on the binding and are usable by `check`, `remove` and the HMAC secret lookup.
- [ ] Tests cover injection attempts.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
