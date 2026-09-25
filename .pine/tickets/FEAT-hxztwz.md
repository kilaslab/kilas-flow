---
id: FEAT-hxztwz
title: 'Pack-trigger signature verification: sha512-only, secret stored in the workflow document; add sha256/Standard Webhooks, credential-held secret'
status: todo
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
updated: "2026-09-25T09:52:33Z"
---

# Description

Pack triggers verify only `sha512` over the raw body (`internal/nodepack/trigger.go:60-71,178-179`).
- The secret is a node parameter (`secretParameter`), so it is stored in the workflow document. That contradicts "A workflow document never contains a secret" (`docs/src/content/docs/concepts/credentials.md:8`).
- Common schemes cannot be verified: Stripe or Standard Webhooks style HMAC-SHA256 over `timestamp.body`, `v1=` prefixes, and two signatures during a secret rotation.

# Acceptance Criteria
- [ ] The `hmac` block accepts: `algorithm: sha256 | sha512`; `encoding: hex | base64`; `prefix`; `signingString`, a template over the body, the timestamp and header values; `timestampHeader` with `toleranceSeconds`; multiple comma-separated signatures
- [ ] The secret can come from a credential field.
- [ ] There is a `standardWebhooks` preset.
- [ ] A missing, bad or stale signature is refused before any execution exists.
- [ ] Docs and tests are updated.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Stale refs, partly outdated. `TriggerHMAC` is trigger.go:60-77, sha512-only check :206-207, verifier built :395-396; the actual HMAC (hex only, body only, no prefix) is internal/webhook/shape.go:523-544 — algorithm/encoding/signing-string changes land there.
- Since BUG-vsmnby, `secretCapture` exists (trigger.go:71-76, :209-217, :441-454) and keeps the secret in the route's lifecycle state (internal/repository/webhooks.go:39, migration 000023), not in the document. "Secret stored in the workflow document" now holds only for `secretParameter`.
- Still missing: sha256, base64, prefix, timestamp tolerance, multiple signatures, credential-held secret, `standardWebhooks` preset.

# Related Files

# Attachments
