---
id: FEAT-pt6ge9
title: Dynamic option loading (`loadOptions`) in node packs
status: todo
priority: high
labels:
    - saas
    - packs
    - loadoptions
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

The host's WhatsApp/CRM actions need pickers filled from the host API: channels, agents, tags, team members and message templates. Packs support only static options; the only internal loader is the operation cascade (`internal/nodepack/nodepack.go:255-290`, `internal/property/property.go:298-318`). Free-text ID fields were the top usability blocker in the host's own engine.

# Acceptance Criteria
- [ ] A parameter may declare `typeOptions.loadOptions: { request: {method, url, qs}, output: { rootProperty, value, label, description? }, dependsOn?: [keys] }`.
- [ ] The request runs with the node's credential under the egress policy and, in an embed, is bounded to the session's workflow.
- [ ] It works for `options`, `multiOptions` and `resourceLocator` list mode.
- [ ] Errors show inline.
- [ ] `nodepackgen` validates the block.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
