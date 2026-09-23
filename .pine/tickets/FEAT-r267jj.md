---
id: FEAT-r267jj
title: Host-declared confinement for embed sessions
status: todo
priority: high
labels:
    - saas
    - embedding
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A workflow session's confinement (the credentials, data tables and sub-workflows its document may reference) is derived at mint from the revision the owner authored (`internal/api/handlers/embed.go:95-131`, `internal/api/handlers/embedscope.go:55-74`). So an end user building a new automation inside the embed cannot select a credential or data table that the host provisioned for their org, unless the host rewrites the workflow document first. A blank workflow can reference nothing. None of this appears in the guides.

# Acceptance Criteria
- [ ] `POST /api/v1/embed-sessions` accepts `allow: { credentials?: string[] | "tenant", datastores?: string[] | "tenant", workflows?: string[] | "tenant" }`. Every id is validated against the minting tenant, and a foreign or unknown id returns 404.
- [ ] `"tenant"` is accepted only from an unbound key.
- [ ] The token carries the declared set. Save, publish, restore and run accept the union of the derived and declared sets.
- [ ] The embedded credential picker lists exactly the allowed credentials.
- [ ] The embedding guide documents confinement, the 403 message and these options.
- [ ] Tests cover a cross-tenant id, an unlisted id and `"tenant"`.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
