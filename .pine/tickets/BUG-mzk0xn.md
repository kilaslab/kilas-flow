---
id: BUG-mzk0xn
title: Consistent publish and activate authority for embeds
status: todo
priority: medium
labels:
    - saas
    - embedding
    - authz
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

- Activation is refused to every embed session (`internal/api/middleware/scope.go:242-245`). That is a sound default, but a white-label host has no way to opt in.
- Publishing a version is allowed to embeds holding `workflow:write`: the refusal at `scope.go:246-250` applies only to API keys, and `embedscope.go:80-85` confirms it.
- Meanwhile the frontend hides publish, waiting for a `workflow:publish` scope the server never mints (`web/src/lib/embed/session.svelte.ts:70-86`).

# Acceptance Criteria
- [ ] The server mints `workflow:publish` and, optionally, `workflow:activate`.
- [ ] Publishing from an embed requires `workflow:publish`.
- [ ] Activating with the scope reuses the compile and `node.not_available` checks.
- [ ] Server, UI and docs agree on the three levels.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, refs accurate (scope.go:242-250, internal/embed/embed.go:33-35, `NormalizeScopes` :466, web/src/lib/embed/session.svelte.ts:70-86, embed-editor.svelte:36).
- Also fix the false comment at embedscope.go:59-61 ("activation and publishing are refused to embed sessions by the routing middleware") — publish is not refused; :76-84 contradicts it.
- guides/embedding.md:271 says nothing about publish; update with the three levels.

# Related Files

# Attachments
