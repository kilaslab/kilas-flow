---
id: FEAT-s3sfx5
title: Actor identity in embed sessions
status: todo
priority: medium
labels:
    - saas
    - embedding
    - audit
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

The token names the tenant, workflow, scopes and origin, but not the user (`internal/embed/embed.go:148-171`). Version history, publish events and manual runs therefore cannot say which host user acted.

# Acceptance Criteria
- [ ] Minting accepts `actor: { id, displayName }`, which is signed into the token.
- [ ] The actor is recorded on versions, publish events and manually queued executions, returned by the API, and shown in history.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, but the plumbing is half there. Versions and publish events already carry actor columns (internal/repository/models.go:252-265, :297-305) filled by `audited()` (internal/api/handlers/workflows.go:958-976); API enums are `user,key` (workflows.go:178, :199).
- Work = an `embed` actor kind + mapping the session actor in `audited()`. Only executions lack actor fields (schema change needed there).

# Related Files

# Attachments
