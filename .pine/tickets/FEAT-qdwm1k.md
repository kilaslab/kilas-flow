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
- [x] The node-authoring docs describe lifecycle `capture` (what it is, which step declares it, what it's for) — guides/node-authoring.md, `#### capture: keep values from the registration answer`: only `set` declares it, key/path/value rules, sealed storage, cleared after remove, unkept-answer cleanup
- [x] The docs describe `{{ .Captured.<key> }}` templating with an example — `#### {{ .Captured.<key> }}: read them back`; example is the `check`/`remove` URLs `.../api/subscriptions/{{ .Captured.id }}` in the full `pack.acmeSubscriptionTrigger` pack; escaping per placement and missing-key behaviour stated
- [x] The docs describe `ParameterJSON.<key>` and when to use it over a plain string placeholder, with an example — `#### {{ .ParameterJSON.<key> }}: lists and objects`; example `"events":{{ .ParameterJSON.subscribedEvents }}` in the pack body, plus the rendered result and the refusal a bare `Parameter` text gets outside a string
- [x] The docs describe `secretCapture` on an HMAC-verified trigger, including that it's mutually exclusive with `secretParameter` and must name a captured key — `#### secretCapture: verify with a secret the service generated`; the pack sets `"secretCapture": "secret"`; refusal cases and the refuse-until-captured window stated

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

## Progress (2026-10-16)

- Added `### Keeping what the service answers` to `docs/src/content/docs/guides/node-authoring.md` (Step 5: triggers), with one complete trigger pack (`pack.acmeSubscriptionTrigger`) using `capture`, `{{ .Captured.id }}` in `check`/`remove`, `{{ .ParameterJSON.subscribedEvents }}` in the `set` body and `hmac.secretCapture`, then one short subsection per mechanism. The existing "The pieces" paragraph now mentions `secretParameter`/`secretCapture` and `check`/`remove`/`capture`.
- Cross-references: one sentence in `docs/src/content/docs/reference/node-packs.md` and a short paragraph in `skills/kilasflow-node-packs/references/PACK_FORMAT.md` (cited as `(docs: guides/node-authoring)`, no repository path).
- No pack under `packs/` uses `capture` yet (the WAHA trigger pack registers through its Go hook), so the example was validated against the code instead: the pack passes `nodepack.ValidateBytes` with no issues, and the `set` body and `check`/`remove` URL render as documented through `substituteBody`/`substituteURL` (temporary tests, removed after running).
- Verified: `make docs-build` (52 pages, all internal links valid), `go test -count=1 ./internal/skills/...`, `make generate-docs-facts-check`, `go test ./internal/docexamples/... ./internal/guardrails/...`.
- Left for FEAT-hxztwz: refresh the HMAC paragraph when other algorithms land (the doc still says `sha512` is the only implemented algorithm).
