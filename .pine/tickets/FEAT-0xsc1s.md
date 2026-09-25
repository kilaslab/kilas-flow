---
id: FEAT-0xsc1s
title: 'Embed postMessage protocol v2: token refresh without reload, dirty-state and expiry events, host commands'
status: todo
priority: high
labels:
    - saas
    - embedding
    - sdk
deps:
    - BUG-mzk0xn
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:33Z"
---

# Description

- Tokens live 15–30 minutes. On expiry the frame only shows a refresh error (`web/src/lib/embed/embed-editor.svelte:289-296`), and the SDK cannot push a new token: `MountedEditor` has only `iframe` and `unmount`.
- The frame emits `workflow-published`, but the SDK type omits it (`sdk/src/browser.ts:56-60`).
- There is no dirty-state event, so the host cannot warn before navigating away, and there are no host → editor commands.

# Acceptance Criteria
- [ ] `editor.setSession(newSession)` swaps the token without a reload.
- [ ] New events: `session-expiring` (60 s before expiry), `unauthorized`, `dirty-changed {dirty}`, a typed `workflow-published`, and `error`.
- [ ] Host commands `save()`, `run()` and `focusNode(id)`, with origin-checked replies.
- [ ] The protocol is versioned and documented.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, refs accurate. `workflow-published` is emitted only from the history `onPublished` path gated on `canPublish`, which is never true today — hence the dep on BUG-mzk0xn.
- Groundwork: the frame's message listener (session.svelte.ts:184-208) stays attached after the handshake and accepts a second `kilasflow:embed-session`, re-attaching the token. Check whether that remounts `EmbedEditor` before building `setSession`.
- The SDK handshake payload (sdk/src/browser.ts:127-136) does not send `expiresAt`; `session-expiring` needs it.

# Related Files

# Attachments
