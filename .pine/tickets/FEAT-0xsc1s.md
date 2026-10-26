---
id: FEAT-0xsc1s
title: 'Embed postMessage protocol v2: token refresh without reload, dirty-state and expiry events, host commands'
status: done
priority: high
labels:
    - saas
    - embedding
    - sdk
deps:
    - BUG-mzk0xn
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-10-25T15:47:57Z"
---

# Description

- Tokens live 15–30 minutes. On expiry the frame only shows a refresh error (`web/src/lib/embed/embed-editor.svelte:289-296`), and the SDK cannot push a new token: `MountedEditor` has only `iframe` and `unmount`.
- The frame emits `workflow-published`, but the SDK type omits it (`sdk/src/browser.ts:56-60`).
- There is no dirty-state event, so the host cannot warn before navigating away, and there are no host → editor commands.

# Acceptance Criteria
- [x] `editor.setSession(newSession)` swaps the token without a reload.
- [x] New events: `session-expiring` (60 s before expiry), `unauthorized`, `dirty-changed {dirty}`, a typed `workflow-published`, and `error`.
- [x] Host commands `save()`, `run()` and `focusNode(id)`, with origin-checked replies.
- [x] The protocol is versioned and documented.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, refs accurate. `workflow-published` is emitted only from the history `onPublished` path gated on `canPublish`, which is never true today — hence the dep on BUG-mzk0xn.
- Groundwork: the frame's message listener (session.svelte.ts:184-208) stays attached after the handshake and accepts a second `kilasflow:embed-session`, re-attaching the token. Check whether that remounts `EmbedEditor` before building `setSession`.
- The SDK handshake payload (sdk/src/browser.ts:127-136) does not send `expiresAt`; `session-expiring` needs it.

## Fix

Protocol v2, additive on both halves. `EMBED_PROTOCOL_VERSION = 2` in the frame (session.svelte.ts) and the SDK (browser.ts); the frame announces it with `embed-ready`, the SDK refuses commands up front against a protocol-1 frame rather than leaving promises hanging.

- Token refresh without reload: the frame's handshake listener was already long-lived and accepts a second `kilasflow:embed-session`; `acceptEmbedSession` now reads `expiresAt` (absent/invalid → null, never a refusal). The SDK's `setSession(newSession)` posts that second message — same workflow only, after the handshake, to the exact editor origin. No remount anywhere: `EmbedEditor` is not keyed on the session object and `canvasFrom` is untouched.
- `session-expiring`: scheduled in `embedSession()` via the pure `expiryNoticePlan` (60 s ahead; immediate when already inside the window or past expiry; rescheduled by a refresh; cleared on teardown).
- `unauthorized`: new `setEmbedUnauthorizedListener` hook in `api/http.ts`, fired on a 401 while an embed token is attached (fetch + download). The embed editor latches it once per token (re-armed when `session.token` changes) and posts the event.
- `dirty-changed {dirty}`: the editor's `onDirtyChange` now also notifies the host (including the settled `dirty: false` after a save).
- Typed `workflow-published` and `error {code, message}` (load-failed / save-failed / conflict / run-failed) join the SDK's `EditorEvent`.
- Commands: frame half in `web/src/lib/embed/commands.ts` (strict parse; well-formed unknown commands are answered, junk is ignored) wired in `embed-editor.svelte` behind `event.origin === session.origin && event.source === window.parent`; replies are `kilasflow:command-result` to the verified origin. `save()` → `editorInstance.saveDraft()` and reports saved/dirty/revision or conflict/save-failed; `run()` → the new `WorkflowEditor.runDraft()` export (the toolbar Execute's exact semantics, returning the terminal execution; chat graphs open the chat panel and answer `executionId: null`); `focusNode(id)` → the new `focusNode` export (select + inspector, `unknown-node` refusal). SDK correlates by requestId, never forwards replies to `onEvent`, defaults a 15 s command timeout (run exempt), and fails pending commands on unmount.
- Docs: embedding.md "The postMessage protocol, v2" (event table, refresh, commands) + the fifteen-minute token paragraph; tenancy-and-embedding.md diagram; SDK README.

Audit follow-ups: both were confirmed before building — the repeat session message does not remount EmbedEditor, and expiresAt now travels in the handshake payload.

# Related Files

# Attachments
