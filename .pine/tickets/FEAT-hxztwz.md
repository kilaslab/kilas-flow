---
id: FEAT-hxztwz
title: 'Pack-trigger signature verification: sha512-only, secret stored in the workflow document; add sha256/Standard Webhooks, credential-held secret'
status: done
priority: high
labels:
    - saas
    - packs
    - webhooks
    - security
deps:
    - FEAT-n12211
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-10-25T16:29:37Z"
---

# Description

Pack triggers verify only `sha512` over the raw body (`internal/nodepack/trigger.go:60-71,178-179`).
- The secret is a node parameter (`secretParameter`), so it is stored in the workflow document. That contradicts "A workflow document never contains a secret" (`docs/src/content/docs/concepts/credentials.md:8`).
- Common schemes cannot be verified: Stripe or Standard Webhooks style HMAC-SHA256 over `timestamp.body`, `v1=` prefixes, and two signatures during a secret rotation.

# Acceptance Criteria
- [x] The `hmac` block accepts: `algorithm: sha256 | sha512`; `encoding: hex | base64`; `prefix`; `signingString`, a template over the body, the timestamp and header values; `timestampHeader` with `toleranceSeconds`; multiple comma-separated signatures
- [x] The secret can come from a credential field.
- [x] There is a `standardWebhooks` preset.
- [x] A missing, bad or stale signature is refused before any execution exists.
- [x] Docs and tests are updated.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Stale refs, partly outdated. `TriggerHMAC` is trigger.go:60-77, sha512-only check :206-207, verifier built :395-396; the actual HMAC (hex only, body only, no prefix) is internal/webhook/shape.go:523-544 — algorithm/encoding/signing-string changes land there.
- Since BUG-vsmnby, `secretCapture` exists (trigger.go:71-76, :209-217, :441-454) and keeps the secret in the route's lifecycle state (internal/repository/webhooks.go:39, migration 000023), not in the document. "Secret stored in the workflow document" now holds only for `secretParameter`.
- Still missing: sha256, base64, prefix, timestamp tolerance, multiple signatures, credential-held secret, `standardWebhooks` preset.

## Fix

Audit follow-ups held: the algorithm/encoding/signing-string work landed in internal/webhook/shape.go as the audit predicted; the "secret stored in the workflow document" half is answered with a third source (`secretCredential`) rather than removing `secretParameter` — WAHA and every imported workflow keep working, and the doc now names the storage-rule-compliant path.

- `HMACVerifier` (hex sha512 over the raw body, whole-header compare) is replaced by `HMACSchemeVerifier(HMACScheme)`: algorithm sha256|sha512, encoding hex (case-insensitive, as before)|base64 (exact — lowercasing base64 breaks its alphabet), prefix, signingString template in the house `{{ .Field }}` style over `{{ .body }}`, `{{ .timestamp }}` (requires `timestampHeader`) and `{{ .header.<name> }}`, timestamp tolerance (unix seconds or RFC 3339), and comma-separated multi-signature acceptance with any-match for rotations. A declared prefix is load-bearing: `v1,` schemes re-join the pair the prefix's own comma split; `sha256=` schemes skip unprefixed tokens. Unknown placeholders (including expressions) are refused at pack-validation time by `webhook.CheckSigningString`, so a typo is an authoring error, not a silently literal signing string.
- `TriggerHMAC` gains `encoding`, `prefix`, `signingString`, `timestampHeader`, `toleranceSeconds`, `preset` and `secretCredential {type, field}`. `standardWebhooks` fills the whole spec scheme (webhook-signature, sha256, base64, `v1,`, `{{ .timestamp }}.{{ .body }}`, webhook-timestamp, 300 s) and is exclusive with every scheme field — one word plus the secret. At most one of secretParameter/secretCapture/secretCredential.
- The credential secret is resolved per delivery through the existing `Delivery.Credential` closure (the Telegram path): unbound reads as not-configured, bound-but-unreadable or a missing field refuses every delivery. `RegisterTrigger` refuses a `secretCredential` whose type is not the pack's own `credentialType` — the only types a binding ever carries.
- Refusal-before-execution unchanged: `kind.Verify` runs in the HTTP boundary before the run is queued, and a missing/bad/stale signature is an error there.
- Docs: node-authoring.md (scheme table, `secretCredential` section, preset section, validator paragraph), reference/node-packs.md trigger field list.

Tests: internal/webhook/hmac_scheme_test.go, internal/nodepack/trigger_signature_test.go; existing WAHA-shape and capture tests untouched and green.

# Related Files

# Attachments
