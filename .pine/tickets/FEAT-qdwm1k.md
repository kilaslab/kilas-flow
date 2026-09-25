---
id: FEAT-qdwm1k
title: the node-authoring docs don't describe lifecycle capture, {{ .Captured.<key> }}, ParameterJSON or secretCapture
status: todo
priority: low
labels:
    - docs
    - packs
parent: EPIC-7c3ry9
created: "2026-09-23T07:35:21Z"
updated: "2026-09-25T09:57:34Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

`docs/src/content/docs/guides/node-authoring.md` is the node-authoring reference, but it does not describe several pieces of the trigger lifecycle mechanism a pack author needs to register a webhook-style trigger with a customer's service:

- Lifecycle `capture`: only a `set` lifecycle step may declare it; the fields it captures become readable elsewhere as `{{ .Captured.<key> }}` (internal/webhook/request_lifecycle.go ~51, ~66, ~100).
- `{{ .Captured.<key> }}` templating: how a captured field is referenced in a later lifecycle step's URL, headers, or body (internal/webhook/request_lifecycle.go ~131-299).
- `ParameterJSON.<key>`: the JSON-encoded form of a node parameter, for use where a raw string placeholder would break JSON structure, e.g. `"events": {{ .ParameterJSON.events }}` (internal/webhook/request_lifecycle.go ~423-439, ~632-652).
- `secretCapture`: an HMAC secret can be sourced from what a lifecycle `capture` kept, as an alternative to a static `secretParameter` — the two are mutually exclusive, and a `secretCapture` must name a key the lifecycle's `set` step actually captures (internal/nodepack/trigger.go ~76, ~209-217).

Without this in the docs, a pack author has to read the lifecycle implementation directly to build a trigger that self-registers with a customer's service.

# Acceptance Criteria
- [ ] The node-authoring docs describe lifecycle `capture` (what it is, which step declares it, what it's for)
- [ ] The docs describe `{{ .Captured.<key> }}` templating with an example
- [ ] The docs describe `ParameterJSON.<key>` and when to use it over a plain string placeholder, with an example
- [ ] The docs describe `secretCapture` on an HMAC-verified trigger, including that it's mutually exclusive with `secretParameter` and must name a captured key

# Implementation Plan

Add a section (or extend the existing trigger-lifecycle section) to `docs/src/content/docs/guides/node-authoring.md` covering the four items above, each with a short example drawn from the code's own doc comments where they already explain the reasoning (e.g. the Telegram-token-in-URL vs WAHA-header-credential distinction already documented on `call`).

# Notes

None of this needs new behavior — it's undocumented existing mechanism.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, all refs current. docs/src/content/docs/guides/node-authoring.md lifecycle/HMAC section (~344-430) mentions only `secretParameter` (367) and `sha512` (426); nothing under docs/src mentions `secretCapture`, `ParameterJSON` or `.Captured.`.
- Documents existing behaviour, so it does not wait on FEAT-hxztwz (dep intentionally not set); refresh the HMAC paragraph when FEAT-hxztwz lands.

# Related Files

docs/src/content/docs/guides/node-authoring.md (the page to extend)
internal/webhook/request_lifecycle.go ~40-70, ~90-105, ~120-300, ~420-440, ~620-655 (capture, `{{ .Captured.<key> }}`, `ParameterJSON.<key>`)
internal/nodepack/trigger.go ~70-80, ~205-218 (`secretCapture`)
